// Package main is the rebalancer Lambda entry point.
//
// EventBridge fires this Lambda every 1 minute. The handler instantiates
// the GitHub client, DDB store, and SQS publisher; computes the
// per-label-set (demand - supply) gap; and publishes ScaleUpMessage with
// Source=SourceRebalancer for each missing slot. See the design spec at
// repositories/zettelkasten/Projects/jit-runners/specs/2026-05-02-effective-scaleup-design.md.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awssqssdk "github.com/aws/aws-sdk-go-v2/service/sqs"

	awsdynamo "github.com/devopsfactory-io/jit-runners/lambda/internal/aws/dynamo"
	awssqs "github.com/devopsfactory-io/jit-runners/lambda/internal/aws/sqs"
	appconfig "github.com/devopsfactory-io/jit-runners/lambda/internal/config"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/github"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/rebalancer"
)

var (
	cfgOnce sync.Once
	appCfg  *appconfig.Config
	cfgErr  error
)

func main() {
	lambda.Start(handler)
}

// handler is invoked by EventBridge with no payload. We ignore the payload
// and run a single rebalance cycle. Errors are logged but the handler
// returns nil so EventBridge does not retry-storm; the next 1-minute cycle
// re-attempts with a fresh rate budget.
func handler(ctx context.Context) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	store := awsdynamo.NewStore(dynamodb.NewFromConfig(awsCfg), cfg.TableName)
	publisher := awssqs.NewPublisher(awssqssdk.NewFromConfig(awsCfg), cfg.QueueURL)

	ghClient, err := newGitHubClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("github client: %w", err)
	}

	repos, err := ghClient.ListInstallationRepositories(ctx)
	if err != nil {
		log.Printf("rebalancer: list installation repositories: %v", err)
		// Return nil so EventBridge does not retry-storm. Next cycle (1 min)
		// re-attempts.
		return nil
	}

	var errCount int
	for _, repo := range repos {
		if err := rebalancer.Rebalance(ctx, ghClient, store, publisher, repo, cfg.InstallationID); err != nil {
			log.Printf("rebalancer: cycle error repo=%s: %v", repo, err)
			errCount++
		}
	}
	log.Printf("rebalancer: tick complete repos=%d errors=%d", len(repos), errCount)
	return nil
}

// newGitHubClient mints a fresh installation token and returns a real
// *github.Client. Mirrors the pattern in cmd/scaledown/main.go.
func newGitHubClient(ctx context.Context, cfg *appconfig.Config) (*github.Client, error) {
	if cfg.InstallationID == 0 {
		return nil, fmt.Errorf("rebalancer: GITHUB_INSTALLATION_ID is required")
	}
	token, err := github.InstallationToken(ctx, cfg.AppID, cfg.PrivateKey, cfg.InstallationID)
	if err != nil {
		return nil, fmt.Errorf("mint installation token: %w", err)
	}
	return github.NewClient(token), nil
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
