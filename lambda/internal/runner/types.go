// Package runner contains cloud-agnostic helpers for working with runner
// state. The persistence layer lives in internal/aws/dynamo or
// internal/gcp/firestore; this package only defines the logical types and
// ID-derivation rules and the cleanup orchestrator (Cleaner) consumed by the
// scaledown Lambda.
package runner

import (
	"strconv"
	"time"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// Status constants are re-exported from the canonical state package so
// callers that historically used runner.Status* keep compiling. New code
// should prefer state.Status* directly.
const (
	StatusPending   = state.StatusPending
	StatusRunning   = state.StatusRunning
	StatusCompleted = state.StatusCompleted
	StatusFailed    = state.StatusFailed
)

// Runner is an alias for state.Runner so cloud-agnostic callers can refer to
// the runner record without importing internal/state directly. New code
// should prefer state.Runner.
type Runner = state.Runner

// ID derives the canonical runner ID from a repository full name and job ID.
// The format ("<repo>#<jobID>") matches the pre-refactor DynamoDB primary
// key so live records continue to round-trip.
func ID(repository string, jobID int64) string {
	return repository + "#" + strconv.FormatInt(jobID, 10)
}

// New builds a Runner with sensible defaults: status=pending, LaunchedAt=now,
// UpdatedAt=now, TTL=now+24h. The runner ID is derived from repository+jobID.
func New(repository string, jobID int64, instanceID string, labels []string) Runner {
	now := time.Now().UTC()
	return Runner{
		ID:         ID(repository, jobID),
		InstanceID: instanceID,
		Repository: repository,
		Labels:     labels,
		Status:     StatusPending,
		LaunchedAt: now,
		UpdatedAt:  now,
		TTL:        now.Add(24 * time.Hour),
	}
}
