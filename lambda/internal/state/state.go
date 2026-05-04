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
//
// ID is the stringified GitHub runner_id returned by
// generate-jitconfig — the only stable identifier post-registration.
// Lookups across packages key on this value (see internal/lifecycle
// and cmd/scaleup). JobID and WorkflowRunID are observability
// metadata derived from the queue-trigger workflow_job webhook;
// they are NEVER lookup keys.
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
	JobID             int64
	WorkflowRunID     int64
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
	// ListActiveRepos returns the deduped Repository values across runner
	// records launched at or after `since`. Used by the rebalancer Lambda
	// to scope its per-cycle GitHub queue queries to repos that have
	// recent webhook-driven activity. Implementations may scan or query
	// the underlying store; results need not be sorted.
	ListActiveRepos(ctx context.Context, since time.Time) ([]string, error)
}

// MatchesLabels reports whether a runner with `runnerLabels` can satisfy a
// job with `jobLabels`. GitHub's matcher uses subset semantics: a runner
// matches a job iff every job label is present in the runner's label set.
// The compare is case-insensitive and order-insensitive.
//
// Used by both cmd/scaleup (to count matching pending runners as supply)
// and internal/rebalancer (to compute per-label-set supply).
func MatchesLabels(runnerLabels, jobLabels []string) bool {
	have := make(map[string]struct{}, len(runnerLabels))
	for _, l := range runnerLabels {
		have[normalizeLabel(l)] = struct{}{}
	}
	for _, l := range jobLabels {
		if _, ok := have[normalizeLabel(l)]; !ok {
			return false
		}
	}
	return true
}

func normalizeLabel(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
