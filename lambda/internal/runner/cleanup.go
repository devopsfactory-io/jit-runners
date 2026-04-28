package runner

import (
	"context"
	"fmt"
	"log"
	"time"
)

// EC2Terminator abstracts EC2 instance termination for testing.
type EC2Terminator interface {
	Terminate(ctx context.Context, instanceIDs ...string) error
	ListManagedInstances(ctx context.Context) ([]string, error)
}

// Cleaner handles stale and orphaned runner cleanup.
type Cleaner struct {
	store                 *Store
	ec2                   EC2Terminator
	staleThresholdMinutes int
	maxAgeMinutes         int
}

// NewCleaner creates a Cleaner with the given thresholds.
func NewCleaner(store *Store, ec2 EC2Terminator, staleMinutes, maxAgeMinutes int) *Cleaner {
	if staleMinutes <= 0 {
		staleMinutes = 10
	}
	if maxAgeMinutes <= 0 {
		maxAgeMinutes = 360 // 6 hours
	}
	return &Cleaner{
		store:                 store,
		ec2:                   ec2,
		staleThresholdMinutes: staleMinutes,
		maxAgeMinutes:         maxAgeMinutes,
	}
}

// CleanupResult summarizes the cleanup operation.
type CleanupResult struct {
	StaleTerminated  int
	OrphanTerminated int
	Errors           int
}

// Run executes the cleanup logic:
// 1. Terminate pending instances older than staleThreshold.
// 2. Terminate running instances older than maxAge.
// 3. Reconcile EC2 instances vs DynamoDB records for orphans.
func (c *Cleaner) Run(ctx context.Context) (*CleanupResult, error) {
	result := &CleanupResult{}
	now := time.Now().Unix()

	// 1. Clean up stale "pending" instances.
	pending, err := c.store.ListByStatus(ctx, StatusPending)
	if err != nil {
		return result, err
	}
	staleThreshold := now - int64(c.staleThresholdMinutes*60)
	if err := c.cleanupStaleInstances(ctx, pending, staleThreshold, "pending", result); err != nil {
		return result, err
	}

	// 2. Clean up stuck "running" instances.
	running, err := c.store.ListByStatus(ctx, StatusRunning)
	if err != nil {
		return result, err
	}
	maxAgeThreshold := now - int64(c.maxAgeMinutes*60)
	if err := c.cleanupStaleInstances(ctx, running, maxAgeThreshold, "running", result); err != nil {
		return result, err
	}

	// 3. Detect orphaned EC2 instances (tagged but not in DynamoDB).
	allRecords := append(pending, running...)
	completed, err := c.store.ListByStatus(ctx, StatusCompleted)
	if err != nil {
		return result, fmt.Errorf("list completed runners: %w", err)
	}
	failed, err := c.store.ListByStatus(ctx, StatusFailed)
	if err != nil {
		return result, fmt.Errorf("list failed runners: %w", err)
	}
	allRecords = append(allRecords, completed...)
	allRecords = append(allRecords, failed...)

	if err := c.reconcileOrphanInstances(ctx, allRecords, result); err != nil {
		return result, err
	}

	return result, nil
}

// cleanupStaleInstances terminates instances that have been in the given status longer than the threshold.
func (c *Cleaner) cleanupStaleInstances(ctx context.Context, records []*Record, threshold int64, statusLabel string, result *CleanupResult) error {
	for _, r := range records {
		if r.CreatedAt < threshold {
			log.Printf("cleanup: terminating stale %s runner %s (instance %s)", statusLabel, r.RunnerID, r.InstanceID)
			if err := c.ec2.Terminate(ctx, r.InstanceID); err != nil {
				log.Printf("cleanup: failed to terminate %s: %v", r.InstanceID, err)
				result.Errors++
				continue
			}
			if err := c.store.UpdateStatus(ctx, r.Repository, r.JobID, StatusFailed); err != nil {
				log.Printf("cleanup: failed to update status for %s: %v", r.RunnerID, err)
				result.Errors++
				continue
			}
			result.StaleTerminated++
		}
	}
	return nil
}

// reconcileOrphanInstances finds EC2 instances not tracked in DynamoDB and terminates them.
func (c *Cleaner) reconcileOrphanInstances(ctx context.Context, knownRecords []*Record, result *CleanupResult) error {
	managedIDs, err := c.ec2.ListManagedInstances(ctx)
	if err != nil {
		return err
	}
	knownIDs := make(map[string]bool)
	for _, r := range knownRecords {
		knownIDs[r.InstanceID] = true
	}
	for _, id := range managedIDs {
		if !knownIDs[id] {
			log.Printf("cleanup: terminating orphaned instance %s", id)
			if err := c.ec2.Terminate(ctx, id); err != nil {
				log.Printf("cleanup: failed to terminate orphan %s: %v", id, err)
				result.Errors++
				continue
			}
			result.OrphanTerminated++
		}
	}
	return nil
}
