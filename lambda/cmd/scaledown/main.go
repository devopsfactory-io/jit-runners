package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"

	funcframework "github.com/GoogleCloudPlatform/functions-framework-go/funcframework"
	"github.com/aws/aws-lambda-go/lambda"

	appconfig "github.com/devopsfactory-io/jit-runners/lambda/internal/config"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/github"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/provider"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/runner"
)

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
		funcframework.RegisterHTTPFunction("/", gcpHTTPHandler)
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

func handler(ctx context.Context) error {
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
	orphanGraceMinutes := envInt("ORPHAN_GRACE_MINUTES", 5)

	cleaner := runner.NewCleaner(bundleRef.State, bundleRef.Compute, ghClient, bundleRef.JobsPublisher,
		staleMinutes, maxAgeMinutes, maxReEnqueueAttempts, orphanGraceMinutes)
	result, err := cleaner.Run(ctx)
	if err != nil {
		return fmt.Errorf("cleanup: %w", err)
	}

	log.Printf("cleanup complete: stale=%d orphans=%d ec2_orphans=%d errors=%d", //nolint:gosec // G706: counters are internal cleanup-result fields from runner.Cleaner — not user input
		result.Stale, result.Orphans, result.EC2Orphans, result.Errors)
	return nil
}

// gcpHTTPHandler is the GCP Cloud Scheduler entry point. Cloud Scheduler
// invokes this function via HTTP with no meaningful payload; we run the same
// cleanup logic as the AWS handler path.
func gcpHTTPHandler(w http.ResponseWriter, r *http.Request) {
	if err := handler(r.Context()); err != nil {
		log.Printf("scaledown gcpHTTPHandler: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
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
