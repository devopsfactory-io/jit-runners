package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"

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
	"github.com/devopsfactory-io/jit-runners/lambda/internal/webhook"
)

const defaultRunnerVersion = "2.321.0"

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

	// Idempotency check.
	existing, err := store.Get(ctx, msg.RepositoryFull, msg.JobID)
	if err != nil {
		return fmt.Errorf("check existing runner: %w", err)
	}
	if existing != nil {
		log.Printf("runner already exists for %s job=%d, skipping", msg.RepositoryFull, msg.JobID)
		return nil
	}

	// Get installation token.
	token, err := github.InstallationToken(ctx, cfg.AppID, cfg.PrivateKey, msg.InstallationID)
	if err != nil {
		return fmt.Errorf("get installation token: %w", err)
	}

	// Generate JIT runner config.
	ghClient := github.NewClient(token)
	runnerName := fmt.Sprintf("jit-%d", msg.JobID)
	customLabels := webhook.CustomLabels(msg.Labels)
	jitCfg, err := ghClient.GenerateJITConfig(ctx, msg.RepositoryFull, runnerName, customLabels)
	if err != nil {
		return fmt.Errorf("generate JIT config: %w", err)
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
			return fmt.Errorf("launch EC2 instance (spot and on-demand both failed): %w", err)
		}
	}

	// Record runner state.
	rec := runner.NewRecord(msg.RepositoryFull, msg.JobID, msg.RunID, instanceID, msg.Labels)
	if err := store.Put(ctx, rec); err != nil {
		log.Printf("failed to record runner state (instance %s already launched): %v", instanceID, err)
	}

	log.Printf("launched instance %s for %s job=%d", instanceID, msg.RepositoryFull, msg.JobID)
	return nil
}

func resolveInstanceType(cfg *appconfig.Config, labels []string) string {
	for _, mapping := range cfg.LabelMappings {
		for _, label := range labels {
			if label == mapping.Label {
				return mapping.InstanceType
			}
		}
	}
	return "t3.medium" // default instance type
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
