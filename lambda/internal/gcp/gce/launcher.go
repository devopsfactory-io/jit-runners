package gce

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	cpb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/compute"
)

// Label keys applied to launched instances.
const (
	labelManagedBy  = "managed-by"
	labelManagedVal = "jit-runners"
	labelRunnerID   = "jit-runners-id"
)

// gceIterator is a minimal interface over *gcpcompute.InstancesScopedListPairIterator
// that enables fake injection in tests.
type gceIterator interface {
	Next() (gcpcompute.InstancesScopedListPair, error)
}

// gceAPI is the narrow Compute Engine API surface the Launcher requires.
// Using a simplified signature (no variadic gax.CallOption) keeps fakes trivial
// while still covering the three operations we need.
type gceAPI interface {
	// Insert provisions a VM and returns its name. The LRO is not awaited —
	// the caller receives the instance name as soon as the insert is accepted.
	Insert(ctx context.Context, req *cpb.InsertInstanceRequest, opts ...interface{}) (string, error)
	// Delete terminates a VM by name+zone. 404 responses are treated as success
	// by the caller.
	Delete(ctx context.Context, req *cpb.DeleteInstanceRequest, opts ...interface{}) error
	// AggregatedList returns an iterator over all instances in the project
	// matching the request filter.
	AggregatedList(ctx context.Context, req *cpb.AggregatedListInstancesRequest, opts ...interface{}) gceIterator
}

// LauncherOptions configures GCP-specific launch parameters not part of
// the cloud-agnostic compute.LaunchSpec.
type LauncherOptions struct {
	// Project is the GCP project ID.
	Project string
	// Zone is the default zone for Insert and Delete calls, e.g. "us-central1-a".
	Zone string
	// Network is the fully-qualified network URL, e.g.
	// "projects/<p>/global/networks/default".
	Network string
	// Subnet is the fully-qualified subnetwork URL.
	Subnet string
	// Image is the fully-qualified image URI used when spec.ImageID is empty.
	Image string
	// ServiceAccount is the runner VM's service account email.
	ServiceAccount string
}

// Launcher manages GCE instance lifecycle for ephemeral runners.
// It satisfies compute.Launcher.
type Launcher struct {
	api  gceAPI
	opts LauncherOptions
}

// Compile-time assertion.
var _ compute.Launcher = (*Launcher)(nil)

// NewLauncher returns a Launcher backed by a real *gcpcompute.InstancesClient.
func NewLauncher(client *gcpcompute.InstancesClient, opts LauncherOptions) *Launcher {
	return &Launcher{api: &clientAdapter{client: client}, opts: opts}
}

// newLauncherWithAPI constructs a Launcher with an arbitrary gceAPI (for tests).
func newLauncherWithAPI(api gceAPI, opts LauncherOptions) *Launcher {
	return &Launcher{api: api, opts: opts}
}

// Launch provisions a SPOT VM with the runner startup script.
// It returns as soon as the insert request is accepted — it does NOT block on
// the LRO, matching the AWS RunInstances fire-and-return pattern.
func (l *Launcher) Launch(ctx context.Context, spec compute.LaunchSpec) (compute.Instance, error) {
	// Decode the startup script from base64 so it can be set as metadata value.
	scriptBytes, err := base64.StdEncoding.DecodeString(spec.UserData)
	if err != nil {
		return compute.Instance{}, fmt.Errorf("gcp/gce: decode startup script: %w", err)
	}
	script := string(scriptBytes)

	imageURI := spec.ImageID
	if imageURI == "" {
		imageURI = l.opts.Image
	}
	subnetURL := ""
	if len(spec.SubnetIDs) > 0 {
		subnetURL = spec.SubnetIDs[0]
	}
	if subnetURL == "" {
		subnetURL = l.opts.Subnet
	}

	// Build a deterministic instance name from the runner ID.  Names must be
	// RFC1035: lowercase letters, digits, hyphens, max 63 chars.
	name := fmt.Sprintf("jit-runner-%s", sanitizeLabel(spec.RunnerID))
	machineTypeURL := fmt.Sprintf("zones/%s/machineTypes/%s", l.opts.Zone, spec.InstanceTypes[0])

	labels := map[string]string{
		labelManagedBy: labelManagedVal,
	}
	if spec.RunnerID != "" {
		labels[labelRunnerID] = sanitizeLabel(spec.RunnerID)
	}

	trueVal := true
	spotModel := "SPOT"
	terminateAction := "DELETE"

	req := &cpb.InsertInstanceRequest{
		Project: l.opts.Project,
		Zone:    l.opts.Zone,
		InstanceResource: &cpb.Instance{
			Name:        &name,
			MachineType: &machineTypeURL,
			Labels:      labels,
			Disks: []*cpb.AttachedDisk{
				{
					Boot:       &trueVal,
					AutoDelete: &trueVal,
					InitializeParams: &cpb.AttachedDiskInitializeParams{
						SourceImage: &imageURI,
					},
				},
			},
			NetworkInterfaces: []*cpb.NetworkInterface{
				{
					Network:    &l.opts.Network,
					Subnetwork: &subnetURL,
				},
			},
			ServiceAccounts: []*cpb.ServiceAccount{
				{
					Email:  &l.opts.ServiceAccount,
					Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
				},
			},
			Scheduling: &cpb.Scheduling{
				ProvisioningModel:         &spotModel,
				Preemptible:               &trueVal,
				OnHostMaintenance:         strPtr("TERMINATE"),
				AutomaticRestart:          boolPtr(false),
				InstanceTerminationAction: &terminateAction,
			},
			Metadata: &cpb.Metadata{
				Items: []*cpb.Items{
					{Key: strPtr("startup-script"), Value: &script},
				},
			},
		},
	}

	instanceName, err := l.api.Insert(ctx, req)
	if err != nil {
		return compute.Instance{}, fmt.Errorf("gcp/gce: insert instance: %w", err)
	}

	return compute.Instance{
		ID:         instanceName,
		State:      "pending",
		LaunchedAt: time.Now().UTC(),
		RunnerID:   spec.RunnerID,
	}, nil
}

// Terminate deletes the named GCE instances. Idempotent — 404 responses are
// treated as success. A nil/empty list is a no-op.
func (l *Launcher) Terminate(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	var errs []error
	for _, id := range ids {
		err := l.api.Delete(ctx, &cpb.DeleteInstanceRequest{
			Project:  l.opts.Project,
			Zone:     l.opts.Zone,
			Instance: id,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("delete %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// ListStale returns all running/pending instances labelled managed-by=jit-runners
// that have been running longer than threshold.
func (l *Launcher) ListStale(ctx context.Context, threshold time.Duration) ([]compute.Instance, error) {
	filter := fmt.Sprintf("labels.%s=%s", labelManagedBy, labelManagedVal)
	iter := l.api.AggregatedList(ctx, &cpb.AggregatedListInstancesRequest{
		Project: l.opts.Project,
		Filter:  &filter,
	})

	cutoff := time.Time{}
	if threshold > 0 {
		cutoff = time.Now().Add(-threshold)
	}

	var result []compute.Instance
	for {
		pair, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("gcp/gce: aggregated list: %w", err)
		}
		if pair.Value == nil {
			continue
		}
		for _, inst := range pair.Value.GetInstances() {
			launchedAt := parseTimestamp(inst.GetCreationTimestamp())
			if !cutoff.IsZero() && !launchedAt.IsZero() && launchedAt.After(cutoff) {
				continue
			}
			result = append(result, compute.Instance{
				ID:         inst.GetName(),
				State:      inst.GetStatus(),
				LaunchedAt: launchedAt,
				RunnerID:   inst.GetLabels()[labelRunnerID],
			})
		}
	}
	return result, nil
}

// parseTimestamp parses a GCE creation timestamp (RFC3339) into a time.Time.
// Returns zero time on error.
func parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// sanitizeLabel converts a runner ID string to a GCE-safe label value (lowercase,
// no special chars beyond hyphen). For numeric IDs this is a no-op.
func sanitizeLabel(s string) string {
	out := make([]byte, 0, len(s))
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+32) // toLower
		case c >= '0' && c <= '9':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

// clientAdapter wraps *gcpcompute.InstancesClient to satisfy gceAPI.
// It fire-and-returns on Insert/Delete (does not await LROs).
type clientAdapter struct {
	client *gcpcompute.InstancesClient
}

func (a *clientAdapter) Insert(ctx context.Context, req *cpb.InsertInstanceRequest, _ ...interface{}) (string, error) {
	// Fire insert — do not call op.Wait(). The VM name is known before the LRO
	// completes, so we return immediately after the RPC is accepted.
	_, err := a.client.Insert(ctx, req)
	if err != nil {
		return "", err
	}
	return req.GetInstanceResource().GetName(), nil
}

func (a *clientAdapter) Delete(ctx context.Context, req *cpb.DeleteInstanceRequest, _ ...interface{}) error {
	_, err := a.client.Delete(ctx, req)
	return err
}

func (a *clientAdapter) AggregatedList(ctx context.Context, req *cpb.AggregatedListInstancesRequest, _ ...interface{}) gceIterator {
	return a.client.AggregatedList(ctx, req)
}
