package lifecycle

import (
	"context"
	"log"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/compute"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// ghClient is the subset of the GitHub client the lifecycle handler needs.
// Defined as an interface so tests can substitute a fake without importing
// the real github package or its transport dependencies.
type ghClient interface {
	DeregisterRunner(ctx context.Context, ownerRepo string, runnerID int64) error
}

// computeLauncher is the subset of compute.Launcher the lifecycle handler
// needs (just Terminate). Defined as a local interface so tests can
// substitute a fake without satisfying the full Launcher contract.
type computeLauncher interface {
	Terminate(ctx context.Context, ids []string) error
}

// Handler dispatches lifecycle messages to DDB updates, GitHub deregister
// calls, and (per issue #74) cloud instance termination on the `completed`
// transition.
type Handler struct {
	Store   state.RunnerStore
	GitHub  ghClient
	Compute computeLauncher
	Logger  *log.Logger
}

// New constructs a Handler with sane defaults (stdlib logger if nil).
func New(store state.RunnerStore, gh ghClient, comp compute.Launcher, logger *log.Logger) *Handler {
	if logger == nil {
		logger = log.Default()
	}
	return &Handler{Store: store, GitHub: gh, Compute: comp, Logger: logger}
}
