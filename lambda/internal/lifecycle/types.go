package lifecycle

import (
	"context"
	"log"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// ghClient is the subset of the GitHub client the lifecycle handler needs.
// Defined as an interface so tests can substitute a fake without importing
// the real github package or its transport dependencies.
type ghClient interface {
	DeregisterRunner(ctx context.Context, ownerRepo string, runnerID int64) error
}

// Handler dispatches lifecycle messages to DDB updates and GitHub deregister calls.
type Handler struct {
	Store  state.RunnerStore
	GitHub ghClient
	Logger *log.Logger
}

// New constructs a Handler with sane defaults (stdlib logger if nil).
func New(store state.RunnerStore, gh ghClient, logger *log.Logger) *Handler {
	if logger == nil {
		logger = log.Default()
	}
	return &Handler{Store: store, GitHub: gh, Logger: logger}
}
