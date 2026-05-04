package memstore

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

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

func TestListActiveRepos(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("empty store returns empty slice", func(t *testing.T) {
		s := New()
		got, err := s.ListActiveRepos(ctx, now.Add(-time.Hour))
		if err != nil {
			t.Fatalf("ListActiveRepos: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 repos, got %d: %v", len(got), got)
		}
	})

	t.Run("single repo with one record", func(t *testing.T) {
		s := New()
		_ = s.Put(ctx, state.Runner{ID: "r1", Repository: "o/a", LaunchedAt: now})
		got, err := s.ListActiveRepos(ctx, now.Add(-time.Hour))
		if err != nil {
			t.Fatalf("ListActiveRepos: %v", err)
		}
		if len(got) != 1 || got[0] != "o/a" {
			t.Errorf("got %v, want [o/a]", got)
		}
	})

	t.Run("multiple repos deduped", func(t *testing.T) {
		s := New()
		_ = s.Put(ctx, state.Runner{ID: "r1", Repository: "o/a", LaunchedAt: now})
		_ = s.Put(ctx, state.Runner{ID: "r2", Repository: "o/a", LaunchedAt: now})
		_ = s.Put(ctx, state.Runner{ID: "r3", Repository: "o/b", LaunchedAt: now})
		got, err := s.ListActiveRepos(ctx, now.Add(-time.Hour))
		if err != nil {
			t.Fatalf("ListActiveRepos: %v", err)
		}
		sort.Strings(got)
		want := []string{"o/a", "o/b"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("since filter excludes older records", func(t *testing.T) {
		s := New()
		t1 := now.Add(-2 * time.Hour)
		t2 := now.Add(-1 * time.Hour)
		_ = s.Put(ctx, state.Runner{ID: "old", Repository: "o/old", LaunchedAt: t1})
		_ = s.Put(ctx, state.Runner{ID: "new", Repository: "o/new", LaunchedAt: t2})
		// since = t2 - 1ns: only the record at t2 should be included
		since := t2.Add(-1 * time.Nanosecond)
		got, err := s.ListActiveRepos(ctx, since)
		if err != nil {
			t.Fatalf("ListActiveRepos: %v", err)
		}
		if len(got) != 1 || got[0] != "o/new" {
			t.Errorf("got %v, want [o/new]", got)
		}
	})
}
