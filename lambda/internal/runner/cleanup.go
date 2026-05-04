package runner

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/compute"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/queue"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// ghClient is the narrow surface of the GitHub client used by Cleaner.
// Defined locally so unit tests can fake it without importing the real
// github package. Satisfied by *github.Client.
type ghClient interface {
	DeregisterRunner(ctx context.Context, ownerRepo string, runnerID int64) error
}

// scaleupPublisher is the narrow surface of the queue publisher used by
// Cleaner to re-enqueue stale-pending records. Satisfied by queue.Publisher
// (any cloud).
type scaleupPublisher interface {
	Publish(ctx context.Context, m queue.Msg) error
}

// Cleaner reaps stale runner records and (for stale-pending) re-enqueues a
// fresh launch attempt with an attempt counter. Stale-running records are
// terminated but NEVER re-enqueued — re-enqueueing a running record can
// double-launch when GitHub may already consider the job done.
//
// Cleaner depends on the cloud-agnostic state.RunnerStore and
// compute.Launcher interfaces. The concrete AWS implementations live under
// internal/aws/{dynamo,ec2}.
type Cleaner struct {
	Store            state.RunnerStore
	Launcher         compute.Launcher
	GitHub           ghClient
	ScaleUpPublisher scaleupPublisher

	// StaleAfter is the threshold applied to status=pending records.
	// A pending record older than now-StaleAfter is considered stuck.
	StaleAfter time.Duration
	// MaxAge is the threshold applied to status=running records. A running
	// record older than now-MaxAge is reaped (terminated + marked failed).
	MaxAge time.Duration
	// MaxReEnqueueAttempts is the budget for stale-pending re-enqueues.
	// When ReEnqueueAttempts has reached this value the record is marked
	// terminal failed and an ERROR is logged instead of re-publishing.
	MaxReEnqueueAttempts int

	// OrphanGraceMinutes is the minimum age of a cloud-side instance
	// before sweepOrphanInstances will consider terminating it. Avoids
	// races with healthy runner self-terminate. Default 5 min.
	OrphanGraceMinutes time.Duration

	// Now is injectable for tests; defaults to time.Now if unset.
	Now func() time.Time
}

// NewCleaner builds a Cleaner. staleMinutes and maxAgeMinutes apply to
// pending and running records respectively; maxReEnqueueAttempts caps the
// number of times a stale-pending record may be re-published.
func NewCleaner(
	store state.RunnerStore,
	launcher compute.Launcher,
	gh ghClient,
	pub scaleupPublisher,
	staleMinutes, maxAgeMinutes, maxReEnqueueAttempts, orphanGraceMinutes int,
) *Cleaner {
	if staleMinutes <= 0 {
		staleMinutes = 10
	}
	if maxAgeMinutes <= 0 {
		maxAgeMinutes = 360 // 6 hours
	}
	if maxReEnqueueAttempts < 0 {
		maxReEnqueueAttempts = 0
	}
	if orphanGraceMinutes <= 0 {
		orphanGraceMinutes = 5
	}
	return &Cleaner{
		Store:                store,
		Launcher:             launcher,
		GitHub:               gh,
		ScaleUpPublisher:     pub,
		StaleAfter:           time.Duration(staleMinutes) * time.Minute,
		MaxAge:               time.Duration(maxAgeMinutes) * time.Minute,
		MaxReEnqueueAttempts: maxReEnqueueAttempts,
		OrphanGraceMinutes:   time.Duration(orphanGraceMinutes) * time.Minute,
		Now:                  time.Now,
	}
}

// CleanupResult summarizes a cleanup pass.
//
// Stale counts records that were terminated (pending or running). Orphans
// counts records that were processed by the stale-pending path (re-enqueued
// or terminally failed because the budget was exhausted). Errors counts any
// path where termination or publish failed and we abandoned the record.
type CleanupResult struct {
	Stale      int
	Orphans    int
	EC2Orphans int // cloud-side orphans (DDB terminal/missing, EC2 alive)
	Errors     int
}

// Run executes the cleanup logic:
//  1. Stale-pending sweep — terminate, deregister, re-enqueue if budget
//     remains, otherwise mark terminal failed.
//  2. Stale-running sweep — terminate, deregister, mark failed. NEVER
//     re-enqueue a running record.
func (c *Cleaner) Run(ctx context.Context) (*CleanupResult, error) {
	result := &CleanupResult{}
	now := c.now()

	pending, err := c.Store.List(ctx, state.Filter{StatusEq: StatusPending})
	if err != nil {
		return result, fmt.Errorf("list pending runners: %w", err)
	}
	staleCutoff := now.Add(-c.StaleAfter)
	c.sweepStalePending(ctx, pending, staleCutoff, now, result)

	running, err := c.Store.List(ctx, state.Filter{StatusEq: StatusRunning})
	if err != nil {
		return result, fmt.Errorf("list running runners: %w", err)
	}
	runCutoff := now.Add(-c.MaxAge)
	c.sweepStaleRunning(ctx, running, runCutoff, now, result)

	c.sweepOrphanInstances(ctx, now, result)

	return result, nil
}

// sweepStalePending handles status=pending records older than the cutoff:
// terminate the EC2 instance (if any), deregister the GitHub runner (if
// any), and either re-enqueue a fresh launch attempt or mark the record
// terminal failed when the attempt budget is exhausted.
func (c *Cleaner) sweepStalePending(ctx context.Context, runners []state.Runner, cutoff time.Time, now time.Time, result *CleanupResult) {
	for _, r := range runners {
		if !r.LaunchedAt.Before(cutoff) {
			continue
		}
		log.Printf("cleanup: terminating stale pending runner %s (instance %s, attempts=%d)",
			r.ID, r.InstanceID, r.ReEnqueueAttempts)

		if err := c.terminateAndDeregister(ctx, r); err != nil {
			result.Errors++
			continue
		}
		result.Stale++

		if r.ReEnqueueAttempts < c.MaxReEnqueueAttempts {
			next := r.ReEnqueueAttempts + 1
			msg := &queue.ScaleUpMessage{
				EventAction:       "queued",
				JobID:             r.JobID,
				RepositoryFull:    r.Repository,
				Labels:            r.Labels,
				Source:            queue.SourceWebhook,
				ReEnqueueAttempts: next,
			}
			if err := queue.PublishScaleUp(ctx, c.ScaleUpPublisher, msg); err != nil {
				log.Printf("cleanup: re-enqueue publish failed for %s: %v", r.ID, err)
				result.Errors++
				continue
			}
			failed := StatusFailed
			at := now
			attempts := next
			if err := c.Store.Update(ctx, r.ID, state.RunnerUpdate{
				Status:            &failed,
				ReEnqueueAttempts: &attempts,
				LastAttemptAt:     &at,
			}); err != nil {
				log.Printf("cleanup: update after re-enqueue failed for %s: %v", r.ID, err)
				result.Errors++
				continue
			}
			result.Orphans++
			continue
		}

		// Budget exhausted — mark terminal failed and log loudly. Do NOT
		// re-enqueue: the message has been retried MaxReEnqueueAttempts
		// times already and continued failure indicates a systemic issue
		// that retries cannot resolve.
		log.Printf("ERROR scaledown: re-enqueue exhausted for %s after %d attempts",
			r.ID, r.ReEnqueueAttempts)
		failed := StatusFailed
		at := now
		if err := c.Store.Update(ctx, r.ID, state.RunnerUpdate{
			Status:        &failed,
			LastAttemptAt: &at,
		}); err != nil {
			log.Printf("cleanup: update terminal failed for %s: %v", r.ID, err)
			result.Errors++
			continue
		}
		result.Orphans++
	}
}

// sweepStaleRunning handles status=running records older than the cutoff:
// terminate the EC2 instance, deregister the GitHub runner, mark the record
// failed. NEVER re-enqueue — a running record may correspond to a job that
// GitHub already considers complete; re-enqueueing would risk a double
// launch.
func (c *Cleaner) sweepStaleRunning(ctx context.Context, runners []state.Runner, cutoff time.Time, now time.Time, result *CleanupResult) {
	for _, r := range runners {
		if !r.LaunchedAt.Before(cutoff) {
			continue
		}
		log.Printf("cleanup: reaping stale running runner %s (instance %s)", r.ID, r.InstanceID)

		if err := c.terminateAndDeregister(ctx, r); err != nil {
			result.Errors++
			continue
		}
		failed := StatusFailed
		at := now
		if err := c.Store.Update(ctx, r.ID, state.RunnerUpdate{
			Status:        &failed,
			LastAttemptAt: &at,
		}); err != nil {
			log.Printf("cleanup: update failed for stale running %s: %v", r.ID, err)
			result.Errors++
			continue
		}
		result.Stale++
	}
}

// terminateAndDeregister performs the two outbound side-effects shared by
// both sweep paths: kill the EC2 instance (if any) and deregister the
// GitHub runner registration (if any). The deregister call returns nil for
// 404 already, so non-404 errors are logged but do not block downstream
// state updates — failing the cleanup pass entirely on a transient GitHub
// outage would leave records stuck. Returns an error only on EC2
// termination failure, which is the one case we cannot safely proceed
// past (the instance may still be running).
func (c *Cleaner) terminateAndDeregister(ctx context.Context, r state.Runner) error {
	if r.InstanceID != "" {
		if err := c.Launcher.Terminate(ctx, []string{r.InstanceID}); err != nil {
			log.Printf("cleanup: terminate %s failed: %v", r.InstanceID, err)
			return err
		}
	}
	if r.GitHubRunnerID > 0 && c.GitHub != nil {
		if err := c.GitHub.DeregisterRunner(ctx, r.Repository, r.GitHubRunnerID); err != nil {
			log.Printf("cleanup: deregister runner %d for %s failed: %v",
				r.GitHubRunnerID, r.Repository, err)
		}
	}
	return nil
}

func (c *Cleaner) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// sweepOrphanInstances lists cloud-side instances tagged managed-by=jit-runners
// (via the existing Launcher.ListStale primitive — filter semantics match
// exactly: managed-by tag + state in [running, pending] + age threshold)
// and terminates any whose RunnerStore row is in a terminal state
// (StatusCompleted/StatusFailed) or missing entirely.
//
// Per issue #74 design D1.2: the architectural fix in the lifecycle handler
// is the primary path; this sweep is the safety net.
func (c *Cleaner) sweepOrphanInstances(ctx context.Context, now time.Time, result *CleanupResult) {
	grace := c.OrphanGraceMinutes
	if grace <= 0 {
		grace = 5 * time.Minute
	}
	instances, err := c.Launcher.ListStale(ctx, grace)
	if err != nil {
		log.Printf("orphan sweep: list instances failed: %v", err)
		result.Errors++
		return
	}
	for _, inst := range instances {
		rec, err := c.Store.GetByInstanceID(ctx, inst.ID)
		switch {
		case errors.Is(err, state.ErrNotFound):
			log.Printf("orphan sweep: terminating %s (no DDB row, age=%s)",
				inst.ID, now.Sub(inst.LaunchedAt))
			if termErr := c.Launcher.Terminate(ctx, []string{inst.ID}); termErr != nil {
				log.Printf("orphan sweep: terminate %s failed: %v", inst.ID, termErr)
				result.Errors++
				continue
			}
			result.EC2Orphans++
		case err != nil:
			log.Printf("orphan sweep: lookup %s failed: %v", inst.ID, err)
			result.Errors++
		case rec.Status == state.StatusCompleted || rec.Status == state.StatusFailed:
			log.Printf("orphan sweep: terminating %s (DDB status=%s, age=%s)",
				inst.ID, rec.Status, now.Sub(inst.LaunchedAt))
			if termErr := c.Launcher.Terminate(ctx, []string{inst.ID}); termErr != nil {
				log.Printf("orphan sweep: terminate %s failed: %v", inst.ID, termErr)
				result.Errors++
				continue
			}
			result.EC2Orphans++
		default:
			// Status is StatusPending or StatusRunning — existing
			// sweeps own these. Leave alone.
		}
	}
}
