# Security Policy

## Supported Versions

We provide security updates for the current release. See the [Releases](https://github.com/devopsfactory-io/jit-runners/releases) page for version history.

| Version | Supported          |
| ------- | ------------------ |
| Latest release (see [Releases](https://github.com/devopsfactory-io/jit-runners/releases)) | :white_check_mark: |
| Older releases | :x: |

## Reporting a Vulnerability

We take security seriously. If you believe you have found a security vulnerability, please report it responsibly.

**Preferred method:** Use [GitHub Security Advisories](https://github.com/devopsfactory-io/jit-runners/security/advisories/new) (private disclosure). This keeps the report confidential until a fix is ready.

**Alternative:** Email the maintainers listed in [MAINTAINERS.md](MAINTAINERS.md) (use the contact method they provide, if any). Do not open a public issue for security vulnerabilities.

### What to include

- Description of the problem
- Steps to reproduce (as precise as possible)
- Affected version(s)
- Possible mitigations, if known

### What to expect

- We will acknowledge receipt within a few business days.
- We may follow up to clarify or confirm the issue.
- We will work on a fix and coordinate disclosure (e.g. release + advisory) when appropriate.

We do not have a formal embargo policy; we handle disclosure in a reasonable, coordinated way.

## Security Considerations

jit-runners orchestrates ephemeral EC2 instances in response to GitHub webhook events. Keep the following in mind when deploying:

- **Webhook HMAC secret**: Stored in AWS Secrets Manager. Rotate it immediately if compromised and update the GitHub App webhook configuration to match.
- **GitHub App private key**: Scoped to the minimum required permissions (Actions: Read, Administration: Read/Write). Store it in Secrets Manager, not in environment variables or source control.
- **IAM instance profile**: EC2 runner instances use an IAM role to self-terminate after job completion. Follow least-privilege — the instance profile should grant only `ec2:TerminateInstances` (scoped to self) and nothing else unless your workflows explicitly require additional AWS access.
- **Ephemeral, single-use runners**: Each runner handles exactly one job and self-terminates. No credentials or artifacts persist between runs.
- **Spot/on-demand fallback**: The scale-up Lambda automatically retries with on-demand if a spot request fails. Ensure your IAM policy allows both `ec2:RequestSpotInstances` and `ec2:RunInstances`.
- **Network isolation**: Runners launch inside your VPC. Restrict security group egress to only what your workflows need (e.g. GitHub API, package registries).

For deployment and hardening guidance, see [docs/getting-started-cloudformation.md](docs/getting-started-cloudformation.md) and [docs/getting-started-terraform.md](docs/getting-started-terraform.md).
