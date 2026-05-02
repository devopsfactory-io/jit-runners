package main

import (
	"context"
	"log"
	"os"
	"testing"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/lifecycle"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state/memstore"
)

// fakeGitHub records DeregisterRunner calls; used to assert the lifecycle
// handler hits the deregister path on completed.
type fakeGitHub struct {
	calls []deregCall
	err   error
}

type deregCall struct {
	repo     string
	runnerID int64
}

func (f *fakeGitHub) DeregisterRunner(_ context.Context, repo string, runnerID int64) error {
	f.calls = append(f.calls, deregCall{repo: repo, runnerID: runnerID})
	return f.err
}

func newHandler(store state.RunnerStore, gh *fakeGitHub) *lifecycle.Handler {
	return lifecycle.New(store, gh, log.New(os.Stderr, "test ", 0))
}

func TestLifecycle_InProgressThenCompleted_FollowsRunnerID(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()

	// Seed at key "999" — the stringified GitHub runner_id.
	seed := state.Runner{
		ID:             "999",
		Status:         state.StatusPending,
		GitHubRunnerID: 999,
		Repository:     "owner/repo",
		JobID:          4567,
		WorkflowRunID:  8910,
	}
	if err := store.Put(ctx, seed); err != nil {
		t.Fatalf("seed Put: %v", err)
	}

	gh := &fakeGitHub{}
	h := newHandler(store, gh)

	// in_progress -> running, no deregister yet.
	body := []byte(`{"job_id":4567,"repo":"owner/repo","runner_id":999,"action":"in_progress"}`)
	if err := h.HandleSQS(ctx, body); err != nil {
		t.Fatalf("HandleSQS in_progress: %v", err)
	}
	got, _ := store.Get(ctx, "999")
	if got.Status != state.StatusRunning {
		t.Errorf("after in_progress: Status = %q, want %q", got.Status, state.StatusRunning)
	}
	if len(gh.calls) != 0 {
		t.Errorf("in_progress should not deregister: calls=%+v", gh.calls)
	}

	// completed -> completed + deregister(999, owner/repo).
	body = []byte(`{"job_id":4567,"repo":"owner/repo","runner_id":999,"action":"completed"}`)
	if err := h.HandleSQS(ctx, body); err != nil {
		t.Fatalf("HandleSQS completed: %v", err)
	}
	got, _ = store.Get(ctx, "999")
	if got.Status != state.StatusCompleted {
		t.Errorf("after completed: Status = %q, want %q", got.Status, state.StatusCompleted)
	}
	if len(gh.calls) != 1 || gh.calls[0].runnerID != 999 || gh.calls[0].repo != "owner/repo" {
		t.Errorf("deregister calls = %+v, want one with runnerID=999 repo=owner/repo", gh.calls)
	}
}

func TestLifecycle_DropsWhenRunnerIDZero(t *testing.T) {
	store := memstore.New()
	gh := &fakeGitHub{}
	h := newHandler(store, gh)

	body := []byte(`{"job_id":4567,"repo":"owner/repo","runner_id":0,"action":"completed"}`)
	if err := h.HandleSQS(context.Background(), body); err != nil {
		t.Fatalf("HandleSQS: %v", err)
	}
	if len(gh.calls) != 0 {
		t.Errorf("zero runner_id must drop without deregister; calls=%+v", gh.calls)
	}
}

func TestLifecycle_DropsWhenRecordAbsent(t *testing.T) {
	store := memstore.New()
	gh := &fakeGitHub{}
	h := newHandler(store, gh)

	body := []byte(`{"job_id":4567,"repo":"owner/repo","runner_id":12345,"action":"completed"}`)
	if err := h.HandleSQS(context.Background(), body); err != nil {
		t.Fatalf("HandleSQS: %v", err)
	}
	if len(gh.calls) != 0 {
		t.Errorf("absent record must drop without deregister; calls=%+v", gh.calls)
	}
}
