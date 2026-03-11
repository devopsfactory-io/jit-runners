# jit-runners

On-demand GitHub Actions self-hosted runners using AWS Lambda (Go) + EC2 spot instances. Listens for `workflow_job` webhooks, launches ephemeral JIT runners on EC2 spot, and auto-cleans up after job completion.

**Language**: Go. See `lambda/go.mod` for the current version.

---

## Architecture

```
GitHub webhook (workflow_job) → API Gateway → Webhook Lambda → SQS → Scale-Up Lambda → EC2 Spot (JIT runner)
                                                                                        Scale-Down Lambda (EventBridge, every 5min) → cleanup
```

Three Lambda functions share code via `lambda/internal/`:
- **webhook**: Validates signature, parses event, enqueues to SQS
- **scaleup**: Launches EC2 spot, generates JIT runner config, tracks state in DynamoDB
- **scaledown**: Cleans up stale/orphaned instances on a schedule

## Go Standards

Applies to all `**/*.go` files:

- **Format**: `gofmt -s`. Run `make check-fmt` before committing.
- **Lint**: Conform to `.golangci.yml`. Do not introduce new violations.
- **Packages**: Code in `lambda/internal/` must not be imported from outside this module.
- **Errors**: Wrap errors with context: `fmt.Errorf("context: %w", err)`. Never silently ignore errors.
- **Exports**: Public functions and types must have doc comments starting with the identifier name.
- **Tests**: Place `*_test.go` in the same package as the code. Use table-driven tests.
- **Interfaces**: Define interfaces for AWS service clients to enable testing with mocks.

## Project Layout

```
lambda/                     # Separate Go module for Lambda functions
  cmd/{webhook,scaleup,scaledown}/main.go   # Entry points
  internal/
    config/                 # Env + Secrets Manager config
    github/                 # Webhook verify, JWT auth, JIT runner API
    webhook/                # workflow_job event parsing
    ec2/                    # Spot instance launch + user-data
    sqs/                    # SQS publish/consume
    runner/                 # DynamoDB state + cleanup
infra/
  terraform/                # OpenTofu/Terraform IaC (HCL)
  cloudformation/           # AWS CloudFormation template (YAML)
docs/                       # Deployment guides and setup instructions
```

## Build & Test

```bash
make lambda.build    # Build all three Lambda binaries
make lambda.test     # Run tests with coverage
make lint            # Run golangci-lint
make check           # All checks (lint + vet + test)
```

## IaC

Infrastructure lives in `infra/` with two deployment options:

- **Terraform/OpenTofu**: `infra/terraform/` — deploy with `cd infra/terraform && tofu init && tofu plan && tofu apply`
- **CloudFormation**: `infra/cloudformation/template.yaml` — deploy with `aws cloudformation deploy`

See `docs/getting-started-terraform.md` and `docs/getting-started-cloudformation.md` for step-by-step guides.

## CI

- `test.yml`: Go test + lint on PRs
- `release.yml`: GoReleaser produces 3 Lambda zip archives on tag push
