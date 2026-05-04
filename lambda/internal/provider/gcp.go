package provider

import (
	"context"
	"errors"
	"fmt"
	"os"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	cf "cloud.google.com/go/firestore"
	pubsubclient "cloud.google.com/go/pubsub/v2"
	smclient "cloud.google.com/go/secretmanager/apiv1"

	gcpfirestore "github.com/devopsfactory-io/jit-runners/lambda/internal/gcp/firestore"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/gcp/gce"
	gcppubsub "github.com/devopsfactory-io/jit-runners/lambda/internal/gcp/pubsub"
	gcpsecret "github.com/devopsfactory-io/jit-runners/lambda/internal/gcp/secretmanager"
)

// newGCP builds a GCP-backed Bundle from environment variables and Application
// Default Credentials.
//
// Required env vars:
//   - GCP_PROJECT
//   - PUBSUB_JOBS_TOPIC          (full resource: projects/<p>/topics/<t>)
//   - PUBSUB_LIFECYCLE_TOPIC     (full resource)
//   - FIRESTORE_DATABASE         (typically "(default)")
//   - FIRESTORE_COLLECTION       (typically "runners")
//   - RUNNER_NETWORK, RUNNER_SUBNET, RUNNER_IMAGE, RUNNER_SA_EMAIL, RUNNER_ZONE
func newGCP(ctx context.Context) (*Bundle, error) {
	project := os.Getenv("GCP_PROJECT")
	if project == "" {
		return nil, fmt.Errorf("provider/gcp: GCP_PROJECT is required")
	}

	psClient, err := pubsubclient.NewClient(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("provider/gcp: pubsub.NewClient: %w", err)
	}
	jobsPub := gcppubsub.NewPublisher(psClient, os.Getenv("PUBSUB_JOBS_TOPIC"))
	lifecyclePub := gcppubsub.NewPublisher(psClient, os.Getenv("PUBSUB_LIFECYCLE_TOPIC"))

	fsClient, err := cf.NewClientWithDatabase(ctx, project, os.Getenv("FIRESTORE_DATABASE"))
	if err != nil {
		closeErr := psClient.Close()
		return nil, errors.Join(fmt.Errorf("provider/gcp: firestore.NewClientWithDatabase: %w", err), closeErr)
	}
	store := gcpfirestore.NewStore(fsClient, os.Getenv("FIRESTORE_COLLECTION"))

	smc, err := smclient.NewClient(ctx)
	if err != nil {
		closeErr := errors.Join(psClient.Close(), fsClient.Close())
		return nil, errors.Join(fmt.Errorf("provider/gcp: secretmanager.NewClient: %w", err), closeErr)
	}
	secLoader := gcpsecret.New(smc)

	insClient, err := gcpcompute.NewInstancesRESTClient(ctx)
	if err != nil {
		closeErr := errors.Join(psClient.Close(), fsClient.Close(), smc.Close())
		return nil, errors.Join(fmt.Errorf("provider/gcp: compute.NewInstancesRESTClient: %w", err), closeErr)
	}
	launcher := gce.NewLauncher(insClient, gce.LauncherOptions{
		Network:        os.Getenv("RUNNER_NETWORK"),
		Subnet:         os.Getenv("RUNNER_SUBNET"),
		Image:          os.Getenv("RUNNER_IMAGE"),
		ServiceAccount: os.Getenv("RUNNER_SA_EMAIL"),
		Project:        project,
		Zone:           os.Getenv("RUNNER_ZONE"),
	})

	closeFn := func() error {
		// Stop publishers first to drain in-flight messages and terminate the
		// SDK's background goroutines before the underlying client closes.
		if jobsPub != nil {
			jobsPub.Stop()
		}
		if lifecyclePub != nil {
			lifecyclePub.Stop()
		}
		return errors.Join(
			psClient.Close(),
			fsClient.Close(),
			smc.Close(),
			insClient.Close(),
		)
	}

	return &Bundle{
		JobsPublisher:      jobsPub,
		LifecyclePublisher: lifecyclePub,
		State:              store,
		Compute:            launcher,
		Secrets:            secLoader,
		CloseFn:            closeFn,
	}, nil
}
