package gce

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	cpb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/compute"
)

// fakeGCE is the in-memory test double for gceAPI.
type fakeGCE struct {
	insertCalled   bool
	insertReq      *cpb.InsertInstanceRequest
	insertReturnID string
	insertErr      error

	deleteCalls []string
	deleteErr   error

	listPairs []gcpcompute.InstancesScopedListPair
	listErr   error
}

func (f *fakeGCE) Insert(_ context.Context, req *cpb.InsertInstanceRequest, _ ...interface{}) (string, error) {
	f.insertCalled = true
	f.insertReq = req
	return f.insertReturnID, f.insertErr
}

func (f *fakeGCE) Delete(_ context.Context, req *cpb.DeleteInstanceRequest, _ ...interface{}) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleteCalls = append(f.deleteCalls, req.Instance)
	return nil
}

func (f *fakeGCE) AggregatedList(_ context.Context, _ *cpb.AggregatedListInstancesRequest, _ ...interface{}) gceIterator {
	return &fakePairIterator{pairs: f.listPairs, err: f.listErr}
}

// fakePairIterator satisfies gceIterator.
type fakePairIterator struct {
	pairs []gcpcompute.InstancesScopedListPair
	pos   int
	err   error
}

func (it *fakePairIterator) Next() (gcpcompute.InstancesScopedListPair, error) {
	if it.err != nil {
		return gcpcompute.InstancesScopedListPair{}, it.err
	}
	if it.pos >= len(it.pairs) {
		return gcpcompute.InstancesScopedListPair{}, iterator.Done
	}
	p := it.pairs[it.pos]
	it.pos++
	return p, nil
}

func defaultOpts() LauncherOptions {
	return LauncherOptions{
		Project:        "my-project",
		Zone:           "us-central1-a",
		Network:        "projects/my-project/global/networks/default",
		Subnet:         "projects/my-project/regions/us-central1/subnetworks/default",
		Image:          "projects/ubuntu-os-cloud/global/images/ubuntu-2404-noble-amd64-v20240801",
		ServiceAccount: "runner@my-project.iam.gserviceaccount.com",
	}
}

func defaultSpec() compute.LaunchSpec {
	return compute.LaunchSpec{
		Labels:        []string{"large"},
		InstanceTypes: []string{"n2-standard-4"},
		ImageID:       "projects/ubuntu-os-cloud/global/images/ubuntu-2404-noble-amd64-v20240801",
		SubnetIDs:     []string{"projects/my-project/regions/us-central1/subnetworks/default"},
		UserData:      base64.StdEncoding.EncodeToString([]byte("#!/bin/bash\necho hello")),
		RunnerID:      "42",
	}
}

func TestLauncher_Launch_ReturnsInstance(t *testing.T) {
	fake := &fakeGCE{insertReturnID: "jit-runner-abcdef12"}
	launcher := newLauncherWithAPI(fake, defaultOpts())

	inst, err := launcher.Launch(context.Background(), defaultSpec())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.ID != "jit-runner-abcdef12" {
		t.Errorf("inst.ID = %q, want %q", inst.ID, "jit-runner-abcdef12")
	}
	if inst.State != "pending" {
		t.Errorf("inst.State = %q, want %q", inst.State, "pending")
	}
	if inst.RunnerID != "42" {
		t.Errorf("inst.RunnerID = %q, want %q", inst.RunnerID, "42")
	}
	if !fake.insertCalled {
		t.Error("expected Insert to be called")
	}
	// Verify SPOT provisioning model is set.
	sched := fake.insertReq.GetInstanceResource().GetScheduling()
	if sched == nil {
		t.Fatal("Scheduling is nil")
	}
	if sched.GetProvisioningModel() != "SPOT" {
		t.Errorf("ProvisioningModel = %q, want SPOT", sched.GetProvisioningModel())
	}
	if !sched.GetPreemptible() {
		t.Error("Preemptible should be true for SPOT VMs")
	}
	// Verify managed-by label is set.
	labels := fake.insertReq.GetInstanceResource().GetLabels()
	if labels[labelManagedBy] != labelManagedVal {
		t.Errorf("label %q = %q, want %q", labelManagedBy, labels[labelManagedBy], labelManagedVal)
	}
}

func TestLauncher_Launch_PropagatesError(t *testing.T) {
	insertErr := errors.New("quota exceeded")
	fake := &fakeGCE{insertErr: insertErr}
	launcher := newLauncherWithAPI(fake, defaultOpts())

	_, err := launcher.Launch(context.Background(), defaultSpec())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, insertErr) {
		t.Errorf("error %v does not wrap %v", err, insertErr)
	}
}

func TestLauncher_Terminate_DeletesInstances(t *testing.T) {
	fake := &fakeGCE{}
	launcher := newLauncherWithAPI(fake, defaultOpts())

	err := launcher.Terminate(context.Background(), []string{"inst-1", "inst-2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.deleteCalls) != 2 {
		t.Errorf("deleteCalls = %d, want 2", len(fake.deleteCalls))
	}
}

func TestLauncher_ListStale_FiltersByLaunchTime(t *testing.T) {
	now := time.Now()
	oldTS := now.Add(-2 * time.Hour).Format(time.RFC3339)
	freshTS := now.Add(-1 * time.Minute).Format(time.RFC3339)

	spot := proto("SPOT")
	pairs := []gcpcompute.InstancesScopedListPair{
		{
			Key: "zones/us-central1-a",
			Value: &cpb.InstancesScopedList{
				Instances: []*cpb.Instance{
					{
						Name:              proto("old-runner"),
						Status:            proto("RUNNING"),
						CreationTimestamp: &oldTS,
						Labels:            map[string]string{labelManagedBy: labelManagedVal},
						Scheduling:        &cpb.Scheduling{ProvisioningModel: spot},
					},
					{
						Name:              proto("fresh-runner"),
						Status:            proto("RUNNING"),
						CreationTimestamp: &freshTS,
						Labels:            map[string]string{labelManagedBy: labelManagedVal},
						Scheduling:        &cpb.Scheduling{ProvisioningModel: spot},
					},
				},
			},
		},
	}

	fake := &fakeGCE{listPairs: pairs}
	launcher := newLauncherWithAPI(fake, defaultOpts())

	stale, err := launcher.ListStale(context.Background(), 1*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("stale count = %d, want 1", len(stale))
	}
	if stale[0].ID != "old-runner" {
		t.Errorf("stale[0].ID = %q, want %q", stale[0].ID, "old-runner")
	}
}

// proto is a helper to get a pointer to a string literal.
func proto(s string) *string {
	return &s
}
