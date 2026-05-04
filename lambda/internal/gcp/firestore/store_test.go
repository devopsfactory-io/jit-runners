package firestore

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// fakeFirestore is an in-memory implementation of firestoreAPI for tests.
type fakeFirestore struct {
	// docs holds collection → docID → docData.
	docs map[string]map[string]map[string]any

	// recordedFilters holds the filters passed to the last Query call.
	recordedFilters []QueryFilter

	// injectable errors per operation.
	setErr    error
	getErr    error
	updateErr error
	deleteErr error
	queryErr  error
}

func newFakeFirestore() *fakeFirestore {
	return &fakeFirestore{
		docs: make(map[string]map[string]map[string]any),
	}
}

func (f *fakeFirestore) Set(_ context.Context, coll, id string, data map[string]any) error {
	if f.setErr != nil {
		return f.setErr
	}
	if f.docs[coll] == nil {
		f.docs[coll] = make(map[string]map[string]any)
	}
	f.docs[coll][id] = data
	return nil
}

func (f *fakeFirestore) Get(_ context.Context, coll, id string) (map[string]any, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if c, ok := f.docs[coll]; ok {
		if doc, ok := c[id]; ok {
			return doc, nil
		}
	}
	return nil, state.ErrNotFound
}

func (f *fakeFirestore) Update(_ context.Context, coll, id string, data map[string]any) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	if c, ok := f.docs[coll]; ok {
		if doc, ok := c[id]; ok {
			for k, v := range data {
				doc[k] = v
			}
			return nil
		}
	}
	return state.ErrNotFound
}

func (f *fakeFirestore) Delete(_ context.Context, coll, id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if c, ok := f.docs[coll]; ok {
		delete(c, id)
	}
	return nil
}

func (f *fakeFirestore) Query(_ context.Context, coll string, filters []QueryFilter) ([]map[string]any, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	f.recordedFilters = filters

	var results []map[string]any
	c := f.docs[coll]
	for _, doc := range c {
		if matchesFilters(doc, filters) {
			cp := make(map[string]any, len(doc))
			for k, v := range doc {
				cp[k] = v
			}
			results = append(results, cp)
		}
	}
	return results, nil
}

// matchesFilters evaluates the filter slice against a single document.
// Supports "==" and ">=" operators on comparable values.
func matchesFilters(doc map[string]any, filters []QueryFilter) bool {
	for _, f := range filters {
		v, ok := doc[f.Field]
		if !ok {
			return false
		}
		switch f.Op {
		case "==":
			if v != f.Value {
				return false
			}
		case ">=":
			// Support time.Time comparison (used by ListActiveRepos).
			if ft, ok := f.Value.(time.Time); ok {
				if dt, ok := v.(time.Time); ok {
					if dt.Before(ft) {
						return false
					}
				} else {
					return false
				}
			}
		}
	}
	return true
}

// helpers
func strPtr(s string) *string { return &s }

// TestStore_Put_PersistsRunner verifies Put writes all fields with snake_case keys.
func TestStore_Put_PersistsRunner(t *testing.T) {
	fake := newFakeFirestore()
	s := newStoreWithAPI(fake, "runners")

	now := time.Now().Truncate(time.Second).UTC()
	r := state.Runner{
		ID:             "42",
		Repository:     "owner/repo",
		Status:         "pending",
		InstanceID:     "i-001",
		Labels:         []string{"self-hosted", "linux"},
		LaunchedAt:     now,
		UpdatedAt:      now,
		TTL:            now.Add(24 * time.Hour),
		GitHubRunnerID: 99,
		JobID:          7,
		WorkflowRunID:  8,
	}

	if err := s.Put(context.Background(), r); err != nil {
		t.Fatalf("Put: %v", err)
	}

	doc, ok := fake.docs["runners"]["42"]
	if !ok {
		t.Fatal("expected doc to be stored under key '42'")
	}

	for _, key := range []string{
		"runner_id", "repository", "status", "instance_id",
		"labels", "launched_at", "created_at", "updated_at", "ttl",
		"gh_runner_id", "job_id", "workflow_run_id",
	} {
		if _, exists := doc[key]; !exists {
			t.Errorf("missing expected key %q in stored doc", key)
		}
	}

	if doc["runner_id"] != "42" {
		t.Errorf("runner_id = %v, want 42", doc["runner_id"])
	}
	if doc["repository"] != "owner/repo" {
		t.Errorf("repository = %v, want owner/repo", doc["repository"])
	}
	if doc["status"] != "pending" {
		t.Errorf("status = %v, want pending", doc["status"])
	}
}

// TestStore_Get_ReturnsRunner verifies round-trip fidelity for all fields.
func TestStore_Get_ReturnsRunner(t *testing.T) {
	fake := newFakeFirestore()
	s := newStoreWithAPI(fake, "runners")

	now := time.Now().Truncate(time.Second).UTC()
	r := state.Runner{
		ID:             "42",
		Repository:     "owner/repo",
		Status:         "pending",
		InstanceID:     "i-001",
		Labels:         []string{"self-hosted", "linux"},
		LaunchedAt:     now,
		UpdatedAt:      now,
		TTL:            now.Add(24 * time.Hour),
		GitHubRunnerID: 99,
		JobID:          7,
		WorkflowRunID:  8,
	}
	if err := s.Put(context.Background(), r); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(context.Background(), "42")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ID != r.ID {
		t.Errorf("ID = %q, want %q", got.ID, r.ID)
	}
	if got.Repository != r.Repository {
		t.Errorf("Repository = %q, want %q", got.Repository, r.Repository)
	}
	if got.Status != r.Status {
		t.Errorf("Status = %q, want %q", got.Status, r.Status)
	}
	if got.InstanceID != r.InstanceID {
		t.Errorf("InstanceID = %q, want %q", got.InstanceID, r.InstanceID)
	}
	if got.GitHubRunnerID != r.GitHubRunnerID {
		t.Errorf("GitHubRunnerID = %d, want %d", got.GitHubRunnerID, r.GitHubRunnerID)
	}
	if got.JobID != r.JobID {
		t.Errorf("JobID = %d, want %d", got.JobID, r.JobID)
	}
	if got.WorkflowRunID != r.WorkflowRunID {
		t.Errorf("WorkflowRunID = %d, want %d", got.WorkflowRunID, r.WorkflowRunID)
	}
}

// TestStore_Get_ReturnsErrNotFound verifies missing doc returns state.ErrNotFound.
func TestStore_Get_ReturnsErrNotFound(t *testing.T) {
	fake := newFakeFirestore()
	s := newStoreWithAPI(fake, "runners")

	_, err := s.Get(context.Background(), "nonexistent")
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected state.ErrNotFound, got %v", err)
	}
}

// TestStore_List_AppliesStatusFilter verifies List uses a "==" filter on status.
func TestStore_List_AppliesStatusFilter(t *testing.T) {
	fake := newFakeFirestore()
	s := newStoreWithAPI(fake, "runners")

	ctx := context.Background()
	now := time.Now().Truncate(time.Second).UTC()

	runners := []state.Runner{
		{ID: "1", Repository: "o/r", Status: "pending", LaunchedAt: now, UpdatedAt: now, TTL: now.Add(time.Hour)},
		{ID: "2", Repository: "o/r", Status: "running", LaunchedAt: now, UpdatedAt: now, TTL: now.Add(time.Hour)},
		{ID: "3", Repository: "o/r", Status: "completed", LaunchedAt: now, UpdatedAt: now, TTL: now.Add(time.Hour)},
	}
	for _, r := range runners {
		if err := s.Put(ctx, r); err != nil {
			t.Fatalf("Put %s: %v", r.ID, err)
		}
	}

	got, err := s.List(ctx, state.Filter{StatusEq: "pending"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// Verify status filter was sent.
	found := false
	for _, f := range fake.recordedFilters {
		if f.Field == "status" && f.Op == "==" && f.Value == "pending" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected status=pending filter to be applied; got filters: %v", fake.recordedFilters)
	}

	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Status != "pending" {
		t.Errorf("got status %q, want pending", got[0].Status)
	}
}

// TestStore_ListActiveRepos_DedupesByRepository verifies deduplication.
func TestStore_ListActiveRepos_DedupesByRepository(t *testing.T) {
	fake := newFakeFirestore()
	s := newStoreWithAPI(fake, "runners")

	ctx := context.Background()
	past := time.Now().Add(-time.Hour).Truncate(time.Second).UTC()
	now := time.Now().Truncate(time.Second).UTC()

	runners := []state.Runner{
		{ID: "1", Repository: "o/a", Status: "running", LaunchedAt: now, UpdatedAt: now, TTL: now.Add(time.Hour)},
		{ID: "2", Repository: "o/a", Status: "running", LaunchedAt: now, UpdatedAt: now, TTL: now.Add(time.Hour)},
		{ID: "3", Repository: "o/b", Status: "running", LaunchedAt: now, UpdatedAt: now, TTL: now.Add(time.Hour)},
	}
	for _, r := range runners {
		if err := s.Put(ctx, r); err != nil {
			t.Fatalf("Put %s: %v", r.ID, err)
		}
	}

	got, err := s.ListActiveRepos(ctx, past)
	if err != nil {
		t.Fatalf("ListActiveRepos: %v", err)
	}

	sort.Strings(got)
	want := []string{"o/a", "o/b"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestStore_ListActiveRepos_AppliesSinceFilter verifies the since time filter.
func TestStore_ListActiveRepos_AppliesSinceFilter(t *testing.T) {
	fake := newFakeFirestore()
	s := newStoreWithAPI(fake, "runners")

	ctx := context.Background()
	now := time.Now().Truncate(time.Second).UTC()

	// one launched 8 days ago (should be excluded), one launched 1 day ago (included).
	old := now.Add(-8 * 24 * time.Hour)
	recent := now.Add(-1 * 24 * time.Hour)

	runners := []state.Runner{
		{ID: "old", Repository: "o/old", Status: "running", LaunchedAt: old, UpdatedAt: old, TTL: old.Add(time.Hour)},
		{ID: "new", Repository: "o/new", Status: "running", LaunchedAt: recent, UpdatedAt: recent, TTL: recent.Add(time.Hour)},
	}
	for _, r := range runners {
		if err := s.Put(ctx, r); err != nil {
			t.Fatalf("Put %s: %v", r.ID, err)
		}
	}

	since := now.Add(-7 * 24 * time.Hour)
	got, err := s.ListActiveRepos(ctx, since)
	if err != nil {
		t.Fatalf("ListActiveRepos: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 repo, got %d: %v", len(got), got)
	}
	if got[0] != "o/new" {
		t.Errorf("got %q, want o/new", got[0])
	}
}

// TestStore_Update_PartialFields verifies Update only touches specified fields.
func TestStore_Update_PartialFields(t *testing.T) {
	fake := newFakeFirestore()
	s := newStoreWithAPI(fake, "runners")

	ctx := context.Background()
	now := time.Now().Truncate(time.Second).UTC()

	r := state.Runner{
		ID:         "42",
		Repository: "owner/repo",
		Status:     "pending",
		InstanceID: "i-old",
		LaunchedAt: now,
		UpdatedAt:  now,
		TTL:        now.Add(time.Hour),
	}
	if err := s.Put(ctx, r); err != nil {
		t.Fatalf("Put: %v", err)
	}

	u := state.RunnerUpdate{
		Status:     strPtr("running"),
		InstanceID: strPtr("i-abc"),
	}
	if err := s.Update(ctx, "42", u); err != nil {
		t.Fatalf("Update: %v", err)
	}

	doc := fake.docs["runners"]["42"]
	if doc["status"] != "running" {
		t.Errorf("status = %v, want running", doc["status"])
	}
	if doc["instance_id"] != "i-abc" {
		t.Errorf("instance_id = %v, want i-abc", doc["instance_id"])
	}
	if _, ok := doc["updated_at"]; !ok {
		t.Error("expected updated_at to be set")
	}

	// Ensure fields not in the update were NOT changed.
	if doc["repository"] != "owner/repo" {
		t.Errorf("repository should be unchanged, got %v", doc["repository"])
	}
}

func TestGetByInstanceID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("found", func(t *testing.T) {
		fake := newFakeFirestore()
		// Seed two docs in the runners collection.
		_ = fake.Set(ctx, "runners", "r1", map[string]any{
			"runner_id":   "r1",
			"instance_id": "i-aaa",
			"status":      "pending",
		})
		_ = fake.Set(ctx, "runners", "r2", map[string]any{
			"runner_id":   "r2",
			"instance_id": "i-bbb",
			"status":      "running",
		})
		s := &Store{api: fake, collName: "runners"}

		got, err := s.GetByInstanceID(ctx, "i-bbb")
		if err != nil {
			t.Fatalf("GetByInstanceID: %v", err)
		}
		if got.ID != "r2" {
			t.Errorf("ID = %q, want r2", got.ID)
		}
		// Confirm the right filter was passed.
		if len(fake.recordedFilters) != 1 {
			t.Fatalf("expected 1 filter, got %d", len(fake.recordedFilters))
		}
		f := fake.recordedFilters[0]
		if f.Field != "instance_id" || f.Op != "==" || f.Value != "i-bbb" {
			t.Errorf("filter = %+v, want {instance_id == i-bbb}", f)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		fake := newFakeFirestore()
		s := &Store{api: fake, collName: "runners"}
		_, err := s.GetByInstanceID(ctx, "i-zzz")
		if !errors.Is(err, state.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("empty_id_returns_not_found_without_call", func(t *testing.T) {
		fake := newFakeFirestore()
		s := &Store{api: fake, collName: "runners"}
		_, err := s.GetByInstanceID(ctx, "")
		if !errors.Is(err, state.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
		if fake.recordedFilters != nil {
			t.Errorf("expected no Query call for empty instance_id; recordedFilters = %+v", fake.recordedFilters)
		}
	})
}

// TestStore_Delete_RemovesDoc verifies Delete removes the document.
func TestStore_Delete_RemovesDoc(t *testing.T) {
	fake := newFakeFirestore()
	s := newStoreWithAPI(fake, "runners")

	ctx := context.Background()
	now := time.Now().Truncate(time.Second).UTC()

	r := state.Runner{
		ID:         "42",
		Repository: "owner/repo",
		Status:     "pending",
		LaunchedAt: now,
		UpdatedAt:  now,
		TTL:        now.Add(time.Hour),
	}
	if err := s.Put(ctx, r); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if len(fake.docs["runners"]) != 1 {
		t.Fatalf("expected 1 doc before delete, got %d", len(fake.docs["runners"]))
	}

	if err := s.Delete(ctx, "42"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if len(fake.docs["runners"]) != 0 {
		t.Errorf("expected 0 docs after delete, got %d", len(fake.docs["runners"]))
	}
}
