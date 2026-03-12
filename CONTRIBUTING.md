# Contributing to jit-runners

Thank you for your interest in contributing. We welcome contributions and encourage you to open an issue or pull request.

By participating, you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).

## Getting started

1. Fork the repository and clone your fork.
2. Create a branch from `main` for your changes. Optionally follow the [branch naming conventions](#branch-naming-and-pr-labels) below so the labeler can auto-apply PR labels (and release-note categories).
3. Set up your environment and run the project locally. The Go module for Lambda functions lives in `lambda/` — use the Go version from [lambda/go.mod](lambda/go.mod). Run `make lambda.build` to build and `make lambda.test` to run tests.

## How to contribute

### Issues

- **Bug reports** and **feature requests**: Use the [issue templates](.github/ISSUE_TEMPLATE/) when opening a new issue. Choose "Bug report" or "Feature request" as appropriate.
- Search [existing issues](https://github.com/devopsfactory-io/jit-runners/issues) first to avoid duplicates.
- For **security vulnerabilities**, do not open a public issue. Please report them as described in our [Security Policy](SECURITY.md) (e.g. via [GitHub Security Advisories](https://github.com/devopsfactory-io/jit-runners/security/advisories/new)).

### Pull requests

- **Sign-off (DCO)**: Your commits must comply with the [Developer Certificate of Origin (DCO)](https://developercertificate.org/). The [DCO bot](https://github.com/apps/dco) is enabled on this repository and will check that every commit has a `Signed-off-by` line matching the commit author.
  - **Git config**: Set your name and email so the sign-off is valid: `git config user.name "Your Name"` and `git config user.email "you@example.com"` (use `--global` to set for all repos).
  - **Adding sign-off**: Use `git commit -s` to add the line automatically. If you forgot, run `git commit --amend -s --no-edit` on the last commit.
  - The DCO bot will comment on the PR with the status; fix any unsigned commits (e.g. amend and force-push) before merge.
- Use the [pull request template](.github/pull_request_template.md) if present. Link related issues (e.g. `Fixes #123` or `Relates to #456`).
- Complete the checklist before requesting review: breaking changes (if any), documentation updated, tests added or updated.

### Branch naming and PR labels

The labeler uses the **head branch name** (and changed files) to auto-apply labels. Those labels drive release-note categories. To get the right label automatically, name your branch as follows:

| Branch name pattern | Label applied |
|---------------------|---------------|
| Starts with `feat` (e.g. `feat/add-thing`) | `feature` |
| Starts with `enhance` (e.g. `enhance/improve-x`) | `enhancement` |
| Starts with `fix` (e.g. `fix/bug-123`) | `bug` |
| Contains `!` (e.g. `feat!/breaking`) | `breaking-change` |
| Starts with `ci` (e.g. `ci/update-workflow`) | `github-actions` |
| Contains `(deps)` (e.g. `(deps)/go-mod`) | `dependencies` |

## Code and documentation

- **Code style**: Format with `gofmt -s` and run `make check-fmt` before committing. Linting follows [.golangci.yml](.golangci.yml).
- **Errors**: Wrap errors with context — `fmt.Errorf("context: %w", err)`. Never silently ignore errors.
- **Tests**: Place `*_test.go` files in the same package as the code. Use table-driven tests.
- **Documentation**: When you change behavior, configuration, or IaC, update the relevant docs: [README.md](README.md), [docs/](docs/), and/or [AGENTS.md](AGENTS.md) as appropriate.
- **IaC**: Infrastructure lives in `infra/` (CloudFormation, Terraform, Packer). Update parameter docs and getting-started guides when adding or changing parameters.

## CI

Pull requests must pass all checks. Run these locally before pushing:

```bash
make check       # lint + vet + tests
make check-fmt   # gofmt formatting check
```

Individual targets:

```bash
make lambda.build   # compile all three Lambda binaries
make lambda.test    # run tests with coverage
make lint           # run golangci-lint
```
