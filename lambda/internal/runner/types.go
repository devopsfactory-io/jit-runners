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

// IDFromGitHubRunnerID derives the canonical runner ID from a GitHub runner
// ID returned by the generate-jitconfig endpoint. The format is the decimal
// string form of the int64; this is the DynamoDB partition key value.
//
// JobID and workflow_run_id are intentionally NOT part of the ID — GitHub's
// JIT contract does not bind a registered runner to either, and earlier
// "<repo>#<jobID>" encodings produced racy lookups under concurrent jobs.
// See zettelkasten Projects/jit-runners/specs/2026-05-02-runner-id-realignment-design.md.
func IDFromGitHubRunnerID(ghRunnerID int64) string {
	return strconv.FormatInt(ghRunnerID, 10)
}

// New builds a Runner with sensible defaults: status=pending, LaunchedAt=now,
// UpdatedAt=now, TTL=now+24h. The runner ID is derived from githubRunnerID.
// jobID and workflowRunID are stored as observability metadata only.
func New(repository string, githubRunnerID int64, instanceID string, jobID int64, workflowRunID int64, labels []string) Runner {
	now := time.Now().UTC()
	return Runner{
		ID:             IDFromGitHubRunnerID(githubRunnerID),
		InstanceID:     instanceID,
		Repository:     repository,
		Labels:         labels,
		Status:         StatusPending,
		LaunchedAt:     now,
		UpdatedAt:      now,
		TTL:            now.Add(24 * time.Hour),
		GitHubRunnerID: githubRunnerID,
		JobID:          jobID,
		WorkflowRunID:  workflowRunID,
	}
}
