package runner

import (
	"context"
	"time"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// StateAdapter wraps *Store to satisfy a subset of state.RunnerStore for the
// lifecycle handler. Only Get and Update are implemented; Put/List/Delete
// panic. This is a temporary bridge until the dynamo Store is refactored to
// operate on state.Runner directly (planned for #45's cloud-abstraction work).
//
// The adapter exists because the lifecycle handler is written against the
// cloud-agnostic state.RunnerStore contract, while the rest of the codebase
// (scaleup, scaledown) consumes *Store directly. Mirroring scaleup's
// construction in cmd/lifecycle/main.go would otherwise require two
// independent DDB layers; instead we reuse *Store and translate at the edge.
type StateAdapter struct {
	Inner *Store
}

// NewStateAdapter builds an adapter that delegates to the given Store.
func NewStateAdapter(inner *Store) *StateAdapter {
	return &StateAdapter{Inner: inner}
}

// Get fetches a record by composite ID and converts the DDB *Record into a
// state.Runner. Returns state.ErrNotFound when no record exists (inherited
// from *Store.GetByID).
func (a *StateAdapter) Get(ctx context.Context, id string) (state.Runner, error) {
	rec, err := a.Inner.GetByID(ctx, id)
	if err != nil {
		return state.Runner{}, err
	}
	return recordToRunner(rec), nil
}

// Update delegates to *Store.Update without translation: state.RunnerUpdate
// is the canonical update payload that *Store already speaks.
func (a *StateAdapter) Update(ctx context.Context, id string, u state.RunnerUpdate) error {
	return a.Inner.Update(ctx, id, u)
}

// Put is intentionally unimplemented. The lifecycle handler never writes new
// records; scaleup is the sole writer, and it uses *Store.Put directly with
// the *Record shape. Calling this is a programmer error.
func (a *StateAdapter) Put(_ context.Context, _ state.Runner) error {
	panic("runner.StateAdapter.Put: not implemented — use *Store.Put with *Record")
}

// List is intentionally unimplemented. The lifecycle handler queries by
// composite ID via Get; scaledown's sweep uses *Store.ListByStatus directly.
func (a *StateAdapter) List(_ context.Context, _ state.Filter) ([]state.Runner, error) {
	panic("runner.StateAdapter.List: not implemented — use *Store.ListByStatus")
}

// Delete is intentionally unimplemented. The lifecycle pipeline marks
// records via status transitions (completed/failed); hard deletes rely on
// DynamoDB TTL.
func (a *StateAdapter) Delete(_ context.Context, _ string) error {
	panic("runner.StateAdapter.Delete: not implemented — TTL handles record lifetime")
}

// recordToRunner converts a *Record (DDB shape, with Unix int64 timestamps)
// into a state.Runner (cloud-agnostic shape, with time.Time timestamps).
// Zero-valued int64 timestamps become zero-valued time.Time values so callers
// can branch on time.Time.IsZero() without special-casing.
func recordToRunner(r *Record) state.Runner {
	if r == nil {
		return state.Runner{}
	}
	return state.Runner{
		ID:                r.RunnerID,
		InstanceID:        r.InstanceID,
		Repository:        r.Repository,
		Labels:            r.Labels,
		Status:            r.Status,
		LaunchedAt:        unixToTime(r.CreatedAt),
		UpdatedAt:         unixToTime(r.UpdatedAt),
		TTL:               unixToTime(r.TTL),
		GitHubRunnerID:    r.GitHubRunnerID,
		ReEnqueueAttempts: r.ReEnqueueAttempts,
		LastAttemptAt:     unixToTime(r.LastAttemptAt),
	}
}

// unixToTime converts a Unix-second int64 to a time.Time, mapping zero to a
// zero-valued time.Time so IsZero() works as expected. Non-zero values use
// time.Unix on the seconds component (records do not store sub-second
// precision).
func unixToTime(secs int64) time.Time {
	if secs == 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0)
}
