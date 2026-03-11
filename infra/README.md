# Infrastructure

jit-runners infrastructure can be deployed using either Terraform/OpenTofu or CloudFormation. Both options create the same set of AWS resources.

## Deployment Options

| Option | Directory | Guide |
| ------ | --------- | ----- |
| **Terraform / OpenTofu** | [`terraform/`](terraform/) | [Getting Started with Terraform](../docs/getting-started-terraform.md) |
| **CloudFormation** | [`cloudformation/`](cloudformation/) | [Getting Started with CloudFormation](../docs/getting-started-cloudformation.md) |

## Resources Created

Both deployment options provision:

- **API Gateway** (HTTP API) — public webhook endpoint for GitHub
- **3 Lambda functions** — webhook, scale-up, scale-down (Go, `provided.al2023`)
- **3 IAM roles** — one per Lambda with least-privilege policies
- **SQS queue + DLQ** — decouples webhook from instance provisioning (30s delay)
- **DynamoDB table** — runner state tracking with TTL auto-cleanup
- **EC2 security group** — egress-only (HTTPS, HTTP, DNS) for runner instances
- **EC2 IAM instance profile** — self-terminate permission only
- **EventBridge Scheduler** — triggers scale-down every 5 minutes
- **CloudWatch log groups** — 14-day retention for all Lambda functions

## Prerequisites

Before deploying, complete the [GitHub App Setup](../docs/github-app-setup.md) to create the required GitHub App and store secrets in AWS Secrets Manager.

## Architecture

```
GitHub webhook (workflow_job)
  → API Gateway (POST /webhook)
    → Webhook Lambda (validate + parse + enqueue)
      → SQS (30s delay)
        → Scale-Up Lambda (JIT config + EC2 spot launch)
          → EC2 Spot Instance (ephemeral JIT runner)

EventBridge (every 5min)
  → Scale-Down Lambda (cleanup stale/orphaned instances)
```
