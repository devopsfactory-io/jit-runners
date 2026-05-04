// Package memstore is an in-memory state.RunnerStore for tests. It is
// not a build-tagged package; production code never imports it. Keeping
// it as a regular Go package makes it usable from any *_test.go in the
// module while incurring zero binary footprint in deployed Lambdas.
package memstore

import (
	"context"
	"sync"
	"time"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// Store is a thread-safe in-memory state.RunnerStore implementation.
type Store struct {
	mu      sync.Mutex
	records map[string]state.Runner
}

// Compile-time assertion that Store satisfies state.RunnerStore.
var _ state.RunnerStore = (*Store)(nil)

// New constructs an empty Store.
func New() *Store {
	return &Store{records: map[string]state.Runner{}}
}

// Put writes a runner record. UpdatedAt is bumped to now to match the
// dynamo implementation's behavior.
func (s *Store) Put(_ context.Context, r state.Runner) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.ID == "" {
		return state.ErrNotFound // arbitrary non-nil; tests should not hit this
	}
	if r.UpdatedAt.IsZero() || r.UpdatedAt.Before(time.Now()) {
		r.UpdatedAt = time.Now().UTC()
	}
	s.records[r.ID] = r
	return nil
}

// Get returns the record at id, or state.ErrNotFound.
func (s *Store) Get(_ context.Context, id string) (state.Runner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return state.Runner{}, state.ErrNotFound
	}
	return r, nil
}

// GetByInstanceID returns the runner whose InstanceID matches. Used by the
// scaledown orphan sweep to cross-reference cloud-side instances against
// store records.
func (s *Store) GetByInstanceID(_ context.Context, instanceID string) (state.Runner, error) {
	if instanceID == "" {
		return state.Runner{}, state.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.records {
		if r.InstanceID == instanceID {
			return r, nil
		}
	}
	return state.Runner{}, state.ErrNotFound
}

// List returns all records optionally filtered by StatusEq.
func (s *Store) List(_ context.Context, f state.Filter) ([]state.Runner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []state.Runner
	for _, r := range s.records {
		if f.StatusEq != "" && r.Status != f.StatusEq {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// Update applies a partial update to the record at id.
func (s *Store) Update(_ context.Context, id string, u state.RunnerUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return state.ErrNotFound
	}
	if u.Status != nil {
		r.Status = *u.Status
	}
	if u.InstanceID != nil {
		r.InstanceID = *u.InstanceID
	}
	if u.GitHubRunnerID != nil {
		r.GitHubRunnerID = *u.GitHubRunnerID
	}
	if u.ReEnqueueAttempts != nil {
		r.ReEnqueueAttempts = *u.ReEnqueueAttempts
	}
	if u.LastAttemptAt != nil {
		r.LastAttemptAt = *u.LastAttemptAt
	}
	r.UpdatedAt = time.Now().UTC()
	s.records[id] = r
	return nil
}

// Delete removes the record at id. Missing keys are not an error.
func (s *Store) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, id)
	return nil
}

// ListActiveRepos returns the deduped Repository values across runner records
// launched at or after since. Records with an empty Repository are skipped.
// Results are not sorted; callers that need a stable order must sort themselves.
func (s *Store) ListActiveRepos(_ context.Context, since time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[string]struct{})
	var repos []string
	for _, r := range s.records {
		if r.LaunchedAt.Before(since) {
			continue
		}
		if r.Repository == "" {
			continue
		}
		if _, dup := seen[r.Repository]; dup {
			continue
		}
		seen[r.Repository] = struct{}{}
		repos = append(repos, r.Repository)
	}
	return repos, nil
}
