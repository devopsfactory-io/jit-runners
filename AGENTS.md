# AGENTS.md

Guidance for AI coding agents working on the jit-runners project.

---

## Project Overview

**jit-runners** provides on-demand GitHub Actions self-hosted runners deployable on AWS or GCP. It listens for `workflow_job` webhooks, launches ephemeral JIT runners on EC2 spot or GCE spot, and auto-cleans up after job completion.

Five serverless functions share code via `lambda/internal/`:

- **webhook**: Validates the GitHub webhook signature, parses the `workflow_job` event, and routes to the jobs queue (`queued` action) or the lifecycle queue (`in_progress` / `completed` action).
- **scaleup**: Consumes jobs-queue messages, generates a JIT runner token, and launches an EC2 spot or GCE spot VM. Tracks state in DynamoDB (AWS) or Firestore Native (GCP).
- **scaledown**: Cleans up stale/orphaned instances on a periodic schedule (every 5 minutes); re-enqueues stuck pending runners.
- **lifecycle**: Consumes lifecycle-queue messages, applies state transitions, deregisters runners.
- **rebalancer**: Runs every 1 minute to detect drift between GitHub queue depth and the state store's pending count, re-publishing jobs-queue messages to recover stranded queued jobs.

The cloud-agnostic interfaces (`internal/queue/`, `internal/state/`, `internal/compute/`, `internal/secrets/`) plus the `internal/provider/` factory that dispatches on `CLOUD_PROVIDER` env var allow the same five entry points to run on either cloud.

**Language**: Go (see `lambda/go.mod`).

---

## Repository Structure

- **`lambda/`** – Separate Go module for the five serverless functions.
  - **`cmd/{webhook,scaleup,scaledown,lifecycle,rebalancer}/main.go`** – Function entry points (5 binaries).
  - **`internal/config/`** – Env + cloud-secret-manager config loading.
  - **`internal/github/`** – Webhook signature verify, JWT auth, JIT runner config generation. Cloud-agnostic.
  - **`internal/webhook/`** – `workflow_job` event type parsing + routing dispatch. Cloud-agnostic.
  - **`internal/queue/`** – Cloud-agnostic queue interfaces + typed payload helpers (`PublishScaleUp`, `ParseScaleUp`, `PublishLifecycle`, `ParseLifecycle`).
  - **`internal/state/`** – Cloud-agnostic runner state store interface (`RunnerStore`).
  - **`internal/compute/`** – Cloud-agnostic VM launcher interface.
  - **`internal/secrets/`** – Cloud-agnostic secrets loader interface.
  - **`internal/provider/`** – Bundle factory: `provider.New(ctx, name)` dispatches to AWS or GCP based on `CLOUD_PROVIDER` env var.
  - **`internal/aws/{sqs,dynamo,ec2,secretsmanager}/`** – AWS implementations of the four cloud-agnostic interfaces.
  - **`internal/gcp/{pubsub,firestore,gce,secretmanager,runtime}/`** – GCP implementations of the same interfaces, plus the CloudEvents HTTP entry-point shim.
  - **`internal/runner/`** – Cleanup logic (cloud-agnostic; uses `state.RunnerStore` + `queue.Publisher`).
  - **`internal/lifecycle/`** – Lifecycle event handler (cloud-agnostic).
  - **`internal/rebalancer/`** – Drift-recovery cycle (cloud-agnostic).
- **`infra/`** – Infrastructure as Code.
  - **`terraform/`** – AWS OpenTofu/Terraform HCL (API Gateway, Lambda, SQS, DynamoDB, EC2, IAM).
  - **`cloudformation/`** – AWS CloudFormation template (same resources, YAML).
  - **`terraform-gcp/`** – GCP OpenTofu/Terraform HCL (Cloud Run functions Gen 2, Pub/Sub + Eventarc + Cloud Scheduler, Firestore Native, Secret Manager, GCS, GCE, IAM). Operators set `var.release_tag` and the module fetches function source declaratively from the matching GitHub Release.
  - **`packer/`** – Shared Packer template for building pre-baked runner images. The same template builds either an Amazon Linux 2023 AMI (`amazon-ebs` source) on AWS or an Ubuntu 24.04 LTS GCE image (`googlecompute` source) on GCP.
    - **`jit-runner.pkr.hcl`** – Two parallel sources (`amazon-ebs` and `googlecompute`); shared post-processors and provisioners; cloud-specific `extra_script` slots; image name format `{name_prefix}-{jit_runners_version}-runner{runner_version}-{timestamp}`; community AMI catalog publishing controlled by `ami_groups` (default `["all"]`, set `[]` for private); validation provisioner that fails the build if any critical tool is missing, including `docker compose version` and `docker buildx version`.
    - **`variables.pkr.hcl`** – Shared variables: `runner_version`, `jit_runners_version` (default `dev`; auto-detected from git in CI), `go_version` (default `1.23.6`), `node_major_version` (default `22`); plus AWS-specific (`aws_region`, `ami_regions`, `ami_distribution_regions`, `ami_groups`, `instance_type`, `subnet_id`, `volume_size`) and GCP-specific (`gcp_project`, `gcp_zone`, `gcp_machine_type`, `gcp_image_storage_locations`) knobs.
    - **`scripts/aws/`** – AWS Amazon Linux 2023 provisioning: orchestrator + 7 ordered sub-scripts (`01-system-base.sh` … `07-cleanup.sh`). Pre-installs an ubuntu-latest-like toolchain on AL2023.
    - **`scripts/gcp/`** – GCP Ubuntu 24.04 LTS provisioning: parallel orchestrator + sub-scripts targeting Ubuntu rather than AL2023. Same logical toolchain (Docker, languages, cloud CLIs incl. `gcloud`) installed via apt + curl rather than dnf.
    - Both pipelines write `/opt/jit-runner-prebaked` marker and `/opt/jit-runner-manifest.txt` tool version manifest (includes `jit_runners_version` field).
- **`docs/`** – Deployment guides and operational docs: `getting-started-aws.md` (AWS path; OpenTofu/Terraform AND CloudFormation), `getting-started-gcp.md` (GCP path; Terraform-only), GitHub App setup, troubleshooting (with both AWS and GCP-specific sections), release procedure.
- **`Makefile`**, **`.golangci.yml`**, **`.goreleaser.yml`**, **`.github/workflows/`** – Build, test, lint, release.
- **`.claude/skills/`** – Project-local Claude skills (workflows): `maintain-documentation/`, `open-pull-request/`, `release-and-versioning/`, `testing-and-ci/`.
- **`.claude/commands/`** – Claude slash commands: `/feature`, `/bug` (invoke the issue-writer workflow; drafts are validated by issue-reviewer before `gh issue create`). Hub-side agents (documentation-maintainer, issue-reviewer, issue-writer, pr-reviewer, etc.) live in the `code-agent-hub` repo and operate against this repo via the project skills above.

---

## Setup Commands

```bash
# Build all five function binaries (webhook, scaleup, scaledown, lifecycle, rebalancer)
make lambda.build

# Run tests with race detection and coverage
make lambda.test

# Run all checks (lint + vet + test)
make check

# Run golangci-lint
make lint

# Check Go formatting
make check-fmt

# AWS — Pre-baked AMI build (Packer, amazon-ebs source)
make ami.validate
make ami.build                        # public AMI in us-east-2 (version from git)
make ami.build-test                   # private test AMI in us-east-2
make ami.build-distribute             # public AMI + copy to all distribution regions (US, EU, SA)
make ami.copy AMI_ID=ami-xxxxxxxx     # copy an existing AMI to all distribution regions

# GCP — Pre-baked GCE image build (Packer, googlecompute source — same template, parallel scripts)
make image.validate
make image.build GCP_PROJECT=my-project          # public GCE image (multi-region distribute via separate command)
make image.build-test GCP_PROJECT=my-project     # private test GCE image
make image.build-distribute GCP_PROJECT=my-project   # public + multi-region storage replication
```

Use Go version from `lambda/go.mod`. CI runs formatting check, go vet, `make lambda.test`, and golangci-lint.

---

## Code Style

- **Format**: Use `gofmt -s`; run `make check-fmt` before committing.
- **Linting**: `.golangci.yml` is authoritative; do not introduce new linter violations.
- **Packages**: Code under `lambda/internal/` must not be imported from outside the lambda module.
- **Errors**: Return errors with context (e.g. `fmt.Errorf("...: %w", err)`); avoid naked returns.
- **Exports**: Public functions and types should have doc comments starting with the name.
- **Cloud-service interfaces**: Define interfaces for cloud service clients (AWS: EC2/SQS/DynamoDB/SecretsManager; GCP: Compute/PubSub/Firestore/SecretManager) to enable testing with mocks. The cloud-agnostic interfaces live in `internal/queue|state|compute|secrets/`.

---

## Testing

- **Run**: `cd lambda && go test ./...` or `make lambda.test`.
- **Location**: Place `*_test.go` next to the code under test (same package).
- **Coverage**: Existing tests cover `lambda/internal/github`. Add tests for new behavior.
- **No external services**: Unit tests should not require live AWS or GCP APIs; mock via the cloud-agnostic interfaces.
- **Mocking**: AWS and GCP clients must implement the cloud-agnostic interfaces (`queue.Publisher`, `state.RunnerStore`, `compute.Launcher`, `secrets.Loader`) so tests can inject mock implementations regardless of cloud.

---

## CI

- **`.github/workflows/test.yml`** – On PRs; runs formatting check (`gofmt -s`), go vet, `make lambda.test`, and golangci-lint. Self-hosted small runner.
- **`.github/workflows/labeler.yml`** – On pull_request (opened, synchronize, reopened); runs [actions/labeler](https://github.com/actions/labeler) with [.github/labeler.yml](.github/labeler.yml). Path-based: jit-runners, lambda/go.mod, documentation. Head-branch: `feat*`→feature, `enhance*`→enhancement, `fix*` (not fix*dep*)→bug, branch containing `!`→breaking-change, `ci*`→github-actions, `(deps)`→dependencies.
- **`.github/workflows/label-old-prs.yml`** – workflow_dispatch; applies the labeler to existing PRs (inputs: state e.g. merged/closed/all, limit). Use to backfill labels on old or merged PRs.
- **`.github/workflows/release.yml`** – On push of tags `v*.*.*` (and workflow_dispatch); runs GoReleaser to create a GitHub Release with five function zip archives (`webhook.zip`, `scaleup.zip`, `scaledown.zip`, `lifecycle.zip`, `rebalancer.zip`), raw binaries, checksums, and release notes. The GCP Terraform module fetches these zips declaratively from the release matching `var.release_tag`.
- **`.github/workflows/gce-image-build.yml`** – GCP GCE image build on tag push or `workflow_dispatch`. OIDC auth via `GCE_BUILD_WIF_PROVIDER` + `GCE_BUILD_SA_EMAIL` repo secrets (Workload Identity Federation set up out-of-band in the maintainer's personal GCP project per spec D14).
- **`.github/workflows/ami-build.yml`** – workflow_dispatch (inputs: `runner_version`, `go_version`, `node_major_version`, `jit_runners_version`, `extra_script`, `distribute`), auto-trigger on version tag push (`v*`), and pull_request trigger for `infra/packer/**` changes. `jit_runners_version` is auto-detected via `git describe --tags --always` when not provided. PR builds create private (`ami_groups=[]`) AMIs with the `jit-runner-pr` name prefix, no distribution, and a post-build cleanup step that deregisters the AMI and deletes its snapshots. Non-PR builds run `packer validate` then `packer build`; when `distribute=true`, copies AMI to all distribution regions (US, EU, SA). Uses OIDC (`AMI_BUILD_ROLE_ARN` secret) to assume the build role. Writes AMI ID, jit-runners version, runner version, Go version, Node.js version, and build summary to the GitHub Actions job summary. **Runs on `ubuntu-latest` (GitHub-hosted)**: the self-hosted runner security group only allows egress on ports 443/80/53 — SSH (port 22) is blocked outbound, which causes Packer to time out when connecting to the build instance. GitHub-hosted runners have unrestricted network access and eliminate the circular dependency of building jit-runner AMIs on the jit-runners infrastructure itself.
- **Renovate** – Dependency-update PRs (Go modules and GitHub Actions) are opened by [Renovate](https://docs.renovatebot.com/) from [.github/renovate.json5](.github/renovate.json5). Do not remove or override this config without reason.

Semantic versioning: use tags like `v0.1.0`.

---

## Documentation and AI Context (Mandatory)

After any change that affects behavior, APIs, IaC, config, or CI:

1. **Delegate**: Delegate documentation updates to the hub-side **documentation-maintainer** subagent (lives in `code-agent-hub`'s `.claude/agents/`) so it runs the full maintain-documentation checklist (README, docs/, infra/, AGENTS.md, CLAUDE.md, .claude/commands, .claude/skills).
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
- **Deployment**: `docs/github-app-setup.md`, `docs/getting-started-aws.md`, `docs/getting-started-gcp.md`.
- **Operations**: `docs/troubleshooting.md` – common issues (zombie runners, vCPU limits, DLQ, stale DynamoDB entries, AMI mismatch), diagnosis commands, and resolutions.
