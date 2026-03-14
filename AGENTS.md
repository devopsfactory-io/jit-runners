# AGENTS.md

Guidance for AI coding agents working on the jit-runners project.

---

## Project Overview

**jit-runners** provides on-demand GitHub Actions self-hosted runners using AWS Lambda (Go) and EC2 spot instances. It listens for `workflow_job` webhooks, launches ephemeral JIT runners on EC2 spot, and auto-cleans up after job completion.

Three Lambda functions share code via `lambda/internal/`:

- **webhook**: Validates signature, parses `workflow_job` event, enqueues to SQS.
- **scaleup**: Consumes SQS messages, launches EC2 spot, generates JIT runner config, tracks state in DynamoDB.
- **scaledown**: Cleans up stale/orphaned instances on an EventBridge schedule (every 5 minutes).

**Language**: Go (see `lambda/go.mod`).

---

## Repository Structure

- **`lambda/`** – Separate Go module for Lambda functions.
  - **`cmd/{webhook,scaleup,scaledown}/main.go`** – Lambda entry points.
  - **`internal/config/`** – Env + Secrets Manager config loading.
  - **`internal/github/`** – Webhook signature verify, JWT auth, JIT runner config generation.
  - **`internal/webhook/`** – `workflow_job` event type parsing.
  - **`internal/ec2/`** – EC2 spot instance launcher + user-data bootstrap script.
  - **`internal/sqs/`** – SQS publisher and message types.
  - **`internal/runner/`** – DynamoDB state store + stale instance cleanup logic.
- **`infra/`** – Infrastructure as Code.
  - **`terraform/`** – OpenTofu/Terraform HCL (API Gateway, Lambda, SQS, DynamoDB, EC2, IAM).
  - **`cloudformation/`** – AWS CloudFormation template (same resources, YAML).
  - **`packer/`** – Packer template for building a pre-baked AL2023 runner AMI.
    - **`jit-runner.pkr.hcl`** – amazon-ebs source; `associate_public_ip_address = true` and `ssh_timeout = "10m"` ensure Packer can SSH into the build instance when the default subnet does not auto-assign public IPs (fixes issue #4); AMI name format `{ami_name_prefix}-{jit_runners_version}-runner{runner_version}-{timestamp}`; tags including `jit-runners-version` and `tools` (comma-separated tool list including `git-lfs`); community AMI catalog publishing controlled by `ami_groups` (default `["all"]`, set `[]` for private); validation provisioner that fails the build if any critical tool is missing, including `docker compose version` and `docker buildx version`.
    - **`variables.pkr.hcl`** – `runner_version`, `jit_runners_version` (default `dev`; auto-detected from git in CI), `aws_region`, `ami_regions`, `ami_distribution_regions`, `ami_groups` (default `["all"]` for public; set `[]` for private PR builds), `instance_type`, `extra_script`, `ami_name_prefix`, `subnet_id`, `go_version` (default `1.23.6`), `node_major_version` (default `22`), `volume_size` (default `30` GB gp3).
    - **`scripts/setup-runner.sh`** – Orchestrator: calls 7 ordered sub-scripts (`01-system-base.sh`, `02-docker.sh`, `03-languages.sh`, `04-cloud-tools.sh`, `05-cli-tools.sh`, `06-runner-agent.sh`, `07-cleanup.sh`). Pre-installs an ubuntu-latest-like toolchain: Docker CE + Compose v2 + Buildx, Python 3, Node.js LTS (installed from nodejs.org binary tarball — not NodeSource RPM), Go, AWS CLI v2, kubectl, Helm 3, gh, jq, yq, git-lfs, gcc/g++/cmake, and common compression utilities. Writes `/opt/jit-runner-prebaked` marker and `/opt/jit-runner-manifest.txt` tool version manifest (includes `jit_runners_version` field).
- **`docs/`** – Deployment guides: GitHub App setup, Terraform guide, CloudFormation guide.
- **`Makefile`**, **`.golangci.yml`**, **`.goreleaser.yml`**, **`.github/workflows/`** – Build, test, lint, release.
- **`.claude/agents/`** – Claude agents: documentation-maintainer (runs doc checklist after code/config/IaC/CI changes; delegate to it for README, docs/, infra/, AGENTS.md, CLAUDE.md, commands, skills), issue-reviewer, pr-reviewer (discoverable for triage and PR review), issue-writer (opens feature requests and bug reports from `/feature` and `/bug` using [.github/ISSUE_TEMPLATE/](.github/ISSUE_TEMPLATE/); drafts are validated by issue-reviewer before upload).
- **`.claude/commands/`** – Claude slash commands: `/feature`, `/bug` (invoke the issue-writer workflow; drafts are validated by issue-reviewer before `gh issue create`).

---

## Setup Commands

```bash
# Build all three Lambda binaries
make lambda.build

# Run tests with race detection and coverage
make lambda.test

# Run all checks (lint + vet + test)
make check

# Run golangci-lint
make lint

# Check Go formatting
make check-fmt

# Validate Packer template
make ami.validate

# Build pre-baked runner AMI in us-east-2 only (public; version from git)
make ami.build

# Build a private (non-public) test AMI in us-east-2
make ami.build-test

# Build AMI and copy to all distribution regions (US, EU, SA)
make ami.build-distribute

# Copy an existing AMI to all distribution regions
make ami.copy AMI_ID=ami-xxxxxxxx
```

Use Go version from `lambda/go.mod`. CI runs formatting check, go vet, `make lambda.test`, and golangci-lint.

---

## Code Style

- **Format**: Use `gofmt -s`; run `make check-fmt` before committing.
- **Linting**: `.golangci.yml` is authoritative; do not introduce new linter violations.
- **Packages**: Code under `lambda/internal/` must not be imported from outside the lambda module.
- **Errors**: Return errors with context (e.g. `fmt.Errorf("...: %w", err)`); avoid naked returns.
- **Exports**: Public functions and types should have doc comments starting with the name.
- **AWS interfaces**: Define interfaces for AWS service clients (EC2, SQS, DynamoDB) to enable testing with mocks.

---

## Testing

- **Run**: `cd lambda && go test ./...` or `make lambda.test`.
- **Location**: Place `*_test.go` next to the code under test (same package).
- **Coverage**: Existing tests cover `lambda/internal/github`. Add tests for new behavior.
- **No external services**: Unit tests should not require live AWS APIs; mock via interfaces.
- **Mocking**: AWS clients must implement interfaces so tests can inject mock implementations.

---

## CI

- **`.github/workflows/test.yml`** – On PRs; runs formatting check (`gofmt -s`), go vet, `make lambda.test`, and golangci-lint. Self-hosted small runner.
- **`.github/workflows/labeler.yml`** – On pull_request (opened, synchronize, reopened); runs [actions/labeler](https://github.com/actions/labeler) with [.github/labeler.yml](.github/labeler.yml). Path-based: jit-runners, lambda/go.mod, documentation. Head-branch: `feat*`→feature, `enhance*`→enhancement, `fix*` (not fix*dep*)→bug, branch containing `!`→breaking-change, `ci*`→github-actions, `(deps)`→dependencies.
- **`.github/workflows/label-old-prs.yml`** – workflow_dispatch; applies the labeler to existing PRs (inputs: state e.g. merged/closed/all, limit). Use to backfill labels on old or merged PRs.
- **`.github/workflows/release.yml`** – On push of tags `v*.*.*` (and workflow_dispatch); runs GoReleaser to create GitHub Release with three Lambda zip archives (webhook.zip, scaleup.zip, scaledown.zip), raw binaries, checksums, and release notes.
- **`.github/workflows/ami-build.yml`** – workflow_dispatch (inputs: `runner_version`, `go_version`, `node_major_version`, `jit_runners_version`, `extra_script`, `distribute`), auto-trigger on push to `infra/packer/**`, and pull_request trigger for `infra/packer/**` changes. `jit_runners_version` is auto-detected via `git describe --tags --always` when not provided. PR builds create private (`ami_groups=[]`) AMIs with the `jit-runner-pr` name prefix, no distribution, and a post-build cleanup step that deregisters the AMI and deletes its snapshots. Non-PR builds run `packer validate` then `packer build`; when `distribute=true`, copies AMI to all distribution regions (US, EU, SA). Uses OIDC (`AMI_BUILD_ROLE_ARN` secret) to assume the build role. Writes AMI ID, jit-runners version, runner version, Go version, Node.js version, and build summary to the GitHub Actions job summary. **Runs on `ubuntu-latest` (GitHub-hosted)**: the self-hosted runner security group only allows egress on ports 443/80/53 — SSH (port 22) is blocked outbound, which causes Packer to time out when connecting to the build instance. GitHub-hosted runners have unrestricted network access and eliminate the circular dependency of building jit-runner AMIs on the jit-runners infrastructure itself.
- **Renovate** – Dependency-update PRs (Go modules and GitHub Actions) are opened by [Renovate](https://docs.renovatebot.com/) from [.github/renovate.json5](.github/renovate.json5). Do not remove or override this config without reason.

Semantic versioning: use tags like `v0.1.0`.

---

## Documentation and AI Context (Mandatory)

After any change that affects behavior, APIs, IaC, config, or CI:

1. **Delegate**: Delegate documentation updates to the **documentation-maintainer** subagent (`.claude/agents/documentation-maintainer.md`) so it runs the full maintain-documentation checklist (README, docs/, infra/, AGENTS.md, CLAUDE.md, .claude/commands, .claude/skills).
2. **Do not edit plan files** unless the user explicitly asks.

When in doubt, update. See `CLAUDE.md` (Documentation rule, always applies) and the **maintain-documentation** skill (`.claude/skills/maintain-documentation/`).

---

## PR Guidance

Before submitting:

1. **Commits must be signed off (DCO).** Use `git commit -s` when creating commits. Do not add a `Made-with: Cursor` (or similar) trailer to commit messages. If you already committed without sign-off, run `git commit --amend -s --no-edit` then force-push.
2. Run `make lambda.test` and `make check-fmt`.
3. Ensure no new linter errors (`make lint` if available).
4. If behavior or setup changed, delegate to the **documentation-maintainer** subagent.
5. **Branch naming**: Branch names matching [.github/labeler.yml](.github/labeler.yml) (e.g. `feat/...`, `fix/...`, `enhance/...`, `(deps)/...`, `ci/...`, or branch containing `!` for breaking) get PR labels applied automatically, which drive release-note categories.

---

## References

- **Claude project rules**: `CLAUDE.md` – mandatory rules (DCO, Go standards, CI/release).
- **Claude commands**: `.claude/commands/` – slash commands (`/feature`, `/bug`) that trigger the issue-writer workflow.
- **Claude skills**: `.claude/skills/` – workflows for documentation maintenance, releases, testing, and open-pull-request.
- **Deployment**: `docs/github-app-setup.md`, `docs/getting-started-terraform.md`, `docs/getting-started-cloudformation.md`.
