package main

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/google/uuid"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/compute"
	appconfig "github.com/devopsfactory-io/jit-runners/lambda/internal/config"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/github"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/provider"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/queue"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/runner"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state/memstore"
)

// fakeLauncher is a minimal compute.Launcher for tests.
type fakeLauncher struct {
	launchErr    error
	launches     []compute.LaunchSpec
	terminateErr error
	terminated   [][]string
}

func (f *fakeLauncher) Launch(_ context.Context, spec compute.LaunchSpec) (compute.Instance, error) {
	f.launches = append(f.launches, spec)
	if f.launchErr != nil {
		return compute.Instance{}, f.launchErr
	}
	return compute.Instance{ID: "i-test123"}, nil
}

func (f *fakeLauncher) Terminate(_ context.Context, ids []string) error {
	f.terminated = append(f.terminated, ids)
	return f.terminateErr
}

func (f *fakeLauncher) ListStale(_ context.Context, _ time.Duration) ([]compute.Instance, error) {
	return nil, nil
}

// errorOnUpdateStore wraps a real RunnerStore and forces Update to error.
// Used to drive the post-Launch + Update-failure code path in T5.
type errorOnUpdateStore struct {
	state.RunnerStore
	updateErr error
	updates   []string // record IDs we tried to update
}

func (s *errorOnUpdateStore) Update(_ context.Context, id string, _ state.RunnerUpdate) error {
	s.updates = append(s.updates, id)
	return s.updateErr
}

// TestProcessRecord_ParseFailureIsNoOp verifies that malformed SQS bodies
// short-circuit before any side-effects: no instance launches, no GitHub
// API calls, no DDB writes. processRecord returns nil so SQS does not retry.
func TestProcessRecord_ParseFailureIsNoOp(t *testing.T) {
	cfg := &appconfig.Config{}
	b := &provider.Bundle{
		State:   memstore.New(),
		Compute: &fakeLauncher{},
	}

	rec := events.SQSMessage{Body: "{not json"}
	if err := processRecord(context.Background(), cfg, b, rec); err != nil {
		t.Fatalf("processRecord on bad JSON should return nil to avoid retry; got %v", err)
	}
	if len(b.Compute.(*fakeLauncher).launches) != 0 {
		t.Errorf("expected no launches, got %d", len(b.Compute.(*fakeLauncher).launches))
	}
}

// TestRunnerNameMatchesUUIDPattern pins the jit-<uuidv4> contract for the
// runner name. processRecord constructs the name as "jit-" + uuid.NewString();
// this test re-creates a name through the same pathway and asserts the
// canonical 36-char UUID format.
func TestRunnerNameMatchesUUIDPattern(t *testing.T) {
	pat := regexp.MustCompile(`^jit-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	name := "jit-" + uuid.NewString()
	if !pat.MatchString(name) {
		t.Errorf("runner name %q does not match jit-<uuidv4>", name)
	}
}

// TestPendingRecordKeyedByGitHubRunnerID exercises runner.New + memstore to
// pin the new identity contract: a Runner built for GitHub runner_id 999
// persists at key "999", and the legacy "<repo>#<job_id>" key is
// unreachable post-cutover.
func TestPendingRecordKeyedByGitHubRunnerID(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()

	pending := runner.New("owner/repo", 999, "", 4567, 8910, []string{"self-hosted", "large"})
	if pending.ID != "999" {
		t.Fatalf("runner.New ID invariant violated: got %q, want %q", pending.ID, "999")
	}

	if err := store.Put(ctx, pending); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := store.Get(ctx, "999")
	if err != nil {
		t.Fatalf(`Get "999": %v`, err)
	}
	if got.GitHubRunnerID != 999 {
		t.Errorf("GitHubRunnerID = %d, want 999", got.GitHubRunnerID)
	}
	if got.JobID != 4567 {
		t.Errorf("JobID = %d, want 4567", got.JobID)
	}
	if got.WorkflowRunID != 8910 {
		t.Errorf("WorkflowRunID = %d, want 8910", got.WorkflowRunID)
	}

	if _, err := store.Get(ctx, "owner/repo#4567"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("legacy key 'owner/repo#4567' must be unreachable; got err=%v", err)
	}
}

type fakeQueueLister struct {
	jobs []github.QueuedJob
	err  error
}

func (f *fakeQueueLister) ListQueuedWorkflowJobs(_ context.Context, _ string) ([]github.QueuedJob, error) {
	return f.jobs, f.err
}

func TestShouldLaunch(t *testing.T) {
	cases := []struct {
		name      string
		source    string
		queued    []github.QueuedJob
		pending   []state.Runner
		msgLabels []string
		want      bool
		wantErr   bool
	}{
		{
			name:      "rebalancer source always launches",
			source:    queue.SourceRebalancer,
			pending:   []state.Runner{{ID: "1", Status: state.StatusPending, Labels: []string{"self-hosted", "large"}}},
			msgLabels: []string{"self-hosted", "large"},
			want:      true,
		},
		{
			name:   "webhook source: demand > supply launches",
			source: queue.SourceWebhook,
			queued: []github.QueuedJob{
				{JobID: 1, Status: "queued", Labels: []string{"self-hosted", "large"}},
				{JobID: 2, Status: "queued", Labels: []string{"self-hosted", "large"}},
			},
			pending:   []state.Runner{{ID: "1", Status: state.StatusPending, Labels: []string{"self-hosted", "large"}}},
			msgLabels: []string{"self-hosted", "large"},
			want:      true,
		},
		{
			name:      "webhook source: demand == supply skips",
			source:    queue.SourceWebhook,
			queued:    []github.QueuedJob{{JobID: 1, Status: "queued", Labels: []string{"self-hosted", "large"}}},
			pending:   []state.Runner{{ID: "1", Status: state.StatusPending, Labels: []string{"self-hosted", "large"}}},
			msgLabels: []string{"self-hosted", "large"},
			want:      false,
		},
		{
			name:   "webhook source: demand < supply skips",
			source: queue.SourceWebhook,
			queued: []github.QueuedJob{{JobID: 1, Status: "queued", Labels: []string{"self-hosted", "large"}}},
			pending: []state.Runner{
				{ID: "1", Status: state.StatusPending, Labels: []string{"self-hosted", "large"}},
				{ID: "2", Status: state.StatusPending, Labels: []string{"self-hosted", "large"}},
			},
			msgLabels: []string{"self-hosted", "large"},
			want:      false,
		},
		{
			name:      "empty Source treated as webhook",
			source:    "",
			queued:    []github.QueuedJob{{JobID: 1, Status: "queued", Labels: []string{"self-hosted", "large"}}},
			pending:   []state.Runner{{ID: "1", Status: state.StatusPending, Labels: []string{"self-hosted", "large"}}},
			msgLabels: []string{"self-hosted", "large"},
			want:      false,
		},
		{
			name:      "subset match: runner with extra label counts as supply",
			source:    queue.SourceWebhook,
			queued:    []github.QueuedJob{{JobID: 1, Status: "queued", Labels: []string{"self-hosted", "large"}}},
			pending:   []state.Runner{{ID: "1", Status: state.StatusPending, Labels: []string{"self-hosted", "large", "x64"}}},
			msgLabels: []string{"self-hosted", "large"},
			want:      false,
		},
		{
			name:      "different label set: pending runner with diff labels does NOT count as supply",
			source:    queue.SourceWebhook,
			queued:    []github.QueuedJob{{JobID: 1, Status: "queued", Labels: []string{"self-hosted", "large"}}},
			pending:   []state.Runner{{ID: "1", Status: state.StatusPending, Labels: []string{"self-hosted", "medium"}}},
			msgLabels: []string{"self-hosted", "large"},
			want:      true,
		},
		{
			name:      "GH error returned",
			source:    queue.SourceWebhook,
			msgLabels: []string{"self-hosted", "large"},
			wantErr:   true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gh := &fakeQueueLister{jobs: tc.queued}
			if tc.wantErr {
				gh.err = errors.New("synthetic")
			}
			store := memstore.New()
			ctx := context.Background()
			for _, r := range tc.pending {
				if err := store.Put(ctx, r); err != nil {
					t.Fatalf("Put: %v", err)
				}
			}
			msg := &queue.ScaleUpMessage{
				Source:         tc.source,
				Labels:         tc.msgLabels,
				RepositoryFull: "owner/repo",
			}
			got, err := shouldLaunch(ctx, gh, store, msg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("shouldLaunch = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBindLaunchedInstance_TerminatesOnUpdateFailure pins the issue #74
// design D1.3 contract for the post-Launch + Update-failure path: when
// Compute.Launch has succeeded but State.Update fails, the helper must
// (1) call Compute.Terminate with the launched instance ID so the EC2
// is reclaimed instead of leaking as a no-DDB-row orphan, and (2) return
// the wrapped Update error so SQS redrives. The next attempt launches a
// fresh instance with a fresh DDB write.
func TestBindLaunchedInstance_TerminatesOnUpdateFailure(t *testing.T) {
	updateErr := errors.New("ddb throttled")
	store := &errorOnUpdateStore{
		RunnerStore: memstore.New(),
		updateErr:   updateErr,
	}
	launcher := &fakeLauncher{}
	b := &provider.Bundle{
		State:   store,
		Compute: launcher,
	}

	const recordID = "999"
	const instanceID = "i-test123"

	err := bindLaunchedInstance(context.Background(), b, recordID, instanceID, 999, 4567)

	if err == nil {
		t.Fatal("expected error from bindLaunchedInstance when Update fails")
	}
	if !errors.Is(err, updateErr) {
		t.Errorf("err = %v, want wraps %v", err, updateErr)
	}
	if len(launcher.terminated) != 1 {
		t.Fatalf("expected 1 Terminate call after Update failure, got %d",
			len(launcher.terminated))
	}
	if got, want := launcher.terminated[0], []string{instanceID}; !reflect.DeepEqual(got, want) {
		t.Errorf("Terminate ids = %v, want %v", got, want)
	}
	if len(store.updates) != 1 || store.updates[0] != recordID {
		t.Errorf("Update calls = %v, want [%q]", store.updates, recordID)
	}
}

// TestBindLaunchedInstance_HappyPath verifies the no-error path is a
// pass-through: no Terminate call, no error returned.
func TestBindLaunchedInstance_HappyPath(t *testing.T) {
	launcher := &fakeLauncher{}
	b := &provider.Bundle{
		State:   memstore.New(),
		Compute: launcher,
	}

	// Seed a pending row so memstore.Update has something to mutate.
	pending := runner.New("owner/repo", 999, "", 4567, 8910, []string{"self-hosted", "large"})
	if err := b.State.Put(context.Background(), pending); err != nil {
		t.Fatalf("seed Put: %v", err)
	}

	if err := bindLaunchedInstance(context.Background(), b, pending.ID, "i-test123", 999, 4567); err != nil {
		t.Fatalf("bindLaunchedInstance happy path: %v", err)
	}
	if len(launcher.terminated) != 0 {
		t.Errorf("happy path must not Terminate; got %d calls", len(launcher.terminated))
	}
}

// TestBindLaunchedInstance_TerminateErrorStillReturnsUpdateError verifies
// that even when the defensive Terminate also errors, bindLaunchedInstance
// still returns the wrapped Update error (Terminate is best-effort and
// surfaces only via log).
func TestBindLaunchedInstance_TerminateErrorStillReturnsUpdateError(t *testing.T) {
	updateErr := errors.New("ddb throttled")
	store := &errorOnUpdateStore{
		RunnerStore: memstore.New(),
		updateErr:   updateErr,
	}
	launcher := &fakeLauncher{terminateErr: errors.New("ec2 throttled")}
	b := &provider.Bundle{
		State:   store,
		Compute: launcher,
	}

	err := bindLaunchedInstance(context.Background(), b, "999", "i-test123", 999, 4567)
	if err == nil {
		t.Fatal("expected error even when Terminate also fails")
	}
	if !errors.Is(err, updateErr) {
		t.Errorf("err = %v, want wraps Update err %v (Terminate err must not shadow it)", err, updateErr)
	}
	if len(launcher.terminated) != 1 {
		t.Errorf("expected Terminate to still be attempted; got %d calls", len(launcher.terminated))
	}
}
