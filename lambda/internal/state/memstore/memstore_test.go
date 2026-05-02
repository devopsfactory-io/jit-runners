package memstore

import (
	"context"
	"errors"
	"testing"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

func TestPutGetUpdateDelete(t *testing.T) {
	s := New()
	ctx := context.Background()

	if _, err := s.Get(ctx, "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("Get missing: err=%v, want ErrNotFound", err)
	}

	r := state.Runner{ID: "1", Status: state.StatusPending, GitHubRunnerID: 1}
	if err := s.Put(ctx, r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, "1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != state.StatusPending {
		t.Errorf("Status = %q, want %q", got.Status, state.StatusPending)
	}

	next := state.StatusRunning
	if err := s.Update(ctx, "1", state.RunnerUpdate{Status: &next}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = s.Get(ctx, "1")
	if got.Status != state.StatusRunning {
		t.Errorf("Status after Update = %q, want %q", got.Status, state.StatusRunning)
	}

	if err := s.Delete(ctx, "1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

func TestList_FilterByStatus(t *testing.T) {
	s := New()
	ctx := context.Background()
	_ = s.Put(ctx, state.Runner{ID: "1", Status: state.StatusPending})
	_ = s.Put(ctx, state.Runner{ID: "2", Status: state.StatusRunning})

	pending, err := s.List(ctx, state.Filter{StatusEq: state.StatusPending})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "1" {
		t.Errorf("List(pending) = %+v, want one record id=1", pending)
	}
}
