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

	// Mint a real installation token at sweep start when GITHUB_INSTALLATION_ID
	// is set so DeregisterRunner calls in terminateAndDeregister actually
	// succeed. The Cleaner does not loop tokens per-record because (a) the
	// token has a 60-min validity which is far longer than any sweep, and
	// (b) DDB records do not carry a per-row installation_id (scaleup
	// derives it from the SQS message; persistence is a Phase G concern).
	// When InstallationID is unset we fall back to a tokenless client; the
	// cleanup path already logs-and-continues on deregister failure so the
	// sweep still terminates EC2 instances and updates DDB.
	ghClient, err := newGitHubClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("github client: %w", err)
	}

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

// newGitHubClient builds a *github.Client carrying a fresh installation
// access token when cfg.InstallationID is set. When unset it returns a
// tokenless client and logs a warning — DeregisterRunner will then 401,
// but the cleanup path tolerates that (logs and continues).
func newGitHubClient(ctx context.Context, cfg *appconfig.Config) (*github.Client, error) {
	if cfg.InstallationID == 0 {
		log.Printf("GITHUB_INSTALLATION_ID is unset: DeregisterRunner calls will 401; cleanup will still terminate EC2 + update DDB")
		return github.NewClient(""), nil
	}
	token, err := github.InstallationToken(ctx, cfg.AppID, cfg.PrivateKey, cfg.InstallationID)
	if err != nil {
		return nil, fmt.Errorf("get installation token: %w", err)
	}
	return github.NewClient(token), nil
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
