package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// runnerID computes the DDB primary key from repo + jobID. Mirrors the
// scaleup helper. A future refactor (Phase B3 in the plan) consolidates
// this in the runner package; for now it is local to lifecycle to keep
// this branch self-contained.
func runnerID(repo string, jobID int64) string {
	return fmt.Sprintf("%s#%d", repo, jobID)
}

// nextStatus is the forward-only state-machine transition table.
// (current, action) -> (next, ok). ok=false means: drop the message
// (backward transition or unknown action). next == current means: no-op.
func nextStatus(current, action string) (next string, ok bool) {
	switch action {
	case "in_progress":
		switch current {
		case state.StatusPending:
			return state.StatusRunning, true
		case state.StatusRunning:
			return current, true // no-op (already running)
		}
		return current, false
	case "completed":
		switch current {
		case state.StatusPending, state.StatusRunning:
			return state.StatusCompleted, true
		case state.StatusCompleted:
			return current, true // no-op (already completed)
		}
		return current, false
	}
	return current, false
}

// HandleSQS processes one SQS record body. Idempotent.
func (h *Handler) HandleSQS(ctx context.Context, body []byte) error {
	var msg Message
	if err := json.Unmarshal(body, &msg); err != nil {
		return fmt.Errorf("lifecycle: parse: %w", err)
	}

	key := runnerID(msg.Repo, msg.JobID)
	rec, err := h.Store.Get(ctx, key)
	switch {
	case errors.Is(err, state.ErrNotFound):
		h.Logger.Printf("lifecycle: drop %s job=%d action=%s: no record", msg.Repo, msg.JobID, msg.Action)
		return nil
	case err != nil:
		return fmt.Errorf("lifecycle: get %s: %w", key, err)
	}

	next, ok := nextStatus(rec.Status, msg.Action)
	if !ok {
		h.Logger.Printf("lifecycle: drop %s job=%d action=%s: backward transition from %s",
			msg.Repo, msg.JobID, msg.Action, rec.Status)
		return nil
	}
	if next == rec.Status {
		return nil // same-state no-op
	}

	now := time.Now()
	if err := h.Store.Update(ctx, key, state.RunnerUpdate{
		Status:        &next,
		LastAttemptAt: &now,
	}); err != nil {
		return fmt.Errorf("lifecycle: update %s: %w", key, err)
	}

	if msg.Action == "completed" && rec.GitHubRunnerID > 0 {
		if err := h.GitHub.DeregisterRunner(ctx, msg.Repo, rec.GitHubRunnerID); err != nil {
			h.Logger.Printf("lifecycle: deregister %d (%s): %v", rec.GitHubRunnerID, msg.Repo, err)
			// do not fail the message: DDB is committed; deregister is best-effort.
		}
	}
	return nil
}
