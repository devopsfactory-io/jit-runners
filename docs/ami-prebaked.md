# Pre-baked Runner AMI

jit-runners provides a pre-baked AMI that eliminates ~40-50 seconds of boot-time setup by pre-installing dependencies, the runner user, and the GitHub Actions runner agent. Instances using this AMI skip directly to JIT configuration and job execution.

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

### Build (source region only)

```bash
make ami.build
```

Builds an AMI in `us-east-2` with runner version `2.332.0` (default). Override the version:

```bash
make ami.build RUNNER_VERSION=2.335.0
```

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
| `aws_region` | `us-east-2` | Region to build the AMI in |
| `ami_regions` | `[]` | Additional regions to copy the AMI to |
| `instance_type` | `t3.medium` | Instance type for the build instance |
| `extra_script` | `""` | Path to an extra setup script (see below) |
| `ami_name_prefix` | `jit-runner` | Prefix for the AMI name |
| `subnet_id` | `""` | Subnet for the build instance (uses default VPC if empty) |

Pass variables with `-var`:

```bash
cd infra/packer && packer build \
  -var "runner_version=2.335.0" \
  -var "instance_type=t3.large" \
  .
```

## Extra setup scripts

You can extend the AMI with additional packages or configuration by providing an `extra_script`. This script runs after the base setup (dependencies, runner user, runner agent).

### Create a script

Create a shell script in `infra/packer/scripts/`:

```bash
#!/bin/bash
set -euo pipefail

# Example: install Docker and Node.js for your workflows
sudo dnf install -y docker
sudo systemctl enable docker

# Install Node.js
curl -fsSL https://rpm.nodesource.com/setup_20.x | sudo bash -
sudo dnf install -y nodejs
```

### Build with the extra script

```bash
cd infra/packer && packer build \
  -var "runner_version=2.332.0" \
  -var "extra_script=scripts/my-custom-setup.sh" \
  .
```

Or via the CI workflow (workflow_dispatch), set the `extra_script` input to the script path relative to `infra/packer/`.

## How it works

### Marker file detection

The AMI contains a marker file at `/opt/jit-runner-prebaked` with the pre-installed runner version (e.g., `2.332.0`).

At boot, the EC2 user-data script checks for this file:

```
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

- System packages: `libicu`, `lttng-ust`, `openssl-libs`, `krb5-libs`, `zlib`, `git`, `make`, `tar`, `gzip`, `unzip`
- User: `runner` with home at `/home/runner`
- Runner agent: extracted at `/home/runner/actions-runner/`
- Marker file: `/opt/jit-runner-prebaked` containing the version string

The `git make tar gzip unzip` packages were added to ship a complete CI toolchain in the AMI, avoiding per-job installation of these common utilities.

## CI workflow

The `.github/workflows/ami-build.yml` workflow builds AMIs automatically:

- **Trigger**: `workflow_dispatch` (manual) or push to `main` when `infra/packer/**` changes
- **Inputs**: `runner_version`, `extra_script`, `distribute` (boolean)
- **Auth**: OIDC role assumption via `AMI_BUILD_ROLE_ARN` secret
- **Output**: AMI ID in the GitHub Actions job summary

## Multi-region cost

Each additional region incurs:
- **One-time**: ~$0.06-0.08 cross-region data transfer per region (~3-4 GB AMI)
- **Monthly**: ~$0.15-0.20/month EBS snapshot storage per region

With all 9 distribution regions: ~$0.60 one-time + ~$1.50-1.80/month.
