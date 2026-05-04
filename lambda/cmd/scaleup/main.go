package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	funcframework "github.com/GoogleCloudPlatform/functions-framework-go/funcframework"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"

	awsec2 "github.com/devopsfactory-io/jit-runners/lambda/internal/aws/ec2"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/compute"
	appconfig "github.com/devopsfactory-io/jit-runners/lambda/internal/config"
	gcpruntime "github.com/devopsfactory-io/jit-runners/lambda/internal/gcp/runtime"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/github"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/provider"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/queue"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/runner"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/webhook"
)

const defaultRunnerVersion = "2.332.0"

// queueLister abstracts the listing call for testability. Production code
// passes *github.Client which satisfies this interface.
type queueLister interface {
	ListQueuedWorkflowJobs(ctx context.Context, ownerRepo string) ([]github.QueuedJob, error)
}

var (
	cfgOnce sync.Once
	appCfg  *appconfig.Config
	cfgErr  error

	bundleOnce sync.Once
	bundleRef  *provider.Bundle
	bundleErr  error
)

func main() {
	if os.Getenv("CLOUD_PROVIDER") == "gcp" {
		ctx := context.Background()
		if err := funcframework.RegisterCloudEventFunctionContext(ctx, "/", gcpHandler); err != nil {
			log.Fatalf("funcframework.RegisterCloudEventFunctionContext: %v", err)
		}
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		if err := funcframework.Start(port); err != nil {
			log.Fatalf("funcframework.Start: %v", err)
		}
		return
	}
	lambda.Start(handler)
}

func handler(ctx context.Context, sqsEvent events.SQSEvent) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	bundleOnce.Do(func() {
		bundleRef, bundleErr = provider.New(ctx, os.Getenv("CLOUD_PROVIDER"))
	})
	if bundleErr != nil {
		return fmt.Errorf("provider.New: %w", bundleErr)
	}

	for _, record := range sqsEvent.Records {
		if err := processRecord(ctx, cfg, bundleRef, record); err != nil {
			log.Printf("error processing record %s: %v", record.MessageId, err)
			return err
		}
	}
	return nil
}

// gcpHandler is the GCP Eventarc / Pub/Sub CloudEvents entry point. Eventarc
// delivers exactly one Pub/Sub message per invocation; we decode the envelope
// and synthesize an events.SQSMessage so the existing processRecord logic can
// be reused without modification.
func gcpHandler(ctx context.Context, e cloudevents.Event) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	bundleOnce.Do(func() {
		bundleRef, bundleErr = provider.New(ctx, "gcp")
	})
	if bundleErr != nil {
		return fmt.Errorf("provider.New: %w", bundleErr)
	}

	body, err := gcpruntime.DecodePubSubData(e)
	if err != nil {
		return fmt.Errorf("decode cloudevent: %w", err)
	}

	record := events.SQSMessage{Body: string(body), MessageId: e.ID()}
	return processRecord(ctx, cfg, bundleRef, record)
}

func processRecord(ctx context.Context, cfg *appconfig.Config, b *provider.Bundle, record events.SQSMessage) error {
	msg, err := queue.ParseScaleUp([]byte(record.Body))
	if err != nil {
		log.Printf("parse SQS message: %v", err)
		return nil // don't retry malformed messages
	}

	// Get installation token.
	token, err := github.InstallationToken(ctx, cfg.AppID, cfg.PrivateKey, msg.InstallationID)
	if err != nil {
		return fmt.Errorf("get installation token: %w", err)
	}

	// Generate JIT runner config. The runner is registered with GitHub at
	// this point; if anything below fails we must deregister it. The runner
	// name is purely cosmetic per GitHub's JIT contract — we use a UUID so
	// no future reader assumes a binding to job_id or workflow_run_id.
	ghClient := github.NewClient(token)

	launch, err := shouldLaunch(ctx, ghClient, b.State, msg)
	if err != nil {
		return fmt.Errorf("scaleup decision: %w", err)
	}
	if !launch {
		log.Printf("scaleup: skip %s job=%d labels=%v: demand <= supply (Source=%q)",
			msg.RepositoryFull, msg.JobID, msg.Labels, msg.Source)
		return nil
	}

	runnerName := "jit-" + uuid.NewString()
	customLabels := webhook.CustomLabels(msg.Labels)
	jitCfg, err := ghClient.GenerateJITConfig(ctx, msg.RepositoryFull, runnerName, msg.Labels)
	if err != nil {
		return fmt.Errorf("generate JIT config: %w", err)
	}

	// Persist a pending record keyed on the GitHub runner_id BEFORE
	// launching the EC2 instance so scaledown's stale-pending sweep can
	// reap orphans if scaleup itself dies between RunInstances and the
	// post-launch update. The record carries jitCfg.Runner.ID as both the
	// primary key and the GitHubRunnerID attribute. JobID and
	// WorkflowRunID are observability metadata, never lookup keys.
	pending := runner.New(msg.RepositoryFull, jitCfg.Runner.ID, "", msg.JobID, msg.RunID, msg.Labels)
	pending.ReEnqueueAttempts = msg.ReEnqueueAttempts
	if err := writePendingRecord(ctx, ghClient, b.State, msg, pending); err != nil {
		return err
	}

	// Resolve instance type from labels.
	instanceType := resolveInstanceType(cfg, customLabels)

	// Generate user-data.
	runnerVersion := os.Getenv("RUNNER_VERSION")
	if runnerVersion == "" {
		runnerVersion = defaultRunnerVersion
	}
	userData, err := awsec2.GenerateUserData(&awsec2.UserDataParams{
		RunnerVersion: runnerVersion,
		JITConfig:     jitCfg.EncodedJIT,
		RunnerID:      jitCfg.Runner.ID,
	})
	if err != nil {
		markLaunchFailed(ctx, ghClient, b.State, msg, pending.ID, jitCfg.Runner.ID)
		return fmt.Errorf("generate user-data: %w", err)
	}

	// Resolve AMI and subnet.
	ami := resolveAMI(cfg, customLabels)
	subnetID := ""
	if len(cfg.SubnetIDs) > 0 {
		subnetID = cfg.SubnetIDs[0] // simple round-robin can be added later
	}

	spec := compute.LaunchSpec{
		Labels:       msg.Labels,
		InstanceType: instanceType,
		ImageID:      ami,
		SubnetID:     subnetID,
		UserData:     userData,
		RunnerID:     pending.ID,
	}

	inst, err := b.Compute.Launch(ctx, spec)
	if err != nil {
		markLaunchFailed(ctx, ghClient, b.State, msg, pending.ID, jitCfg.Runner.ID)
		return fmt.Errorf("launch instance: %w", err)
	}

	// Launch succeeded — bind the instance ID and bump LastAttemptAt on the
	// pending record. The record stays in StatusPending; the runner agent
	// on the instance will flip it to running via the lifecycle pipeline.
	if err := bindLaunchedInstance(ctx, b, pending.ID, inst.ID, jitCfg.Runner.ID, msg.JobID); err != nil {
		return err
	}

	log.Printf("launched instance %s for runner=%d job=%d (%s)",
		inst.ID, jitCfg.Runner.ID, msg.JobID, msg.RepositoryFull)
	return nil
}

// bindLaunchedInstance writes the freshly-launched instance ID onto the
// pending DDB row. On Update failure it best-effort terminates the EC2
// instance and returns the wrapped error so SQS redrives — the next
// attempt launches a fresh instance with a fresh DDB write. Without the
// defensive Terminate the row would have no InstanceID, leaving an
// orphan that survives until MaxRunnerAgeMinutes (issue #74 design D1.3).
func bindLaunchedInstance(ctx context.Context, b *provider.Bundle, recordID, instanceID string, ghRunnerID, jobID int64) error {
	now := time.Now()
	err := b.State.Update(ctx, recordID, state.RunnerUpdate{
		InstanceID:    &instanceID,
		LastAttemptAt: &now,
	})
	if err == nil {
		return nil
	}
	log.Printf("scaleup: update record after launch failed; terminating orphan instance %s for runner=%d job=%d: %v",
		instanceID, ghRunnerID, jobID, err)
	if termErr := b.Compute.Terminate(ctx, []string{instanceID}); termErr != nil {
		log.Printf("scaleup: terminate orphan instance %s failed: %v", instanceID, termErr)
	}
	return fmt.Errorf("update runner record after launch: %w", err)
}

// writePendingRecord persists pending as the pre-launch state. Always a
// fresh Put — the partition key is the just-returned GitHub runner_id and
// no prior record can exist at that key. On Put failure we deregister the
// GitHub runner so we don't leak registration state.
func writePendingRecord(ctx context.Context, gh *github.Client, store state.RunnerStore, msg *queue.ScaleUpMessage, pending state.Runner) error {
	if err := store.Put(ctx, pending); err != nil {
		if derr := gh.DeregisterRunner(ctx, msg.RepositoryFull, pending.GitHubRunnerID); derr != nil {
			log.Printf("deregister after pending-put failure for runner=%d job=%d: %v",
				pending.GitHubRunnerID, msg.JobID, derr)
		}
		return fmt.Errorf("put pending runner record: %w", err)
	}
	return nil
}

// markLaunchFailed deregisters the GitHub-side runner registration and
// marks the record failed after a post-registration error path. Errors are
// logged but not returned — the caller is already returning the launch
// error.
func markLaunchFailed(ctx context.Context, gh *github.Client, store state.RunnerStore, msg *queue.ScaleUpMessage, recordID string, ghRunnerID int64) {
	if err := gh.DeregisterRunner(ctx, msg.RepositoryFull, ghRunnerID); err != nil {
		log.Printf("deregister runner=%d after launch failure for %s job=%d: %v",
			ghRunnerID, msg.RepositoryFull, msg.JobID, err)
	}
	now := time.Now()
	failed := state.StatusFailed
	if err := store.Update(ctx, recordID, state.RunnerUpdate{
		Status:        &failed,
		LastAttemptAt: &now,
	}); err != nil {
		log.Printf("mark runner failed for runner=%d job=%d: %v",
			ghRunnerID, msg.JobID, err)
	}
}

func resolveInstanceType(cfg *appconfig.Config, labels []string) string {
	for _, mapping := range cfg.LabelMappings {
		for _, label := range labels {
			if label == mapping.Label {
				return mapping.InstanceType
			}
		}
	}
	return cfg.DefaultInstanceType
}

func resolveAMI(cfg *appconfig.Config, labels []string) string {
	for _, mapping := range cfg.LabelMappings {
		for _, label := range labels {
			if label == mapping.Label && mapping.AMI != "" {
				return mapping.AMI
			}
		}
	}
	return cfg.DefaultAMI
}

// shouldLaunch decides whether to proceed with the launch pipeline for a
// given ScaleUpMessage. Returns true if the message's Source warrants an
// unconditional launch (rebalancer) or if the webhook-path demand check
// passes (queued matching jobs > pending matching runners).
//
// "Matching" uses subset semantics: a pending runner whose labels are a
// superset of the queued job's labels is considered supply.
//
// On the webhook path, an empty Source is treated as SourceWebhook for
// backwards compat with in-flight messages at deploy time.
func shouldLaunch(ctx context.Context, gh queueLister, store state.RunnerStore, msg *queue.ScaleUpMessage) (bool, error) {
	if msg.Source == queue.SourceRebalancer {
		return true, nil
	}

	queued, err := gh.ListQueuedWorkflowJobs(ctx, msg.RepositoryFull)
	if err != nil {
		return false, fmt.Errorf("scaleup: list queued jobs: %w", err)
	}
	demand := 0
	for _, j := range queued {
		if state.MatchesLabels(msg.Labels, j.Labels) {
			demand++
		}
	}

	pending, err := store.List(ctx, state.Filter{StatusEq: state.StatusPending})
	if err != nil {
		return false, fmt.Errorf("scaleup: list pending runners: %w", err)
	}
	supply := 0
	for _, r := range pending {
		if state.MatchesLabels(r.Labels, msg.Labels) {
			supply++
		}
	}

	return demand > supply, nil
}

func loadConfig(ctx context.Context) (*appconfig.Config, error) {
	cfgOnce.Do(func() {
		appCfg, cfgErr = appconfig.Load(ctx)
	})
	return appCfg, cfgErr
}

func init() {
	log.SetFlags(log.Lshortfile)
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		log.SetOutput(os.Stdout)
	}
}
