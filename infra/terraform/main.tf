# jit-runners - On-demand GitHub Actions self-hosted runners
#
# This module deploys:
# - API Gateway (webhook endpoint)
# - 3 Lambda functions (webhook, scale-up, scale-down)
# - SQS queue with DLQ (event buffering)
# - DynamoDB table (runner state tracking)
# - EC2 security group and IAM instance profile (runner instances)
# - EventBridge schedule (scale-down every 5 minutes)
#
# Prerequisites:
# - GitHub App with workflow_job webhook subscription
# - Webhook secret and App private key in Secrets Manager
# - Lambda zip files uploaded to S3
# - VPC with private subnets for runner instances
