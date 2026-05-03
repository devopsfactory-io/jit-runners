// Package main is the rebalancer Lambda entry point.
//
// EventBridge fires this Lambda every 1 minute. The handler instantiates
// the GitHub client, provider bundle (DDB store + SQS publisher); computes
// the per-label-set (demand - supply) gap; and publishes ScaleUpMessage with
// Source=SourceRebalancer for each missing slot. See the design spec at
// repositories/zettelkasten/Projects/jit-runners/specs/2026-05-02-effective-scaleup-design.md.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	funcframework "github.com/GoogleCloudPlatform/functions-framework-go/funcframework"

	appconfig "github.com/devopsfactory-io/jit-runners/lambda/internal/config"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/github"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/provider"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/rebalancer"
)

var (
	cfgOnce sync.Once
	appCfg  *appconfig.Config
	cfgErr  error

	bundleOnce sync.Once
	bundleRef  *provider.Bundle
	bundleErr  error
)

// activityWindow scopes the rebalancer's per-cycle work to repos that
// have at least one runner record launched in the past N. Drift recovery
// requires that scaleup already attempted to launch (which writes a
// record), so a repo with no recent record cannot have stranded queued
// jobs in our system. 7 days is a generous default; tune via env later
// if needed.
const activityWindow = 7 * 24 * time.Hour

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

// handler is invoked by EventBridge with no payload. We ignore the payload
// and run a single rebalance cycle. Errors are logged but the handler
// returns nil so EventBridge does not retry-storm; the next 1-minute cycle
// re-attempts with a fresh rate budget.
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

	ghClient, err := newGitHubClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("github client: %w", err)
	}

	since := time.Now().Add(-activityWindow)
	repos, err := bundleRef.State.ListActiveRepos(ctx, since)
	if err != nil {
		log.Printf("rebalancer: list active repos: %v", err)
		// Return nil so EventBridge does not retry-storm. Next cycle (1 min)
		// re-attempts.
		return nil
	}

	var errCount int
	for _, repo := range repos {
		if err := rebalancer.Rebalance(ctx, ghClient, bundleRef.State, bundleRef.JobsPublisher, repo, cfg.InstallationID); err != nil {
			log.Printf("rebalancer: cycle error repo=%s: %v", repo, err)
			errCount++
		}
	}
	log.Printf("rebalancer: tick complete repos=%d errors=%d", len(repos), errCount)
	return nil
}

// gcpHTTPHandler is the GCP Cloud Scheduler entry point. Cloud Scheduler
// invokes this function via HTTP with no meaningful payload; we run the same
// rebalance cycle as the AWS handler path.
func gcpHTTPHandler(w http.ResponseWriter, r *http.Request) {
	if err := handler(r.Context()); err != nil {
		log.Printf("rebalancer gcpHTTPHandler: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
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
