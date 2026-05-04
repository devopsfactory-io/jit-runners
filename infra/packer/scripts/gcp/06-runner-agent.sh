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

# actions/runner does NOT publish per-asset .sha256 sidecar files.
# The SHA-256 lives inside the release body, fenced by HTML comment
# markers like `<!-- BEGIN SHA linux-x64 -->...<!-- END SHA linux-x64 -->`.
# Parse from the GitHub API to verify integrity.
RELEASE_BODY=$(curl -fsSL \
  "https://api.github.com/repos/actions/runner/releases/tags/v${RUNNER_VERSION}" \
  | jq -r '.body')
expected_hash=$(echo "${RELEASE_BODY}" \
  | sed -n 's/.*<!-- BEGIN SHA linux-x64 -->\([0-9a-f]\{64\}\)<!-- END SHA linux-x64 -->.*/\1/p')
if [ -z "${expected_hash}" ]; then
  echo "ERROR: could not parse SHA256 from actions/runner v${RUNNER_VERSION} release body" >&2
  exit 1
fi
actual_hash=$(sudo sha256sum /home/runner/actions-runner/runner.tar.gz | awk '{print $1}')
if [ "${expected_hash}" != "${actual_hash}" ]; then
  echo "ERROR: checksum mismatch for runner tarball" >&2
  echo "  expected: ${expected_hash}" >&2
  echo "  actual:   ${actual_hash}" >&2
  exit 1
fi
sudo tar xzf /home/runner/actions-runner/runner.tar.gz -C /home/runner/actions-runner
sudo rm -f /home/runner/actions-runner/runner.tar.gz
sudo chown -R runner:runner /home/runner/actions-runner

echo "=== jit-runners: runner agent v${RUNNER_VERSION} installed ==="
