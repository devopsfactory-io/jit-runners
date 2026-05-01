// Package state defines the cloud-agnostic runner state-store contract.
package state

import (
	"context"
	"errors"
	"time"
)

// Status values used in Runner.Status.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// ErrNotFound is returned by RunnerStore.Get when no record exists.
var ErrNotFound = errors.New("state: runner not found")

// Runner is the persisted state for a runner managed by jit-runners.
type Runner struct {
	ID                string
	InstanceID        string
	Repository        string
	Labels            []string
	Status            string
	LaunchedAt        time.Time
	UpdatedAt         time.Time
	TTL               time.Time
	GitHubRunnerID    int64
	ReEnqueueAttempts int
	LastAttemptAt     time.Time
}

// Filter narrows a List query.
type Filter struct {
	StatusEq       string        // empty = no filter
	OlderThan      time.Duration // zero = no filter (compares against LaunchedAt)
	IncludeExpired bool
}

// RunnerUpdate carries pointer-typed fields for partial updates. nil = leave unchanged.
type RunnerUpdate struct {
	Status            *string
	InstanceID        *string
	GitHubRunnerID    *int64
	ReEnqueueAttempts *int
	LastAttemptAt     *time.Time
}

// RunnerStore persists Runner records with TTL semantics. Implementations:
// internal/aws/dynamo (DynamoDB on-demand) and internal/gcp/firestore.
type RunnerStore interface {
	Put(ctx context.Context, r Runner) error
	Get(ctx context.Context, id string) (Runner, error)
	List(ctx context.Context, f Filter) ([]Runner, error)
	Update(ctx context.Context, id string, fields RunnerUpdate) error
	Delete(ctx context.Context, id string) error
}
