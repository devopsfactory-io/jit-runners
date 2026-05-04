# Infrastructure

jit-runners infrastructure can be deployed on either AWS or GCP. The control plane (5 serverless functions, a job queue, a state store, a runner-VM launcher) is identical across clouds; only the underlying provider services differ.

## Deployment Options

| Cloud | IaC tool | Directory | Guide |
| ----- | -------- | --------- | ----- |
| **AWS** | Terraform / OpenTofu | [`terraform/`](terraform/) | [Getting Started on AWS](../docs/getting-started-aws.md) |
| **AWS** | CloudFormation | [`cloudformation/`](cloudformation/) | [Getting Started on AWS](../docs/getting-started-aws.md) |
| **GCP** | Terraform / OpenTofu | [`terraform-gcp/`](terraform-gcp/) | [Getting Started on GCP](../docs/getting-started-gcp.md) |

The Packer template at [`packer/`](packer/) is shared across both clouds and produces either an AWS AMI (`amazon-ebs` source) or a GCE image (`googlecompute` source) from the same provisioning recipe.

## Resources Created

The five serverless functions (`webhook`, `scaleup`, `scaledown`, `lifecycle`, `rebalancer`) are mapped to per-cloud equivalents:

| Component | AWS | GCP |
| --------- | --- | --- |
| Webhook ingress | API Gateway HTTP | Cloud Run function HTTPS URL |
| Functions runtime | Lambda (`provided.al2023`) | Cloud Run functions Gen 2 (`go122`) |
| Job queue | SQS + EventBridge schedule | Pub/Sub + Eventarc + Cloud Scheduler |
| State store | DynamoDB on-demand | Firestore Native + TTL |
| Secrets | AWS Secrets Manager | Secret Manager |
| Runner VM | EC2 spot | GCE spot |
| Periodic schedule | EventBridge (5 min, 1 min) | Cloud Scheduler (5 min, 1 min) |

## Prerequisites

Before deploying, complete the [GitHub App Setup](../docs/github-app-setup.md) to create the required GitHub App and store the webhook secret + private key in your cloud's secret manager (AWS Secrets Manager or GCP Secret Manager).

## Architecture

```
GitHub webhook (workflow_job)
  → Webhook function (validate + parse + enqueue)
    → Jobs queue (SQS on AWS, Pub/Sub on GCP)
      → Scaleup function (JIT config + spot VM launch)
        → Spot VM (ephemeral JIT runner)
    → Lifecycle queue (in_progress / completed events)
      → Lifecycle function (state transitions, deregistration)

Periodic schedule (every 5 min)
  → Scaledown function (cleanup stale/orphaned instances)

Periodic schedule (every 1 min)
  → Rebalancer function (drift recovery, re-publish stranded jobs)
```
