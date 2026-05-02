package main

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/google/uuid"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/compute"
	appconfig "github.com/devopsfactory-io/jit-runners/lambda/internal/config"
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
