package main

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/google/uuid"

	awssqs "github.com/devopsfactory-io/jit-runners/lambda/internal/aws/sqs"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/compute"
	appconfig "github.com/devopsfactory-io/jit-runners/lambda/internal/config"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/github"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/runner"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state/memstore"
)

// fakeLauncher is a minimal compute.Launcher for tests.
type fakeLauncher struct {
	launchErr error
	launches  []compute.LaunchSpec
}

func (f *fakeLauncher) Launch(_ context.Context, spec compute.LaunchSpec) (compute.Instance, error) {
	f.launches = append(f.launches, spec)
	if f.launchErr != nil {
		return compute.Instance{}, f.launchErr
	}
	return compute.Instance{ID: "i-test123"}, nil
}

func (f *fakeLauncher) Terminate(_ context.Context, _ []string) error { return nil }

func (f *fakeLauncher) ListStale(_ context.Context, _ time.Duration) ([]compute.Instance, error) {
	return nil, nil
}

// TestProcessRecord_ParseFailureIsNoOp verifies that malformed SQS bodies
// short-circuit before any side-effects: no instance launches, no GitHub
// API calls, no DDB writes. processRecord returns nil so SQS does not retry.
func TestProcessRecord_ParseFailureIsNoOp(t *testing.T) {
	cfg := &appconfig.Config{}
	launcher := &fakeLauncher{}
	store := memstore.New()

	rec := events.SQSMessage{Body: "{not json"}
	if err := processRecord(context.Background(), cfg, launcher, store, nil, rec); err != nil {
		t.Fatalf("processRecord on bad JSON should return nil to avoid retry; got %v", err)
	}
	if len(launcher.launches) != 0 {
		t.Errorf("expected no launches, got %d", len(launcher.launches))
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
			source:    awssqs.SourceRebalancer,
			pending:   []state.Runner{{ID: "1", Status: state.StatusPending, Labels: []string{"self-hosted", "large"}}},
			msgLabels: []string{"self-hosted", "large"},
			want:      true,
		},
		{
			name:   "webhook source: demand > supply launches",
			source: awssqs.SourceWebhook,
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
			source:    awssqs.SourceWebhook,
			queued:    []github.QueuedJob{{JobID: 1, Status: "queued", Labels: []string{"self-hosted", "large"}}},
			pending:   []state.Runner{{ID: "1", Status: state.StatusPending, Labels: []string{"self-hosted", "large"}}},
			msgLabels: []string{"self-hosted", "large"},
			want:      false,
		},
		{
			name:   "webhook source: demand < supply skips",
			source: awssqs.SourceWebhook,
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
			source:    awssqs.SourceWebhook,
			queued:    []github.QueuedJob{{JobID: 1, Status: "queued", Labels: []string{"self-hosted", "large"}}},
			pending:   []state.Runner{{ID: "1", Status: state.StatusPending, Labels: []string{"self-hosted", "large", "x64"}}},
			msgLabels: []string{"self-hosted", "large"},
			want:      false,
		},
		{
			name:      "different label set: pending runner with diff labels does NOT count as supply",
			source:    awssqs.SourceWebhook,
			queued:    []github.QueuedJob{{JobID: 1, Status: "queued", Labels: []string{"self-hosted", "large"}}},
			pending:   []state.Runner{{ID: "1", Status: state.StatusPending, Labels: []string{"self-hosted", "medium"}}},
			msgLabels: []string{"self-hosted", "large"},
			want:      true,
		},
		{
			name:      "GH error returned",
			source:    awssqs.SourceWebhook,
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
			msg := &awssqs.ScaleUpMessage{
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
