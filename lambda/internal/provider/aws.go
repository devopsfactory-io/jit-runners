package provider

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awsec2sdk "github.com/aws/aws-sdk-go-v2/service/ec2"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	awssqssdk "github.com/aws/aws-sdk-go-v2/service/sqs"

	awsdynamo "github.com/devopsfactory-io/jit-runners/lambda/internal/aws/dynamo"
	awsec2 "github.com/devopsfactory-io/jit-runners/lambda/internal/aws/ec2"
	awssecrets "github.com/devopsfactory-io/jit-runners/lambda/internal/aws/secretsmanager"
	awssqs "github.com/devopsfactory-io/jit-runners/lambda/internal/aws/sqs"
)

// newAWS builds an AWS-backed Bundle from environment variables and the
// default AWS config chain. Required env vars (asserted at this layer):
//
//   - SQS_QUEUE_URL              — the scaleup queue (jobs)
//   - LIFECYCLE_QUEUE_URL        — the lifecycle queue
//   - DYNAMODB_TABLE_NAME        — the runner state table
//
// Optional:
//   - EC2_SECURITY_GROUP_ID, EC2_IAM_INSTANCE_PROFILE — used by Compute.
//
// Lambdas that don't need a particular field (e.g. webhook never reads
// state) leave those AWS clients constructed but unused; this is cheap
// and keeps the factory uniform.
func newAWS(ctx context.Context) (*Bundle, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("provider/aws: load config: %w", err)
	}

	jobsURL := os.Getenv("SQS_QUEUE_URL")
	lifecycleURL := os.Getenv("LIFECYCLE_QUEUE_URL")
	tableName := os.Getenv("DYNAMODB_TABLE_NAME")

	sqsClient := awssqssdk.NewFromConfig(awsCfg)
	jobsPub := awssqs.NewPublisher(sqsClient, jobsURL)
	lifecyclePub := awssqs.NewLifecyclePublisher(sqsClient, lifecycleURL)
	store := awsdynamo.NewStore(dynamodb.NewFromConfig(awsCfg), tableName)

	cpuCredits := os.Getenv("EC2_CPU_CREDITS")
	if cpuCredits == "" {
		cpuCredits = "standard"
	}
	launcher := awsec2.NewLauncher(awsec2sdk.NewFromConfig(awsCfg), awsec2.LauncherOptions{
		SecurityGroupID:    os.Getenv("EC2_SECURITY_GROUP_ID"),
		IAMInstanceProfile: os.Getenv("EC2_IAM_INSTANCE_PROFILE"),
		CPUCredits:         cpuCredits,
	})

	secLoader := awssecrets.New(awssm.NewFromConfig(awsCfg))

	return &Bundle{
		JobsPublisher:      jobsPub,
		LifecyclePublisher: lifecyclePub,
		State:              store,
		Compute:            launcher,
		Secrets:            secLoader,
	}, nil
}
