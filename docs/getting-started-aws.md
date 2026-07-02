# Getting Started on AWS

This guide walks through deploying jit-runners on AWS using either OpenTofu/Terraform or CloudFormation. The two IaC tools deploy identical infrastructure; pick the one matching your existing toolchain.

> **For GCP**: see [Getting Started on GCP](getting-started-gcp.md).

## Prerequisites

- AWS account with permissions to create Lambda, API Gateway, SQS, DynamoDB, EC2, IAM, EventBridge, and Secrets Manager resources.
- AWS CLI installed and configured with credentials.
- A GitHub App configured for jit-runners (see [GitHub App Setup](github-app-setup.md)).
- Two secrets stored in AWS Secrets Manager (webhook secret and GitHub App private key — see [GitHub App Setup](github-app-setup.md#5-store-secrets-in-aws-secrets-manager)).
- An S3 bucket for Lambda deployment packages.

## 1. Build Lambda Binaries

Build and zip the five Lambda functions from source:

```bash
make lambda.zip
```

This produces five zip files in `bin/`:

- `bin/webhook.zip`
- `bin/scaleup.zip`
- `bin/scaledown.zip`
- `bin/lifecycle.zip`
- `bin/rebalancer.zip`

Alternatively, download pre-built binaries from a [GitHub Release](https://github.com/devopsfactory-io/jit-runners/releases).

## 2. Upload Lambda Packages to S3

```bash
export LAMBDA_BUCKET="your-lambda-bucket"
export VERSION="v1.0.0-rc.4"

for fn in webhook scaleup scaledown lifecycle rebalancer; do
  aws s3 cp "bin/${fn}.zip" "s3://${LAMBDA_BUCKET}/${VERSION}/${fn}.zip"
done
```

> **Note on the S3 key convention**: the live stack uses `${VERSION}/<fn>.zip` (no `jit-runners/` prefix). Older releases used `jit-runners/${VERSION}/<fn>.zip` — that drift is documented in [release.md](release.md).

## 3. Deploy

Pick one of the two IaC options below.

### Option A: OpenTofu / Terraform

The Terraform module lives at `infra/terraform/`.

#### Configure variables

Copy the example tfvars file and fill in your values:

```bash
cd infra/terraform
cp terraform.tfvars.example terraform.tfvars
```

Edit `terraform.tfvars` with your values:
- `aws_region` — AWS region (e.g. `us-east-1`).
- `github_app_id` — Your GitHub App ID (numeric).
- `github_installation_id` — Your GitHub App installation ID (numeric).
- `webhook_secret_arn` — ARN of the Secrets Manager secret containing the webhook secret.
- `private_key_arn` — ARN of the Secrets Manager secret containing the GitHub App private key (PEM body).
- `lambda_s3_bucket` — S3 bucket name where Lambda zips were uploaded.
- `webhook_lambda_s3_key` — S3 key for `webhook.zip` (e.g. `v1.0.0-rc.4/webhook.zip`).
- `scaleup_lambda_s3_key` — S3 key for `scaleup.zip`.
- `scaledown_lambda_s3_key` — S3 key for `scaledown.zip`.
- `lifecycle_lambda_s3_key` — S3 key for `lifecycle.zip`.
- `rebalancer_lambda_s3_key` — S3 key for `rebalancer.zip`.
- `vpc_id` — VPC ID where runner EC2 instances will launch.
- `subnet_ids` — List of subnet IDs (private subnets recommended).
- `default_ami` — AMI ID for runner instances (Amazon Linux 2023; build a private AMI with `make ami.build` or `make ami.build-test`, then use that ID).

Optional:
- `label_mappings` — JSON array mapping workflow labels to instance types (default: `[]`, which uses `t3.medium`).
- `stale_threshold_minutes` — Minutes before a pending runner is considered stale (default: `10`).
- `max_runner_age_minutes` — Maximum age before force-termination (default: `360`).
- `max_re_enqueue_attempts` — Re-enqueue budget for stuck pending runners (default: `3`).

#### Initialize and apply

```bash
tofu init    # or: terraform init
tofu plan    # or: terraform plan
tofu apply   # or: terraform apply
```

#### Webhook URL

```bash
tofu output webhook_url
```

Set the printed URL as the GitHub App's **Webhook URL**.

#### Remote state (recommended)

For production, configure a remote backend in `versions.tf`:

```hcl
terraform {
  backend "s3" {
    bucket = "your-terraform-state-bucket"
    key    = "jit-runners/terraform.tfstate"
    region = "us-east-1"
  }
}
```

#### Destroying the stack

```bash
cd infra/terraform
tofu destroy    # or: terraform destroy
```

This terminates all managed resources including any running EC2 instances.

### Option B: CloudFormation

The CloudFormation template lives at `infra/cloudformation/template.yaml`.

#### Deploy the stack

```bash
aws cloudformation deploy \
  --template-file infra/cloudformation/template.yaml \
  --stack-name jit-runners \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides \
    GitHubAppId="123456" \
    GitHubInstallationId="789012" \
    LambdaS3Bucket="${LAMBDA_BUCKET}" \
    WebhookLambdaS3Key="${VERSION}/webhook.zip" \
    ScaleUpLambdaS3Key="${VERSION}/scaleup.zip" \
    ScaleDownLambdaS3Key="${VERSION}/scaledown.zip" \
    LifecycleLambdaS3Key="${VERSION}/lifecycle.zip" \
    RebalancerLambdaS3Key="${VERSION}/rebalancer.zip" \
    WebhookSecretArn="arn:aws:secretsmanager:us-east-1:123456789012:secret:jit-runners/github-webhook-secret-AbCdEf" \
    PrivateKeySecretArn="arn:aws:secretsmanager:us-east-1:123456789012:secret:jit-runners/github-app-private-key-GhIjKl" \
    VpcId="vpc-0123456789abcdef0" \
    SubnetIds="subnet-aaa,subnet-bbb" \
    DefaultAMI="ami-0123456789abcdef0"
```

`--capabilities CAPABILITY_NAMED_IAM` is required because the template creates named IAM roles, including the `AWSServiceRoleForEC2Spot` service-linked role. This role is required for EC2 to launch spot instances.

**AMI region**: The `DefaultAMI` must exist in the **same AWS region** where you are deploying the stack. The pre-baked AMI is built in `us-east-2` (Packer's `var.aws_region`); to deploy in a different region, build there by running Packer with `-var aws_region=<region>` (or copy the AMI into that region), or use a stock Amazon Linux 2023 AMI.

#### Optional parameters

Add these to `--parameter-overrides` if needed:

- `LabelMappings='[{"label":"large","instance_type":"c5.xlarge"},{"label":"release","instance_type":"m5.xlarge"}]'` — Map workflow labels to instance types.
- `StaleThresholdMinutes=10` — Minutes before a pending runner is considered stale (default: 10).
- `MaxRunnerAgeMinutes=360` — Maximum age before force-termination (default: 360).
- `MaxReEnqueueAttempts=3` — Re-enqueue budget for stuck pending runners (default: 3).

#### Default `LabelMappings`

The template ships with the following label-to-instance-type mappings:

| Label | Instance type |
|-------|---------------|
| nano | t2.nano |
| micro | t2.micro |
| small | t2.small |
| medium | t3.medium |
| large | c5.xlarge |
| release | m5.xlarge |

The `release` label is intended for release workflows that require a stable, low-interruption instance type:

```yaml
jobs:
  release:
    runs-on: [self-hosted, release]
```

Override or extend these mappings via the `LabelMappings` parameter.

#### Webhook URL

```bash
aws cloudformation describe-stacks \
  --stack-name jit-runners \
  --query 'Stacks[0].Outputs[?OutputKey==`WebhookUrl`].OutputValue' \
  --output text
```

Set the printed URL as the GitHub App's **Webhook URL**.

#### Stack outputs

```bash
aws cloudformation describe-stacks \
  --stack-name jit-runners \
  --query 'Stacks[0].Outputs' \
  --output table
```

Available outputs include `WebhookUrl`, all five Lambda ARNs, `DynamoDBTableName`, `SQSQueueUrl`, `LifecycleQueueUrl`, `RunnerSecurityGroupId`, `RunnerInstanceProfileName`.

#### Deleting the stack

```bash
aws cloudformation delete-stack --stack-name jit-runners
aws cloudformation wait stack-delete-complete --stack-name jit-runners
```

This terminates all managed resources including any running EC2 instances.

## 4. Test the Setup

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

2. Trigger the workflow manually from the Actions tab.

3. Watch Lambda logs:

```bash
aws logs tail /aws/lambda/jit-runners-webhook --follow
aws logs tail /aws/lambda/jit-runners-scaleup --follow
aws logs tail /aws/lambda/jit-runners-rebalancer --filter-pattern '"cycle complete"' --follow
```

The rebalancer cycle log should fire every minute and report `cycle complete repo=<owner/repo> demand=N supply=M published=K label_sets=L`. See [troubleshooting.md](troubleshooting.md#stranded-queued-jobs) for what these mean.

## 5. Updating Lambda Functions

When a new version ships:

1. Build or download the new Lambda zips.
2. Upload to S3 with the new version prefix.
3. Update the stack (CFN) or `terraform.tfvars` (Terraform) with the new S3 keys.
4. Re-run `aws cloudformation deploy` or `tofu apply`.

For the canonical step-by-step rollout used in production, see [release.md](release.md).

## Pre-baked AMI

jit-runners ships a pre-baked Amazon Linux 2023 AMI with an ubuntu-latest-like toolchain pre-installed. It is **private** and built in `us-east-2` only — the project is single-tenant, so public distribution was dropped to eliminate EBS-snapshot cost and Packer complexity.

To build a private AMI (production builds and CI tag pushes):

```bash
make ami.build
```

To build a private test AMI with the `jit-runner-pr` prefix (for local Packer validation):

```bash
make ami.build-test
```

See [ami-prebaked.md](ami-prebaked.md) for the full Packer pipeline reference.

## Next Steps

- [GitHub App Setup](github-app-setup.md) — if you haven't set up the GitHub App yet.
- [Release procedure](release.md) — production rollout flow.
- [Troubleshooting](troubleshooting.md) — common operational issues + diagnosis recipes.
- [Getting Started on GCP](getting-started-gcp.md) — GCP path is parallel; same architecture, GCP-native services.
