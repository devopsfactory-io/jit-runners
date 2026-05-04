package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/queue"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

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
//
// Lookups key on msg.RunnerID — the int64 GitHub returned from
// generate-jitconfig and that GitHub populates in workflow_job webhook
// deliveries. Per issue #52, this is the only stable runner identifier;
// the prior (repo, job_id) lookup was racy under concurrent jobs.
func (h *Handler) HandleSQS(ctx context.Context, body []byte) error {
	var msg queue.LifecycleMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return fmt.Errorf("lifecycle: parse: %w", err)
	}

	if msg.RunnerID == 0 {
		// Defensive: GitHub did not populate runner_id in the webhook
		// payload. There is no record to update; drop the message
		// without a lookup so we do not pollute logs with bogus
		// "no record at key 0" entries.
		h.Logger.Printf("lifecycle: drop %s job=%d action=%s: missing runner_id",
			msg.Repo, msg.JobID, msg.Action)
		return nil
	}

	key := strconv.FormatInt(msg.RunnerID, 10)
	rec, err := h.Store.Get(ctx, key)
	switch {
	case errors.Is(err, state.ErrNotFound):
		h.Logger.Printf("lifecycle: drop %s job=%d runner=%d action=%s: no record",
			msg.Repo, msg.JobID, msg.RunnerID, msg.Action)
		return nil
	case err != nil:
		return fmt.Errorf("lifecycle: get %s: %w", key, err)
	}

	next, ok := nextStatus(rec.Status, msg.Action)
	if !ok {
		h.Logger.Printf("lifecycle: drop %s job=%d runner=%d action=%s: backward transition from %s",
			msg.Repo, msg.JobID, msg.RunnerID, msg.Action, rec.Status)
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
