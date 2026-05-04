package rebalancer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
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

func (f *fakePub) Publish(_ context.Context, m queue.Msg) error {
	if f.err != nil {
		return f.err
	}
	var msg queue.ScaleUpMessage
	if err := json.Unmarshal(m.Body, &msg); err != nil {
		return err
	}
	f.published = append(f.published, &msg)
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

// TestRebalance_CycleCompleteLogShape locks the operator-facing log line
// shape that troubleshooting.md and release.md reference by name. A future
// refactor that drops `published=` or renames `label_sets` would silently
// break dashboards built against this contract; this test guards it.
//
// Per spec D20 (Phase F): the format string is
//
//	rebalancer: cycle complete repo=<repo> demand=<n> supply=<n> published=<n> label_sets=<n>
func TestRebalance_CycleCompleteLogShape(t *testing.T) {
	cases := []struct {
		name             string
		jobs             []github.QueuedJob
		seedPending      []state.Runner
		expectSubstrings []string
	}{
		{
			name: "no_gap",
			jobs: nil,
			expectSubstrings: []string{
				"rebalancer: cycle complete",
				"repo=owner/repo",
				"demand=0",
				"supply=0",
				"published=0",
				"label_sets=0",
			},
		},
		{
			name: "demand_exceeds_supply",
			jobs: []github.QueuedJob{
				{JobID: 1, Status: "queued", Labels: []string{"self-hosted", "large"}},
				{JobID: 2, Status: "queued", Labels: []string{"self-hosted", "large"}},
				{JobID: 3, Status: "queued", Labels: []string{"self-hosted", "large"}},
			},
			seedPending: []state.Runner{
				{ID: "r0", Status: state.StatusPending, Labels: []string{"self-hosted", "large"}},
			},
			expectSubstrings: []string{
				"rebalancer: cycle complete",
				"repo=owner/repo",
				"demand=3",
				"supply=1",
				"published=2",
				"label_sets=1",
			},
		},
		{
			name: "two_label_sets",
			jobs: []github.QueuedJob{
				{JobID: 1, Status: "queued", Labels: []string{"self-hosted", "large"}},
				{JobID: 2, Status: "queued", Labels: []string{"self-hosted", "medium"}},
			},
			seedPending: nil,
			expectSubstrings: []string{
				"rebalancer: cycle complete",
				"repo=owner/repo",
				"demand=2",
				"supply=0",
				"published=2",
				"label_sets=2",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Capture stdlib log output. rebalancer.go uses log.Printf
			// (not slog), so we redirect the default logger's output
			// to a buffer for the duration of one Rebalance() call.
			var buf bytes.Buffer
			origOut := log.Writer()
			origFlags := log.Flags()
			log.SetOutput(&buf)
			log.SetFlags(0) // strip date/time prefix; assert pure message
			defer func() {
				log.SetOutput(origOut)
				log.SetFlags(origFlags)
			}()

			gh := &fakeGH{jobs: tc.jobs}
			store := memstore.New()
			ctx := context.Background()
			for _, r := range tc.seedPending {
				if err := store.Put(ctx, r); err != nil {
					t.Fatalf("Put(%s): %v", r.ID, err)
				}
			}
			pub := &fakePub{}

			if err := Rebalance(ctx, gh, store, pub, "owner/repo", 42); err != nil {
				t.Fatalf("Rebalance: %v", err)
			}

			got := buf.String()
			for _, want := range tc.expectSubstrings {
				if !strings.Contains(got, want) {
					t.Errorf("log output missing %q\nfull output: %q", want, got)
				}
			}
		})
	}
}
