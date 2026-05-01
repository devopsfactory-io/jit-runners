package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	appconfig "github.com/devopsfactory-io/jit-runners/lambda/internal/config"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/ec2"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/github"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/runner"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/sqs"
)

var (
	cfgOnce sync.Once
	appCfg  *appconfig.Config
	cfgErr  error
)

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	store := runner.NewStore(dynamodb.NewFromConfig(awsCfg), cfg.TableName)
	launcher := ec2.NewLauncher(awsec2.NewFromConfig(awsCfg))
	publisher := sqs.NewPublisher(awssqs.NewFromConfig(awsCfg), cfg.QueueURL)

	// Phase E uses a tokenless GitHub client. DeregisterRunner is a no-op
	// when the record's GitHubRunnerID is zero; non-zero IDs will fail with
	// 401 and the cleanup path tolerates that (logged, not returned). Phase
	// F will wire a real installation token resolver once runner records
	// carry the InstallationID.
	ghClient := github.NewClient("")

	staleMinutes := envInt("STALE_THRESHOLD_MINUTES", 10)
	maxAgeMinutes := envInt("MAX_RUNNER_AGE_MINUTES", 360)
	maxReEnqueueAttempts := envInt("MAX_RE_ENQUEUE_ATTEMPTS", 3)

	cleaner := runner.NewCleaner(store, launcher, ghClient, publisher,
		staleMinutes, maxAgeMinutes, maxReEnqueueAttempts)
	result, err := cleaner.Run(ctx)
	if err != nil {
		return fmt.Errorf("cleanup: %w", err)
	}

	log.Printf("cleanup complete: stale=%d orphans=%d errors=%d",
		result.Stale, result.Orphans, result.Errors)
	return nil
}

func loadConfig(ctx context.Context) (*appconfig.Config, error) {
	cfgOnce.Do(func() {
		appCfg, cfgErr = appconfig.Load(ctx)
	})
	return appCfg, cfgErr
}

func envInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

func init() {
	log.SetFlags(log.Lshortfile)
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		log.SetOutput(os.Stdout)
	}
}
