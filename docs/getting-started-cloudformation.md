# Getting Started with CloudFormation

## Overview
This guide walks through deploying jit-runners using the CloudFormation template in `infra/cloudformation/template.yaml`.

## Prerequisites
- AWS account with permissions to create Lambda, API Gateway, SQS, DynamoDB, EC2, IAM, EventBridge, and CloudWatch resources
- AWS CLI installed and configured with credentials
- A GitHub App configured for jit-runners (see [GitHub App Setup](github-app-setup.md))
- Two secrets stored in AWS Secrets Manager (webhook secret and private key -- see [GitHub App Setup](github-app-setup.md#5-store-secrets-in-aws-secrets-manager))
- An S3 bucket for Lambda deployment packages

## 1. Build Lambda Binaries

Build the three Lambda functions from source:

```bash
make lambda.build
```

This produces three zip files in `lambda/dist/`:
- `webhook.zip`
- `scaleup.zip`
- `scaledown.zip`

Alternatively, download pre-built binaries from a [GitHub Release](https://github.com/devopsfactory-io/jit-runners/releases).

## 2. Upload Lambda Packages to S3

```bash
export LAMBDA_BUCKET="your-lambda-bucket"
export VERSION="v0.1.0"

aws s3 cp lambda/dist/webhook.zip "s3://${LAMBDA_BUCKET}/jit-runners/${VERSION}/webhook.zip"
aws s3 cp lambda/dist/scaleup.zip "s3://${LAMBDA_BUCKET}/jit-runners/${VERSION}/scaleup.zip"
aws s3 cp lambda/dist/scaledown.zip "s3://${LAMBDA_BUCKET}/jit-runners/${VERSION}/scaledown.zip"
```

## 3. Deploy the Stack

```bash
aws cloudformation deploy \
  --template-file infra/cloudformation/template.yaml \
  --stack-name jit-runners \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides \
    GitHubAppId="123456" \
    LambdaS3Bucket="${LAMBDA_BUCKET}" \
    WebhookLambdaS3Key="jit-runners/${VERSION}/webhook.zip" \
    ScaleUpLambdaS3Key="jit-runners/${VERSION}/scaleup.zip" \
    ScaleDownLambdaS3Key="jit-runners/${VERSION}/scaledown.zip" \
    WebhookSecretArn="arn:aws:secretsmanager:us-east-1:123456789012:secret:jit-runners/github-webhook-secret-AbCdEf" \
    PrivateKeySecretArn="arn:aws:secretsmanager:us-east-1:123456789012:secret:jit-runners/github-app-private-key-GhIjKl" \
    VpcId="vpc-0123456789abcdef0" \
    SubnetIds="subnet-aaa,subnet-bbb" \
    DefaultAMI="ami-0123456789abcdef0"
```

**Note**: `--capabilities CAPABILITY_NAMED_IAM` is required because the template creates named IAM roles.

### Optional Parameters

Add these to `--parameter-overrides` if needed:

- `LabelMappings='[{"label":"large","instance_type":"c5.2xlarge"}]'` -- Map workflow labels to instance types
- `StaleThresholdMinutes=10` -- Minutes before a pending runner is considered stale (default: 10)
- `MaxRunnerAgeMinutes=360` -- Maximum age before force-termination (default: 360)

## 4. Get the Webhook URL

```bash
aws cloudformation describe-stacks \
  --stack-name jit-runners \
  --query 'Stacks[0].Outputs[?OutputKey==`WebhookUrl`].OutputValue' \
  --output text
```

## 5. Set the Webhook URL

Go to your GitHub App settings and set the **Webhook URL** to the value from step 4. It will look like:

```
https://xxxxxxxxxx.execute-api.us-east-1.amazonaws.com/webhook
```

## 6. Test the Setup

1. Create a workflow in a repository where the GitHub App is installed:

```yaml
name: test-jit-runner
on: workflow_dispatch

jobs:
  test:
    runs-on: [self-hosted, linux, x64]
    steps:
      - run: echo "Hello from jit-runner!"
      - run: uname -a
```

2. Trigger the workflow manually from the Actions tab
3. Watch the Lambda logs to confirm it's working:

```bash
aws logs tail /aws/lambda/jit-runners-webhook --follow
aws logs tail /aws/lambda/jit-runners-scaleup --follow
```

## 7. View All Stack Outputs

```bash
aws cloudformation describe-stacks \
  --stack-name jit-runners \
  --query 'Stacks[0].Outputs' \
  --output table
```

Available outputs:
| Output | Description |
|--------|-------------|
| WebhookUrl | API Gateway endpoint for GitHub webhooks |
| WebhookLambdaArn | ARN of the webhook Lambda function |
| ScaleUpLambdaArn | ARN of the scale-up Lambda function |
| ScaleDownLambdaArn | ARN of the scale-down Lambda function |
| DynamoDBTableName | DynamoDB table for runner state |
| SQSQueueUrl | SQS queue URL for scale-up messages |
| RunnerSecurityGroupId | Security group ID for runner EC2 instances |
| RunnerInstanceProfileName | IAM instance profile for runner EC2 instances |

## Updating Lambda Functions

When a new version is released:

1. Build or download the new Lambda zips
2. Upload to S3 with the new version prefix
3. Update the stack with the new S3 keys:

```bash
export NEW_VERSION="v0.2.0"

aws cloudformation deploy \
  --template-file infra/cloudformation/template.yaml \
  --stack-name jit-runners \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides \
    WebhookLambdaS3Key="jit-runners/${NEW_VERSION}/webhook.zip" \
    ScaleUpLambdaS3Key="jit-runners/${NEW_VERSION}/scaleup.zip" \
    ScaleDownLambdaS3Key="jit-runners/${NEW_VERSION}/scaledown.zip"
```

**Note**: Parameters not specified in `--parameter-overrides` retain their previous values.

## Deleting the Stack

```bash
aws cloudformation delete-stack --stack-name jit-runners
aws cloudformation wait stack-delete-complete --stack-name jit-runners
```

This terminates all managed resources including any running EC2 instances.

## Troubleshooting

### Stack creation fails with IAM error
Ensure you include `--capabilities CAPABILITY_NAMED_IAM` in the deploy command.

### Lambda function errors
Check CloudWatch logs:
```bash
aws logs tail /aws/lambda/jit-runners-webhook --follow
aws logs tail /aws/lambda/jit-runners-scaleup --follow
aws logs tail /aws/lambda/jit-runners-scaledown --follow
```

### EC2 instances not launching
- Verify the VPC ID and subnet IDs are correct and the subnets have internet access (NAT Gateway for private subnets)
- Check the AMI ID is valid in your region
- Verify the security group allows outbound HTTPS (port 443) -- the template configures this by default

## Next Steps

- [GitHub App Setup](github-app-setup.md) -- if you haven't set up the GitHub App yet
- [Getting Started with Terraform](getting-started-terraform.md) -- alternative deployment method
