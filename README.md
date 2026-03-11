# jit-runners

<p align="center">
  <a href="https://github.com/devopsfactory-io/jit-runners/releases"><img src="https://img.shields.io/github/v/release/devopsfactory-io/jit-runners?color=%239F50DA&display_name=tag&label=Version" alt="Latest Release" /></a>
  <a href="https://pkg.go.dev/github.com/devopsfactory-io/jit-runners"><img src="https://pkg.go.dev/badge/github.com/devopsfactory-io/jit-runners" alt="Go Docs" /></a>
  <a href="https://goreportcard.com/report/github.com/devopsfactory-io/jit-runners"><img src="https://goreportcard.com/badge/github.com/devopsfactory-io/jit-runners" alt="Go Report Card" /></a>
  <a href="https://github.com/devopsfactory-io/jit-runners/actions?query=branch%3Amain"><img src="https://github.com/devopsfactory-io/jit-runners/actions/workflows/test.yml/badge.svg" alt="CI Status" /></a>
</p>

<p align="center">
  <b>On-demand GitHub Actions self-hosted runners using AWS Lambda (Go) + EC2 spot instances</b>
</p>

- [jit-runners](#jit-runners)
  - [Resources](#resources)
  - [What is jit-runners?](#what-is-jit-runners)
  - [How does it work?](#how-does-it-work)
  - [Why use it?](#why-use-it)
  - [Quick Start](#quick-start)

## Resources

- **Documentation**: [docs/](docs/) - Configuration, deployment, and development guides.
- **Releases**: [github.com/devopsfactory-io/jit-runners/releases](https://github.com/devopsfactory-io/jit-runners/releases)
- **Infrastructure (OpenTofu/Terraform)**: [infra/terraform/](infra/terraform/) - HCL modules for all AWS resources.
- **Infrastructure (CloudFormation)**: [infra/cloudformation/](infra/cloudformation/) - CloudFormation template (`template.yaml`).
- **Getting started (Terraform)**: [docs/getting-started-terraform.md](docs/getting-started-terraform.md)
- **Getting started (CloudFormation)**: [docs/getting-started-cloudformation.md](docs/getting-started-cloudformation.md)
- **GitHub App setup**: [docs/github-app-setup.md](docs/github-app-setup.md) - Create and configure the GitHub App that sends `workflow_job` webhooks.
- **Contributing**: [CLAUDE.md](CLAUDE.md) for AI and contributor guidance.

## What is jit-runners?

jit-runners provisions on-demand GitHub Actions self-hosted runners that launch EC2 spot instances as ephemeral JIT (Just-In-Time) runners. Three AWS Lambda functions written in Go handle webhook reception, instance provisioning, and cleanup. There are no long-running servers — the entire control plane runs on serverless infrastructure.

```mermaid
graph LR
    A[GitHub webhook<br>workflow_job] --> B[API Gateway]
    B --> C[Webhook Lambda]
    C --> D[SQS Queue]
    D --> E[Scale-Up Lambda]
    E --> F[EC2 Spot<br>JIT Runner]
    G[EventBridge<br>every 5min] --> H[Scale-Down Lambda]
    H -->|cleanup| F
```

The three Lambda functions share code via `lambda/internal/`:

- **webhook** - Validates the GitHub webhook signature, parses the `workflow_job` event, and enqueues a message to SQS.
- **scaleup** - Processes SQS messages, generates a JIT runner token via the GitHub API, and launches an EC2 spot instance with a user-data script that registers and runs the ephemeral runner.
- **scaledown** - Runs on a schedule to clean up stale or orphaned instances and deregisters any runners that have not self-terminated.

## How does it work?

1. A GitHub App sends `workflow_job` webhooks to an API Gateway endpoint when a workflow job is queued.
2. The Webhook Lambda validates the HMAC signature, parses the event, and enqueues a message to SQS with a 30-second visibility delay — this gives any already-warm runner a chance to claim the job before a new instance is launched.
3. The Scale-Up Lambda processes the SQS message, calls the GitHub API to generate a JIT runner registration token, and launches an EC2 spot instance. The instance user-data script installs the runner agent, registers it using the JIT config, and immediately starts accepting jobs.
4. After the job completes, the runner agent self-deregisters from GitHub and the instance self-terminates — no manual cleanup needed.
5. The Scale-Down Lambda fires every 5 minutes via an EventBridge rule. It queries DynamoDB for runner state and terminates any instances that are stale, orphaned, or whose runners have already deregistered.

## Why use it?

- **Up to 90% cost savings** - EC2 spot instances cost a fraction of GitHub-hosted runners for equivalent compute.
- **No idle infrastructure** - Runners launch on demand and terminate after use; you pay only for the seconds a job is running.
- **Private network access** - Runners launch inside your VPC and can reach private resources (RDS, EKS API, internal registries) that GitHub-hosted runners cannot.
- **Custom hardware** - Configure instance types and sizes per workflow label (e.g. `runs-on: [self-hosted, c6i.4xlarge]`).
- **Single-use ephemeral runners** - Each job gets a clean environment with no shared state, no credential leakage, and no leftover artifacts from previous runs.
- **Serverless control plane** - No servers to maintain or patch. The entire orchestration layer is Lambda, SQS, DynamoDB, and EventBridge.

## Quick Start

Choose the deployment path that matches your tooling:

- **OpenTofu / Terraform**: Follow [docs/getting-started-terraform.md](docs/getting-started-terraform.md) to deploy the full AWS stack with HCL modules in [infra/terraform/](infra/terraform/).
- **CloudFormation**: Follow [docs/getting-started-cloudformation.md](docs/getting-started-cloudformation.md) to deploy using the SAM/CloudFormation template in [infra/cloudformation/template.yaml](infra/cloudformation/template.yaml).

Both guides assume a GitHub App is already configured. If you have not set one up yet, start with [docs/github-app-setup.md](docs/github-app-setup.md).
