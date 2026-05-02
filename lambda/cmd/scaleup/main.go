package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awsec2sdk "github.com/aws/aws-sdk-go-v2/service/ec2"
	awssdkssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/google/uuid"

	awsdynamo "github.com/devopsfactory-io/jit-runners/lambda/internal/aws/dynamo"
	awsec2 "github.com/devopsfactory-io/jit-runners/lambda/internal/aws/ec2"
	awsssm "github.com/devopsfactory-io/jit-runners/lambda/internal/aws/ssm"
	awssqs "github.com/devopsfactory-io/jit-runners/lambda/internal/aws/sqs"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/compute"
	appconfig "github.com/devopsfactory-io/jit-runners/lambda/internal/config"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/github"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/runner"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/webhook"
)

const defaultRunnerVersion = "2.332.0"

var (
	cfgOnce sync.Once
	appCfg  *appconfig.Config
	cfgErr  error

	ssmLoaderOnce sync.Once
	ssmLoaderRef  *awsssm.Loader
)

const (
	runnerLogLevelParam   = "/jit-runners/runner-log-level"
	runnerLogLevelDefault = "info"
	ssmCacheTTL           = 30 * time.Second
)

func main() {
	lambda.Start(handler)
}

// TODO(phase B): replace direct AWS wiring with provider.New(provider.AWS|GCP).
func handler(ctx context.Context, sqsEvent events.SQSEvent) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	ssmLoader := getSSMLoader(awsCfg)

	launcher := awsec2.NewLauncher(awsec2sdk.NewFromConfig(awsCfg), awsec2.LauncherOptions{
		SecurityGroupID:    cfg.SecurityGroupID,
		IAMInstanceProfile: cfg.IAMInstanceProfile,
	})
	store := awsdynamo.NewStore(dynamodb.NewFromConfig(awsCfg), cfg.TableName)

	for _, record := range sqsEvent.Records {
		if err := processRecord(ctx, cfg, launcher, store, ssmLoader, record); err != nil {
			log.Printf("error processing record %s: %v", record.MessageId, err)
			return err
		}
	}
	return nil
}

func processRecord(ctx context.Context, cfg *appconfig.Config, launcher compute.Launcher, store state.RunnerStore, ssmLoader *awsssm.Loader, record events.SQSMessage) error {
	msg, err := awssqs.ParseMessage(record.Body)
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
	runnerName := "jit-" + uuid.NewString()
	customLabels := webhook.CustomLabels(msg.Labels)
	jitCfg, err := ghClient.GenerateJITConfig(ctx, msg.RepositoryFull, runnerName, msg.Labels)
	if err != nil {
		return fmt.Errorf("generate JIT config: %w", err)
	}

	runnerLogLevel, _ := ssmLoader.Get(ctx, runnerLogLevelParam, runnerLogLevelDefault)

	// Persist a pending record keyed on the GitHub runner_id BEFORE
	// launching the EC2 instance so scaledown's stale-pending sweep can
	// reap orphans if scaleup itself dies between RunInstances and the
	// post-launch update. The record carries jitCfg.Runner.ID as both the
	// primary key and the GitHubRunnerID attribute. JobID and
	// WorkflowRunID are observability metadata, never lookup keys.
	pending := runner.New(msg.RepositoryFull, jitCfg.Runner.ID, "", msg.JobID, msg.RunID, msg.Labels)
	pending.ReEnqueueAttempts = msg.ReEnqueueAttempts
	if err := writePendingRecord(ctx, ghClient, store, msg, pending); err != nil {
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
		RunnerVersion:  runnerVersion,
		JITConfig:      jitCfg.EncodedJIT,
		RunnerID:       jitCfg.Runner.ID,
		RunnerLogLevel: runnerLogLevel,
	})
	if err != nil {
		markLaunchFailed(ctx, ghClient, store, msg, pending.ID, jitCfg.Runner.ID)
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

	inst, err := launcher.Launch(ctx, spec)
	if err != nil {
		markLaunchFailed(ctx, ghClient, store, msg, pending.ID, jitCfg.Runner.ID)
		return fmt.Errorf("launch instance: %w", err)
	}

	// Launch succeeded — bind the instance ID and bump LastAttemptAt on the
	// pending record. The record stays in StatusPending; the runner agent
	// on the instance will flip it to running via the lifecycle pipeline.
	now := time.Now()
	if err := store.Update(ctx, pending.ID, state.RunnerUpdate{
		InstanceID:    &inst.ID,
		LastAttemptAt: &now,
	}); err != nil {
		// The instance is launched and the runner is registered — log and
		// continue so the message is ack'd. Scaledown will reconcile if
		// anything goes wrong from here.
		log.Printf("failed to update runner record with instance %s for runner=%d job=%d: %v",
			inst.ID, jitCfg.Runner.ID, msg.JobID, err)
	}

	log.Printf("launched instance %s for runner=%d job=%d (%s)",
		inst.ID, jitCfg.Runner.ID, msg.JobID, msg.RepositoryFull)
	return nil
}

// writePendingRecord persists pending as the pre-launch state. Always a
// fresh Put — the partition key is the just-returned GitHub runner_id and
// no prior record can exist at that key. On Put failure we deregister the
// GitHub runner so we don't leak registration state.
func writePendingRecord(ctx context.Context, gh *github.Client, store state.RunnerStore, msg *awssqs.ScaleUpMessage, pending state.Runner) error {
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
func markLaunchFailed(ctx context.Context, gh *github.Client, store state.RunnerStore, msg *awssqs.ScaleUpMessage, recordID string, ghRunnerID int64) {
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

func getSSMLoader(awsCfg aws.Config) *awsssm.Loader {
	ssmLoaderOnce.Do(func() {
		ssmLoaderRef = awsssm.New(awssdkssm.NewFromConfig(awsCfg), ssmCacheTTL)
	})
	return ssmLoaderRef
}

func init() {
	log.SetFlags(log.Lshortfile)
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		log.SetOutput(os.Stdout)
	}
}
