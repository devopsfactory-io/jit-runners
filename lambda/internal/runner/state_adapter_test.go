package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// Compile-time assertion that StateAdapter satisfies state.RunnerStore.
var _ state.RunnerStore = (*StateAdapter)(nil)

func TestStateAdapter_GetTranslatesRecordToRunner(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC).Unix()
	rec := &Record{
		RunnerID:          "owner/repo#1",
		InstanceID:        "i-abc",
		JobID:             1,
		RunID:             2,
		Repository:        "owner/repo",
		Labels:            []string{"self-hosted", "linux"},
		Status:            StatusRunning,
		CreatedAt:         now,
		UpdatedAt:         now,
		TTL:               now + 3600,
		GitHubRunnerID:    99,
		ReEnqueueAttempts: 1,
		LastAttemptAt:     now,
	}

	fake := &fakeDDBClient{}
	store := NewStore(fake, "t")
	if err := store.Put(context.Background(), rec); err != nil {
		t.Fatalf("Put: %v", err)
	}
	adapter := NewStateAdapter(store)

	got, err := adapter.Get(context.Background(), "owner/repo#1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "owner/repo#1" {
		t.Errorf("ID = %q want owner/repo#1", got.ID)
	}
	if got.Status != StatusRunning {
		t.Errorf("Status = %q want %q", got.Status, StatusRunning)
	}
	if got.GitHubRunnerID != 99 {
		t.Errorf("GitHubRunnerID = %d want 99", got.GitHubRunnerID)
	}
	if got.ReEnqueueAttempts != 1 {
		t.Errorf("ReEnqueueAttempts = %d want 1", got.ReEnqueueAttempts)
	}
	if got.LaunchedAt.Unix() != now {
		t.Errorf("LaunchedAt = %d want %d", got.LaunchedAt.Unix(), now)
	}
	if got.LastAttemptAt.Unix() != now {
		t.Errorf("LastAttemptAt = %d want %d", got.LastAttemptAt.Unix(), now)
	}
}

func TestStateAdapter_GetNotFound(t *testing.T) {
	t.Parallel()

	fake := &fakeDDBClient{}
	store := NewStore(fake, "t")
	adapter := NewStateAdapter(store)

	_, err := adapter.Get(context.Background(), "owner/repo#999")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("expected state.ErrNotFound, got %v", err)
	}
}

func TestStateAdapter_UpdateDelegates(t *testing.T) {
	t.Parallel()

	fake := &fakeDDBClient{}
	store := NewStore(fake, "t")
	adapter := NewStateAdapter(store)

	status := state.StatusCompleted
	if err := adapter.Update(context.Background(), "owner/repo#1", state.RunnerUpdate{
		Status: &status,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if fake.updateCalls != 1 {
		t.Errorf("expected 1 UpdateItem call, got %d", fake.updateCalls)
	}
}

func TestRecordToRunner_NilReturnsZero(t *testing.T) {
	t.Parallel()

	got := recordToRunner(nil)
	if got.ID != "" || got.InstanceID != "" || got.Status != "" || got.GitHubRunnerID != 0 || len(got.Labels) != 0 {
		t.Errorf("expected zero-valued Runner, got %+v", got)
	}
	if !got.LaunchedAt.IsZero() || !got.UpdatedAt.IsZero() || !got.TTL.IsZero() || !got.LastAttemptAt.IsZero() {
		t.Errorf("expected zero timestamps, got %+v", got)
	}
}

func TestRecordToRunner_ZeroTimestampsRemainZero(t *testing.T) {
	t.Parallel()

	rec := &Record{
		RunnerID: "owner/repo#1",
		// CreatedAt, UpdatedAt, TTL, LastAttemptAt all zero.
	}
	got := recordToRunner(rec)
	if !got.LaunchedAt.IsZero() {
		t.Errorf("LaunchedAt should be zero time, got %v", got.LaunchedAt)
	}
	if !got.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt should be zero time, got %v", got.UpdatedAt)
	}
	if !got.TTL.IsZero() {
		t.Errorf("TTL should be zero time, got %v", got.TTL)
	}
	if !got.LastAttemptAt.IsZero() {
		t.Errorf("LastAttemptAt should be zero time, got %v", got.LastAttemptAt)
	}
}

func TestStateAdapter_PutPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic")
		}
	}()
	adapter := NewStateAdapter(nil)
	_ = adapter.Put(context.Background(), state.Runner{})
}

func TestStateAdapter_ListPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic")
		}
	}()
	adapter := NewStateAdapter(nil)
	_, _ = adapter.List(context.Background(), state.Filter{})
}

func TestStateAdapter_DeletePanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic")
		}
	}()
	adapter := NewStateAdapter(nil)
	_ = adapter.Delete(context.Background(), "owner/repo#1")
}
