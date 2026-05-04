#!/bin/bash
set -euo pipefail

# jit-runners: GitHub Actions runner agent download and setup.

RUNNER_VERSION="${RUNNER_VERSION:-2.332.0}"

echo "=== jit-runners: creating runner user ==="
sudo useradd -m -s /bin/bash runner || true

echo "=== jit-runners: downloading runner v${RUNNER_VERSION} ==="
sudo mkdir -p /home/runner/actions-runner
RUNNER_URL="https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz"
sudo curl -fsSL -o /home/runner/actions-runner/runner.tar.gz "${RUNNER_URL}"
sudo curl -fsSL -o /tmp/runner.tar.gz.sha256 "${RUNNER_URL}.sha256"
expected_hash=$(awk '{print $1}' /tmp/runner.tar.gz.sha256)
actual_hash=$(sudo sha256sum /home/runner/actions-runner/runner.tar.gz | awk '{print $1}')
if [ "${expected_hash}" != "${actual_hash}" ]; then
  echo "ERROR: checksum mismatch for runner tarball" >&2
  exit 1
fi
sudo rm -f /tmp/runner.tar.gz.sha256
sudo tar xzf /home/runner/actions-runner/runner.tar.gz -C /home/runner/actions-runner
sudo rm -f /home/runner/actions-runner/runner.tar.gz
sudo chown -R runner:runner /home/runner/actions-runner

echo "=== jit-runners: runner agent v${RUNNER_VERSION} installed ==="
