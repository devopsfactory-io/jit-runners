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

	appconfig "github.com/devopsfactory-io/jit-runners/lambda/internal/config"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/ec2"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/runner"
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

	staleMinutes := envInt("STALE_THRESHOLD_MINUTES", 10)
	maxAgeMinutes := envInt("MAX_RUNNER_AGE_MINUTES", 360)

	cleaner := runner.NewCleaner(store, launcher, staleMinutes, maxAgeMinutes)
	result, err := cleaner.Run(ctx)
	if err != nil {
		return fmt.Errorf("cleanup: %w", err)
	}

	log.Printf("cleanup complete: stale=%d orphans=%d errors=%d",
		result.StaleTerminated, result.OrphanTerminated, result.Errors)
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
