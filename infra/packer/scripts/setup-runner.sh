#!/bin/bash
set -euo pipefail

# jit-runners: Pre-bake AMI with GitHub Actions runner dependencies
# This script is run by Packer during AMI build.

RUNNER_VERSION="${RUNNER_VERSION:-2.332.0}"

echo "=== jit-runners: installing runner dependencies ==="
sudo dnf install -y libicu lttng-ust openssl-libs krb5-libs zlib git make tar gzip unzip

echo "=== jit-runners: creating runner user ==="
sudo useradd -m -s /bin/bash runner || true

echo "=== jit-runners: downloading runner v${RUNNER_VERSION} ==="
sudo mkdir -p /home/runner/actions-runner
sudo curl -sL -o /home/runner/actions-runner/runner.tar.gz \
  "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz"
sudo tar xzf /home/runner/actions-runner/runner.tar.gz -C /home/runner/actions-runner
sudo rm -f /home/runner/actions-runner/runner.tar.gz
sudo chown -R runner:runner /home/runner/actions-runner

echo "=== jit-runners: writing marker file ==="
echo "${RUNNER_VERSION}" | sudo tee /opt/jit-runner-prebaked > /dev/null

echo "=== jit-runners: AMI setup complete (runner v${RUNNER_VERSION}) ==="
