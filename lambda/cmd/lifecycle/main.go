// Package main is the lifecycle Lambda entry point. It consumes lifecycle
// SQS messages (workflow_job in_progress / completed) and applies the
// state-machine transition + GitHub deregister side-effect.
//
// Construction mirrors cmd/scaledown and cmd/scaleup: load config, build
// the provider bundle via provider.New, and mint an installation token at
// startup for DeregisterRunner.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	appconfig "github.com/devopsfactory-io/jit-runners/lambda/internal/config"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/github"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/lifecycle"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/provider"
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
	lambda.Start(handler)
}

func handler(ctx context.Context, ev events.SQSEvent) error {
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

	// Mint a real installation token when GITHUB_INSTALLATION_ID is set.
	// Without it we fall back to a tokenless client and DeregisterRunner
	// calls will 401, but the lifecycle DDB transitions still run — the
	// handler logs and ignores deregister failures by design (DDB commit
	// is the source of truth; deregister is best-effort cleanup).
	ghClient, err := newGitHubClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("github client: %w", err)
	}

	h := lifecycle.New(bundleRef.State, ghClient, log.Default())

	for _, rec := range ev.Records {
		if err := h.HandleSQS(ctx, []byte(rec.Body)); err != nil {
			return err
		}
	}
	return nil
}

// newGitHubClient builds a *github.Client. When cfg.InstallationID is set
// the client carries a fresh installation access token (mirrors the
// scaleup flow). When unset we log a warning and return a tokenless
// client — production should always set GITHUB_INSTALLATION_ID; the
// fallback exists so a misconfigured Lambda cold-start does not crash and
// continues to commit lifecycle transitions to DynamoDB.
func newGitHubClient(ctx context.Context, cfg *appconfig.Config) (*github.Client, error) {
	if cfg.InstallationID == 0 {
		log.Printf("GITHUB_INSTALLATION_ID is unset: DeregisterRunner calls will 401; lifecycle DDB transitions will still run")
		return github.NewClient(""), nil
	}
	token, err := github.InstallationToken(ctx, cfg.AppID, cfg.PrivateKey, cfg.InstallationID)
	if err != nil {
		return nil, fmt.Errorf("get installation token: %w", err)
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
