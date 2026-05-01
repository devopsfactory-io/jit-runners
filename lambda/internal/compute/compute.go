// Package compute defines the cloud-agnostic runner-launcher contract.
package compute

import (
	"context"
	"time"
)

// LaunchSpec describes one runner instance to launch.
type LaunchSpec struct {
	// Labels passed to the JIT runner registration; used to pick instance type.
	Labels []string
	// InstanceType (AWS) / MachineType (GCP), resolved by the caller from labels.
	InstanceType string
	// ImageID is the AMI ID (AWS) or fully-qualified GCE image URI (GCP).
	ImageID string
	// SubnetID (AWS) / Subnet (GCP).
	SubnetID string
	// UserData (AWS cloud-init) / StartupScript (GCP). Both pass the same script body.
	UserData string
	// RunnerID is the jit-runners-assigned ID, surfaced as a tag/label so the
	// scaledown loop can match instances back to RunnerStore records.
	RunnerID string
}

// Instance represents a launched compute resource.
type Instance struct {
	ID         string
	State      string // running, pending, stopping, terminated, ...
	LaunchedAt time.Time
	RunnerID   string // value of the jit-runners-id tag/label, if set
}

// Launcher launches and terminates one-shot Spot/Spot-VM instances.
type Launcher interface {
	Launch(ctx context.Context, spec LaunchSpec) (Instance, error)
	Terminate(ctx context.Context, ids []string) error
	ListStale(ctx context.Context, threshold time.Duration) ([]Instance, error)
}
