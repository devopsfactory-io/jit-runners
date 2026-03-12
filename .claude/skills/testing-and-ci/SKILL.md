---
name: testing-and-ci
description: Runs tests, lint, and format checks; explains CI workflows. Use when running or debugging tests, lint, or GitHub Actions.
---

# Testing and CI

## Commands

- **make lambda.build** – Build all three Lambda binaries (webhook, scaleup, scaledown).
- **make lambda.test** – Run tests with race detection and coverage.
- **make lint** – Run golangci-lint (requires golangci-lint installed).
- **make check** – Run all checks (lint + vet + test).
- **make check-fmt** – Verify Go formatting (`gofmt -s -l`); fails if any file needs formatting.
- **make ami.validate** – Validate the Packer template (`packer validate`).
- **make ami.build** – Build pre-baked runner AMI in us-east-2 only.
- **make ami.build-distribute** – Build AMI and copy to all distribution regions (US, EU, SA).
- **make ami.copy AMI_ID=ami-xxx** – Copy an existing AMI to all distribution regions.

## CI Workflows

- **test.yml** – On PRs. Runs `make check-fmt`, `go vet`, `make lambda.test`, and golangci-lint. Uses self-hosted runners (medium).
- **labeler.yml** – On pull_request. Runs actions/labeler with `.github/labeler.yml` (path and head-branch rules).
- **label-old-prs.yml** – workflow_dispatch. Backfills labels: applies same rules as labeler to head branch and PR title (labeler has no branch context on manual run), then runs the labeler for path-based labels. Use state and limit inputs.
- **release.yml** – On push of tags `v*.*.*`. Runs GoReleaser to create the GitHub Release with Lambda zip archives.
- **ami-build.yml** – workflow_dispatch (inputs: `runner_version`, `extra_script`, `distribute`) and auto-trigger on push to `infra/packer/**`. Runs `packer validate` then `packer build`. When `distribute=true`, copies AMI to all distribution regions. Uses OIDC (`AMI_BUILD_ROLE_ARN` secret). Writes AMI ID to the job summary.

## Adding Tests

- Place `*_test.go` in the same package as the code (e.g. `lambda/internal/github/verify_test.go`).
- Use table-driven tests for multiple cases; single-case tests are fine when appropriate.
- Define interfaces for AWS service clients (EC2, SQS, DynamoDB) to enable testing with mocks.
- Do not depend on live AWS APIs in unit tests; use interface mocks.
- Existing test packages: `lambda/internal/github`. Add or extend tests when touching those areas or adding new packages.
