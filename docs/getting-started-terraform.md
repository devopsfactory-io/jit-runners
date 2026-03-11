# Getting Started with Terraform / OpenTofu

## Overview
This guide walks through deploying jit-runners using the OpenTofu/Terraform configuration in `infra/terraform/`.

## Prerequisites
- AWS account with permissions to create Lambda, API Gateway, SQS, DynamoDB, EC2, IAM, EventBridge, and CloudWatch resources
- [OpenTofu](https://opentofu.org/) >= 1.6.0 or Terraform >= 1.6.0 installed
- AWS CLI configured with credentials
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

## 3. Configure Variables

Copy the example tfvars file and fill in your values:

```bash
cd infra/terraform
cp terraform.tfvars.example terraform.tfvars
```

Edit `terraform.tfvars` with your values:
- `aws_region` -- AWS region (e.g. `us-east-1`)
- `github_app_id` -- Your GitHub App ID (numeric)
- `webhook_secret_arn` -- ARN of the Secrets Manager secret containing the webhook secret
- `private_key_secret_arn` -- ARN of the Secrets Manager secret containing the private key PEM
- `lambda_s3_bucket` -- S3 bucket name where Lambda zips were uploaded
- `webhook_lambda_s3_key` -- S3 key for webhook.zip (e.g. `jit-runners/v0.1.0/webhook.zip`)
- `scaleup_lambda_s3_key` -- S3 key for scaleup.zip
- `scaledown_lambda_s3_key` -- S3 key for scaledown.zip
- `vpc_id` -- VPC ID where runner EC2 instances will launch
- `subnet_ids` -- List of subnet IDs (private subnets recommended)
- `default_ami` -- AMI ID for runner instances (Amazon Linux 2023 recommended)

Optional:
- `label_mappings` -- JSON array mapping workflow labels to instance types (default: `[]`, which uses `t3.medium`)
- `stale_threshold_minutes` -- Minutes before a pending runner is considered stale (default: `10`)
- `max_runner_age_minutes` -- Maximum age before force-termination (default: `360`)

## 4. Initialize and Deploy

```bash
cd infra/terraform

# Initialize providers
tofu init    # or: terraform init

# Review the plan
tofu plan    # or: terraform plan

# Apply
tofu apply   # or: terraform apply
```

## 5. Set the Webhook URL

After deployment, Terraform outputs the webhook URL:

```bash
tofu output webhook_url
```

Go to your GitHub App settings and set the **Webhook URL** to this value (it will look like `https://xxxxxxxxxx.execute-api.us-east-1.amazonaws.com/webhook`).

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
3. Watch the Lambda logs to see the webhook received and EC2 instance launched:

```bash
aws logs tail /aws/lambda/jit-runners-webhook --follow
aws logs tail /aws/lambda/jit-runners-scaleup --follow
```

## 7. Customize Instance Types (Optional)

Use `label_mappings` to map workflow labels to specific instance types:

```hcl
label_mappings = jsonencode([
  {"label": "large",   "instance_type": "c5.2xlarge", "ami": "ami-xxxxxxxxx"},
  {"label": "gpu",     "instance_type": "g4dn.xlarge", "ami": "ami-yyyyyyyyy"},
  {"label": "arm64",   "instance_type": "c7g.xlarge",  "ami": "ami-zzzzzzzzz"}
])
```

Then use the labels in your workflows:

```yaml
jobs:
  build:
    runs-on: [self-hosted, large]
```

## Remote State (Recommended)

For production use, configure a remote backend. Uncomment the S3 backend block in `versions.tf`:

```hcl
terraform {
  backend "s3" {
    bucket = "your-terraform-state-bucket"
    key    = "jit-runners/terraform.tfstate"
    region = "us-east-1"
  }
}
```

## Updating Lambda Functions

When a new version is released:

1. Build or download the new Lambda zips
2. Upload to S3 with the new version prefix
3. Update the S3 key variables in `terraform.tfvars`
4. Run `tofu plan && tofu apply`

## Destroying the Stack

```bash
cd infra/terraform
tofu destroy    # or: terraform destroy
```

This terminates all managed resources including any running EC2 instances.

## Next Steps

- [GitHub App Setup](github-app-setup.md) -- if you haven't set up the GitHub App yet
- [Getting Started with CloudFormation](getting-started-cloudformation.md) -- alternative deployment method
