// Package provider builds and exposes the cloud-specific implementations
// of the four cloud-agnostic interfaces (queue, state, compute, secrets).
// Each cmd/<func> entry point loads exactly one Bundle at startup based on
// the CLOUD_PROVIDER env var.
package provider

import (
	"github.com/devopsfactory-io/jit-runners/lambda/internal/compute"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/queue"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/secrets"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// Bundle holds the cloud-specific implementations consumed by the entry
// points. JobsPublisher and LifecyclePublisher are separate fields because
// AWS routes the two payload types to two distinct SQS queues and GCP routes
// them to two distinct Pub/Sub topics — strict cloud parity.
type Bundle struct {
	JobsPublisher      queue.Publisher
	LifecyclePublisher queue.Publisher
	State              state.RunnerStore
	Compute            compute.Launcher
	Secrets            secrets.Loader

	// CloseFn is invoked by entry points on shutdown to flush clients.
	// Nil-safe: a nil Bundle or a Bundle without CloseFn is a no-op Close.
	CloseFn func() error
}

// Close invokes the bundle's CloseFn if set. Safe to call on a nil Bundle.
func (b *Bundle) Close() error {
	if b == nil || b.CloseFn == nil {
		return nil
	}
	return b.CloseFn()
}
