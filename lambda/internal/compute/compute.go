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
	// InstanceTypes is the ordered candidate list (AWS instance types / GCP
	// machine types). The launcher tries them in order; always ≥1 element.
	InstanceTypes []string
	// ImageID is the AMI ID (AWS) or fully-qualified GCE image URI (GCP).
	ImageID string
	// SubnetIDs are the candidate subnets (AWS) / subnet (GCP, uses [0]).
	// May be empty: AWS lets EC2 pick a default-VPC subnet; GCP uses opts.Subnet.
	SubnetIDs []string
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
	// LiveInstanceIDs returns the subset of ids that are still in a
	// cloud-side "alive" state (AWS: running/pending; GCP:
	// running/provisioning/staging) — i.e. not yet terminated, stopped, or
	// (AWS) reclaimed by spot. Used by scaleup's demand-aware supply check
	// so a pending state-store record whose underlying instance has
	// already been reclaimed does not count as live capacity for a retry
	// (see devopsfactory-io/jit-runners#105). Empty input returns an
	// empty, non-nil slice. Implementations should treat "instance
	// unknown to the cloud API" as not live rather than erroring, since a
	// reclaimed spot instance can age out of describe-by-ID visibility.
	LiveInstanceIDs(ctx context.Context, ids []string) ([]string, error)
}
