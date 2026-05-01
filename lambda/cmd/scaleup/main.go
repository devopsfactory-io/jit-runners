package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"

	appconfig "github.com/devopsfactory-io/jit-runners/lambda/internal/config"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/ec2"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/github"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/runner"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/sqs"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/webhook"
)

const defaultRunnerVersion = "2.332.0"

var (
	cfgOnce sync.Once
	appCfg  *appconfig.Config
	cfgErr  error
)

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, sqsEvent events.SQSEvent) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	launcher := ec2.NewLauncher(awsec2.NewFromConfig(awsCfg))
	store := runner.NewStore(dynamodb.NewFromConfig(awsCfg), cfg.TableName)

	for _, record := range sqsEvent.Records {
		if err := processRecord(ctx, cfg, launcher, store, record); err != nil {
			log.Printf("error processing record %s: %v", record.MessageId, err)
			return err
		}
	}
	return nil
}

func processRecord(ctx context.Context, cfg *appconfig.Config, launcher *ec2.Launcher, store *runner.Store, record events.SQSMessage) error {
	msg, err := sqs.ParseMessage(record.Body)
	if err != nil {
		log.Printf("parse SQS message: %v", err)
		return nil // don't retry malformed messages
	}

	// Idempotency check. Skip only when an in-flight runner is already
	// registered and running. A pending record may be a partial write from a
	// prior attempt that died before Launch — we still want to retry. A failed
	// record (e.g. written by scaledown after a stale-pending reap) signals
	// the runner was deregistered and the message has been re-enqueued, so a
	// fresh registration + launch is appropriate.
	existing, err := store.Get(ctx, msg.RepositoryFull, msg.JobID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("check existing runner: %w", err)
	}
	if existing != nil && existing.Status == state.StatusRunning {
		log.Printf("runner already running for %s job=%d, skipping", msg.RepositoryFull, msg.JobID)
		return nil
	}

	// Get installation token.
	token, err := github.InstallationToken(ctx, cfg.AppID, cfg.PrivateKey, msg.InstallationID)
	if err != nil {
		return fmt.Errorf("get installation token: %w", err)
	}

	// Generate JIT runner config. Note the runner is registered with GitHub
	// at this point; if anything below fails we must deregister it.
	ghClient := github.NewClient(token)
	runnerName := fmt.Sprintf("jit-%d", msg.JobID)
	customLabels := webhook.CustomLabels(msg.Labels)
	jitCfg, err := ghClient.GenerateJITConfig(ctx, msg.RepositoryFull, runnerName, msg.Labels)
	if err != nil {
		return fmt.Errorf("generate JIT config: %w", err)
	}

	// Persist a pending DDB record BEFORE launching the EC2 instance so that
	// scaledown's stale-pending sweep can reap orphans if scaleup itself dies
	// between RunInstances and the post-launch update. The record carries the
	// GitHub runner ID (for deregistration) and the re-enqueue counter.
	rec := runner.NewRecord(msg.RepositoryFull, msg.JobID, msg.RunID, "", msg.Labels)
	rec.GitHubRunnerID = jitCfg.Runner.ID
	rec.ReEnqueueAttempts = msg.ReEnqueueAttempts
	if err := writePendingRecord(ctx, ghClient, store, msg, rec, existing != nil); err != nil {
		return err
	}

	// Resolve instance type from labels.
	instanceType := resolveInstanceType(cfg, customLabels)

	// Generate user-data.
	runnerVersion := os.Getenv("RUNNER_VERSION")
	if runnerVersion == "" {
		runnerVersion = defaultRunnerVersion
	}
	userData, err := ec2.GenerateUserData(&ec2.UserDataParams{
		RunnerVersion: runnerVersion,
		JITConfig:     jitCfg.EncodedJIT,
	})
	if err != nil {
		markLaunchFailed(ctx, ghClient, store, msg, rec.RunnerID, jitCfg.Runner.ID)
		return fmt.Errorf("generate user-data: %w", err)
	}

	// Resolve AMI and subnet.
	ami := resolveAMI(cfg, customLabels)
	subnetID := ""
	if len(cfg.SubnetIDs) > 0 {
		subnetID = cfg.SubnetIDs[0] // simple round-robin can be added later
	}

	launchCfg := &ec2.LaunchConfig{
		InstanceType:       instanceType,
		AMI:                ami,
		SubnetID:           subnetID,
		SecurityGroupID:    cfg.SecurityGroupID,
		IAMInstanceProfile: cfg.IAMInstanceProfile,
		Labels:             msg.Labels,
		UserData:           userData,
		Tags: map[string]string{
			"job-id":     fmt.Sprintf("%d", msg.JobID),
			"repository": msg.RepositoryFull,
		},
	}

	// Try spot first, fall back to on-demand.
	instanceID, err := launcher.Launch(ctx, launchCfg)
	if err != nil {
		log.Printf("spot launch failed for %s job=%d: %v, trying on-demand", msg.RepositoryFull, msg.JobID, err)
		instanceID, err = launcher.LaunchOnDemand(ctx, launchCfg)
		if err != nil {
			markLaunchFailed(ctx, ghClient, store, msg, rec.RunnerID, jitCfg.Runner.ID)
			return fmt.Errorf("launch EC2 instance (spot and on-demand both failed): %w", err)
		}
	}

	// Launch succeeded — bind the instance ID and bump LastAttemptAt on the
	// pending record. The record stays in StatusPending; the runner agent on
	// the instance will flip it to running via the lifecycle pipeline.
	now := time.Now()
	if err := store.Update(ctx, rec.RunnerID, state.RunnerUpdate{
		InstanceID:    &instanceID,
		LastAttemptAt: &now,
	}); err != nil {
		// The instance is launched and the runner is registered — log and
		// continue so the message is ack'd. Scaledown will reconcile via tags
		// if anything goes wrong from here.
		log.Printf("failed to update runner record with instance %s for %s job=%d: %v",
			instanceID, msg.RepositoryFull, msg.JobID, err)
	}

	log.Printf("launched instance %s for %s job=%d", instanceID, msg.RepositoryFull, msg.JobID)
	return nil
}

// writePendingRecord persists rec as the pending pre-launch state. If a
// non-running record already exists (recordExists=true), the writer Updates
// the existing row instead of Putting (which would conflict with the
// attribute_not_exists guard). Any failure deregisters the GitHub runner so
// we don't leak registration state.
func writePendingRecord(ctx context.Context, gh *github.Client, store *runner.Store, msg *sqs.ScaleUpMessage, rec *runner.Record, recordExists bool) error {
	if !recordExists {
		if err := store.Put(ctx, rec); err != nil {
			if derr := gh.DeregisterRunner(ctx, msg.RepositoryFull, rec.GitHubRunnerID); derr != nil {
				log.Printf("deregister after pending-put failure for %s job=%d: %v", msg.RepositoryFull, msg.JobID, derr)
			}
			return fmt.Errorf("put pending runner record: %w", err)
		}
		return nil
	}
	// Refresh the existing non-running record (typically pending or failed
	// from a prior attempt) so the GitHub runner ID and re-enqueue counter
	// reflect the current attempt before launch.
	now := time.Now()
	pending := state.StatusPending
	ghID := rec.GitHubRunnerID
	attempts := rec.ReEnqueueAttempts
	if err := store.Update(ctx, rec.RunnerID, state.RunnerUpdate{
		Status:            &pending,
		GitHubRunnerID:    &ghID,
		ReEnqueueAttempts: &attempts,
		LastAttemptAt:     &now,
	}); err != nil {
		if derr := gh.DeregisterRunner(ctx, msg.RepositoryFull, rec.GitHubRunnerID); derr != nil {
			log.Printf("deregister after pending-update failure for %s job=%d: %v", msg.RepositoryFull, msg.JobID, derr)
		}
		return fmt.Errorf("refresh pending runner record: %w", err)
	}
	return nil
}

// markLaunchFailed deregisters the GitHub-side runner registration and marks
// the DDB record failed after a post-registration error path. Errors are
// logged but not returned — the caller is already returning the launch error.
func markLaunchFailed(ctx context.Context, gh *github.Client, store *runner.Store, msg *sqs.ScaleUpMessage, recordID string, runnerID int64) {
	if err := gh.DeregisterRunner(ctx, msg.RepositoryFull, runnerID); err != nil {
		log.Printf("deregister runner %d after launch failure for %s job=%d: %v",
			runnerID, msg.RepositoryFull, msg.JobID, err)
	}
	now := time.Now()
	failed := state.StatusFailed
	if err := store.Update(ctx, recordID, state.RunnerUpdate{
		Status:        &failed,
		LastAttemptAt: &now,
	}); err != nil {
		log.Printf("mark runner failed for %s job=%d: %v", msg.RepositoryFull, msg.JobID, err)
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
	return "t3.large" // default instance type
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
