package rebalancer

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/github"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/queue"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state/memstore"
)

type fakeGH struct {
	jobs []github.QueuedJob
	err  error
}

func (f *fakeGH) ListQueuedWorkflowJobs(_ context.Context, _ string) ([]github.QueuedJob, error) {
	return f.jobs, f.err
}

type fakePub struct {
	published []*queue.ScaleUpMessage
	err       error
}

func (f *fakePub) PublishScaleUp(_ context.Context, m *queue.ScaleUpMessage) error {
	if f.err != nil {
		return f.err
	}
	f.published = append(f.published, m)
	return nil
}

func TestRebalance_NoQueuedNoPublishes(t *testing.T) {
	gh := &fakeGH{jobs: nil}
	store := memstore.New()
	pub := &fakePub{}

	if err := Rebalance(context.Background(), gh, store, pub, "owner/repo", 42); err != nil {
		t.Fatalf("Rebalance: %v", err)
	}
	if len(pub.published) != 0 {
		t.Errorf("expected 0 publishes, got %d", len(pub.published))
	}
}

func TestRebalance_DemandExceedsSupply_PublishesGap(t *testing.T) {
	gh := &fakeGH{jobs: []github.QueuedJob{
		{JobID: 1, RunID: 100, Status: "queued", Labels: []string{"self-hosted", "large"}},
		{JobID: 2, RunID: 100, Status: "queued", Labels: []string{"self-hosted", "large"}},
		{JobID: 3, RunID: 200, Status: "queued", Labels: []string{"self-hosted", "large"}},
		{JobID: 4, RunID: 200, Status: "queued", Labels: []string{"self-hosted", "large"}},
		{JobID: 5, RunID: 200, Status: "queued", Labels: []string{"self-hosted", "large"}},
	}}
	store := memstore.New()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := store.Put(ctx, state.Runner{
			ID:     fmt.Sprintf("r%d", i),
			Status: state.StatusPending,
			Labels: []string{"self-hosted", "large"},
		}); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	pub := &fakePub{}

	if err := Rebalance(ctx, gh, store, pub, "owner/repo", 42); err != nil {
		t.Fatalf("Rebalance: %v", err)
	}
	if len(pub.published) != 3 {
		t.Errorf("expected 3 publishes (5 demand - 2 supply), got %d", len(pub.published))
	}
	for _, m := range pub.published {
		if m.Source != queue.SourceRebalancer {
			t.Errorf("published Source = %q, want %q", m.Source, queue.SourceRebalancer)
		}
		if m.RepositoryFull != "owner/repo" {
			t.Errorf("published RepositoryFull = %q, want %q", m.RepositoryFull, "owner/repo")
		}
		if m.InstallationID != 42 {
			t.Errorf("published InstallationID = %d, want 42", m.InstallationID)
		}
	}
}

func TestRebalance_TwoLabelSetsIndependent(t *testing.T) {
	gh := &fakeGH{jobs: []github.QueuedJob{
		{JobID: 1, Status: "queued", Labels: []string{"self-hosted", "large"}},
		{JobID: 2, Status: "queued", Labels: []string{"self-hosted", "large"}},
		{JobID: 3, Status: "queued", Labels: []string{"self-hosted", "large"}},
		{JobID: 4, Status: "queued", Labels: []string{"self-hosted", "medium"}},
		{JobID: 5, Status: "queued", Labels: []string{"self-hosted", "medium"}},
	}}
	store := memstore.New()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_ = store.Put(ctx, state.Runner{
			ID:     fmt.Sprintf("r%d", i),
			Status: state.StatusPending,
			Labels: []string{"self-hosted", "large"},
		})
	}
	pub := &fakePub{}

	if err := Rebalance(ctx, gh, store, pub, "owner/repo", 42); err != nil {
		t.Fatalf("Rebalance: %v", err)
	}
	if len(pub.published) != 3 {
		t.Errorf("expected 3 publishes (1 large + 2 medium), got %d", len(pub.published))
	}
	largeCount, mediumCount := 0, 0
	for _, m := range pub.published {
		if state.MatchesLabels(m.Labels, []string{"self-hosted", "large"}) {
			largeCount++
		}
		if state.MatchesLabels(m.Labels, []string{"self-hosted", "medium"}) {
			mediumCount++
		}
	}
	if largeCount != 1 {
		t.Errorf("large publishes = %d, want 1", largeCount)
	}
	if mediumCount != 2 {
		t.Errorf("medium publishes = %d, want 2", mediumCount)
	}
}

func TestRebalance_SubsetMatchCountsAsSupply(t *testing.T) {
	gh := &fakeGH{jobs: []github.QueuedJob{
		{JobID: 1, Status: "queued", Labels: []string{"self-hosted", "large"}},
	}}
	store := memstore.New()
	ctx := context.Background()
	_ = store.Put(ctx, state.Runner{
		ID:     "r1",
		Status: state.StatusPending,
		Labels: []string{"self-hosted", "large", "x64"},
	})
	pub := &fakePub{}

	if err := Rebalance(ctx, gh, store, pub, "owner/repo", 42); err != nil {
		t.Fatalf("Rebalance: %v", err)
	}
	if len(pub.published) != 0 {
		t.Errorf("expected 0 publishes (subset match counts as supply), got %d", len(pub.published))
	}
}

func TestRebalance_GHErrorPropagates(t *testing.T) {
	gh := &fakeGH{err: errors.New("rate-limited")}
	store := memstore.New()
	pub := &fakePub{}

	err := Rebalance(context.Background(), gh, store, pub, "owner/repo", 42)
	if err == nil {
		t.Fatal("expected error on GH failure")
	}
	if len(pub.published) != 0 {
		t.Errorf("expected 0 publishes on error, got %d", len(pub.published))
	}
}
