# Pre-baked Runner AMI

jit-runners provides a pre-baked AMI that eliminates ~40-50 seconds of boot-time setup by pre-installing dependencies, the runner user, and the GitHub Actions runner agent. Instances using this AMI skip directly to JIT configuration and job execution.

## AMI naming format

AMI names follow the pattern:

```text
jit-runner-{jit_runners_version}-runner{runner_version}-{timestamp}
```

Example: `jit-runner-v0.3.0-runner2.332.0-1773472793`

The corresponding EC2 tags are:

- `Name` — same as the AMI name (without timestamp)
- `runner-version` — GitHub Actions runner version
- `jit-runners-version` — jit-runners project version
- `project` — `jit-runners`
- `source` — `github.com/devopsfactory-io/jit-runners`
- `built-by` — `packer`
- `tools` — comma-separated list of pre-installed tools: `git,docker,python3,node,go,awscli,kubectl,helm,gh,jq,yq,git-lfs,gcc,cmake,make`

PR test builds use the prefix `jit-runner-pr` and are private (not published to the Community AMI catalog). They are automatically deregistered after the build completes.

## Using the published AMI

The AMI is published to the **AWS Community AMI catalog** and can be used by any AWS account.

### Find the AMI

**AWS Console:** EC2 > AMIs > Community AMIs > search `jit-runner`

**AWS CLI:**

```bash
aws ec2 describe-images \
  --filters "Name=name,Values=jit-runner-*" \
  --owners 767000629676 \
  --region us-east-2 \
  --query 'sort_by(Images, &CreationDate)[-1].{ID:ImageId,Name:Name,Created:CreationDate}' \
  --output table
```

### Use with CloudFormation

Pass the AMI ID as the `DefaultAMI` parameter:

```bash
aws cloudformation deploy \
  --template-file infra/cloudformation/template.yaml \
  --stack-name jit-runners \
  --parameter-overrides \
    DefaultAMI="ami-054a333b01986bcf5" \
    ...
```

### Use with Terraform

Set the `default_ami` variable:

```hcl
module "jit-runners" {
  source      = "./infra/terraform"
  default_ami = "ami-054a333b01986bcf5"
  # ...
}
```

## Building your own AMI

If you want to customize the AMI or build for a specific runner version, use Packer.

### Prerequisites

- [Packer](https://www.packer.io/downloads) installed (`brew install packer`)
- AWS credentials configured with EC2 permissions

> **Note:** The Packer source block sets `associate_public_ip_address = true` and `ssh_timeout = "10m"`. The public IP is required so Packer can SSH into the build instance when the default subnet does not auto-assign public IPs. The extended SSH timeout accommodates subnets where IP assignment is slow.

### Build (source region only)

```bash
make ami.build
```

Builds a public AMI in `us-east-2` with runner version `2.332.0` and the version auto-detected from git tags (defaults to `dev` if no tags exist). Override versions:

```bash
make ami.build RUNNER_VERSION=2.335.0
make ami.build RUNNER_VERSION=2.335.0 JIT_RUNNERS_VERSION=v0.3.0
```

### Build a private test AMI

```bash
make ami.build-test
```

Builds a private (non-public) AMI in `us-east-2` — sets `ami_groups=[]` so it is not published to the Community AMI catalog. Useful for validating Packer changes locally before merging. Also passes `JIT_RUNNERS_VERSION` automatically from git.

### Build and distribute to all regions

```bash
make ami.build-distribute
```

Builds in `us-east-2` and copies to: `us-east-1`, `us-west-1`, `us-west-2`, `eu-west-1`, `eu-west-2`, `eu-west-3`, `eu-central-1`, `eu-north-1`, `sa-east-1`.

### Copy an existing AMI to all regions

If you already have an AMI and want to distribute it without rebuilding:

```bash
make ami.copy AMI_ID=ami-054a333b01986bcf5
```

This copies the AMI to all distribution regions, waits for each copy to become available, and makes it public.

### Validate the Packer template

```bash
make ami.validate
```

### Packer variables

| Variable | Default | Description |
|----------|---------|-------------|
| `runner_version` | `2.332.0` | GitHub Actions runner version to pre-install |
| `jit_runners_version` | `dev` | jit-runners project version (e.g. `v0.3.0`). Defaults to `dev` for local builds. Auto-detected from git tags in CI. |
| `aws_region` | `us-east-2` | Region to build the AMI in |
| `ami_regions` | `[]` | Additional regions to copy the AMI to |
| `ami_groups` | `["all"]` | Launch permission groups. Use `["all"]` for public (community AMI), `[]` for private (PR test builds). |
| `instance_type` | `t3.medium` | Instance type for the build instance |
| `extra_script` | `""` | Path to an extra setup script (see below) |
| `ami_name_prefix` | `jit-runner` | Prefix for the AMI name |
| `subnet_id` | `""` | Subnet for the build instance (uses default VPC if empty) |
| `go_version` | `1.23.6` | Go version to pre-install in the AMI |
| `node_major_version` | `22` | Node.js major version (LTS) to pre-install in the AMI |
| `volume_size` | `30` | Root EBS volume size in GB (gp3) |

Pass variables with `-var`:

```bash
cd infra/packer && packer build \
  -var "runner_version=2.335.0" \
  -var "instance_type=t3.large" \
  -var "go_version=1.23.6" \
  -var "node_major_version=22" \
  .
```

## Extra setup scripts

You can extend the AMI with additional packages or configuration by providing an `extra_script`. This script runs after the full base setup (all 7 sub-scripts: system packages, Docker, languages, cloud tools, CLI tools, runner agent, and cleanup). Because Docker, Go, Node.js, AWS CLI, kubectl, Helm, and the GitHub CLI are already present, `extra_script` is best used for tools not in the base toolchain.

### What's already installed (no need to add in extra_script)

| Category | Tools |
| -------- | ----- |
| Container | Docker CE, Docker Compose v2, Docker Buildx |
| Languages | Python 3 + pip, Node.js 22 LTS + npm, Go 1.23.x |
| Cloud | AWS CLI v2, kubectl, Helm 3 |
| CLI | gh, jq, yq, git-lfs, yamllint, curl, wget, rsync, tree |
| Build | gcc, g++, cmake (Development Tools group) |
| Compression | zip, bzip2, xz, zstd, lz4 |
| Runner | GitHub Actions runner agent, runner OS user |

### What's NOT included (install per-workflow or via extra_script)

OpenTofu/Terraform/Terragrunt, Azure CLI, GCP CLI, Java, .NET, Ruby, Podman, Buildah.

### Create a script

Create a shell script in `infra/packer/scripts/`:

```bash
#!/bin/bash
set -euo pipefail

# Example: install Terraform for workflows that need it
sudo dnf install -y yum-utils
sudo yum-config-manager --add-repo https://rpm.releases.hashicorp.com/AmazonLinux/hashicorp.repo
sudo dnf install -y terraform
```

### Build with the extra script

```bash
cd infra/packer && packer build \
  -var "runner_version=2.332.0" \
  -var "extra_script=scripts/my-custom-setup.sh" \
  .
```

The Packer provisioner copies all scripts under `scripts/` to `/tmp/packer-scripts/` on the build instance and then invokes the extra script as `/tmp/packer-scripts/$(basename <extra_script>)`. Only the filename is used on the remote side, so the script must be placed under `infra/packer/scripts/` (or another directory that gets uploaded) and the `extra_script` value must be a path whose basename is unique within that directory.

Or via the CI workflow (workflow_dispatch), set the `extra_script` input to the script path relative to `infra/packer/`.

## How it works

### Marker file detection

The AMI contains a marker file at `/opt/jit-runner-prebaked` with the pre-installed runner version (e.g., `2.332.0`).

At boot, the EC2 user-data script checks for this file:

```text
if /opt/jit-runner-prebaked exists:
    -> pre-baked AMI detected
    -> if version matches requested version: skip setup entirely
    -> if version mismatch: re-download the requested runner version
else:
    -> stock AMI: full install (dependencies, user, runner download)
```

This means:

- **Pre-baked AMI + matching version**: Boot in ~5-10 seconds (just JIT config + start)
- **Pre-baked AMI + version mismatch**: Boot in ~15-20 seconds (re-download runner only)
- **Stock AMI (no marker file)**: Boot in ~50 seconds (full install, backward compatible)

### What's pre-installed

The AMI ships an ubuntu-latest-like toolchain on Amazon Linux 2023, installed by 7 ordered sub-scripts called from `setup-runner.sh`:

| Sub-script | Installs |
| ---------- | -------- |
| `01-system-base.sh` | `libicu`, `lttng-ust`, `openssl-libs`, `krb5-libs`, `zlib`, `git`, `make`, `tar`, `gzip`, `unzip`, and Development Tools (`gcc`, `g++`, `cmake`) |
| `02-docker.sh` | Docker CE, Docker Compose v2, Docker Buildx; `runner` user added to `docker` group |
| `03-languages.sh` | Python 3 + pip, Node.js LTS (downloaded as binary tarball from nodejs.org — not from NodeSource RPM) + npm, Go (`go_version`) |
| `04-cloud-tools.sh` | AWS CLI v2, kubectl (latest stable), Helm 3 |
| `05-cli-tools.sh` | `gh`, `jq`, `yq`, `git-lfs`, `yamllint`, `curl`, `wget`, `rsync`, `tree`, `zip`, `bzip2`, `xz`, `zstd`, `lz4` |
| `06-runner-agent.sh` | `runner` OS user, GitHub Actions runner agent at `/home/runner/actions-runner/`, marker file at `/opt/jit-runner-prebaked`, manifest at `/opt/jit-runner-manifest.txt` |
| `07-cleanup.sh` | DNF cache purge, temp file removal, journal truncation to minimise AMI size; writes final manifest with `jit_runners_version` field |

A validation provisioner runs after all scripts and fails the Packer build if any critical tool is missing (`git`, `docker`, `docker compose`, `docker buildx`, `python3`, `node`, `go`, `aws`, `kubectl`, `helm`, `gh`, `jq`, `yq`, `gcc`, `cmake`, `make`, `git-lfs`).

The manifest file at `/opt/jit-runner-manifest.txt` records all installed tool versions for traceability.

## CI workflow

The `.github/workflows/ami-build.yml` workflow builds AMIs automatically:

- **Runs on**: `ubuntu-latest` (GitHub-hosted runners). The self-hosted runner security group only permits egress on ports 443/80/53 — SSH (port 22) is blocked outbound, which causes Packer to time out when connecting to the build instance. GitHub-hosted runners have unrestricted network access. Using them also avoids the circular dependency of building jit-runner AMIs on the jit-runners infrastructure itself.
- **Triggers**:
  - `workflow_dispatch` (manual)
  - Push to `main` when `infra/packer/**` changes — produces a public, distributable AMI
  - Pull request targeting `main` when `infra/packer/**` changes — produces a private, single-region test AMI that is automatically cleaned up after the build
- **Inputs**: `runner_version`, `go_version`, `node_major_version`, `jit_runners_version` (auto-detected from git tags if empty), `extra_script`, `distribute` (boolean)
- **Version auto-detection**: If `jit_runners_version` input is empty, the workflow uses `git describe --tags --always` to derive the version from the most recent tag. On tag pushes (`refs/tags/v*`), it uses the tag name directly.
- **Auth**: OIDC role assumption via `AMI_BUILD_ROLE_ARN` secret
- **PR builds**: AMI is built with `ami_groups=[]` (private), a `jit-runner-pr` name prefix, and no distribution. A cleanup step deregisters the AMI and deletes its snapshots after the build.
- **Output**: AMI ID, jit-runners version, runner version, Go version, and Node.js version in the GitHub Actions job summary

## Multi-region cost

Each additional region incurs:

- **One-time**: ~$0.06-0.08 cross-region data transfer per region (~3-4 GB AMI)
- **Monthly**: ~$0.15-0.20/month EBS snapshot storage per region

With all 9 distribution regions: ~$0.60 one-time + ~$1.50-1.80/month.
