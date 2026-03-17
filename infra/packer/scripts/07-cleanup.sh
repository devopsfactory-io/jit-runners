#!/bin/bash
set -euo pipefail

# jit-runners: Post-provisioning cleanup, manifest generation, and marker file.

RUNNER_VERSION="${RUNNER_VERSION:-2.332.0}"
JIT_RUNNERS_VERSION="${JIT_RUNNERS_VERSION:-dev}"

echo "=== jit-runners: cleaning up ==="
sudo dnf clean all
sudo rm -rf /var/cache/dnf /tmp/*

# Write pre-baked marker (used by user-data to skip setup)
echo "${RUNNER_VERSION}" | sudo tee /opt/jit-runner-prebaked > /dev/null

# Write a manifest of installed tools for debugging and verification
sudo tee /opt/jit-runner-manifest.txt > /dev/null <<MANIFEST
jit-runner-prebaked AMI manifest
jit_runners_version: ${JIT_RUNNERS_VERSION}
runner_version: ${RUNNER_VERSION}
build_date: $(date -u +%Y-%m-%dT%H:%M:%SZ)
---
git: $(git --version 2>/dev/null || echo 'not installed')
gh: $(gh --version 2>/dev/null | head -1 || echo 'not installed')
docker: $(docker --version 2>/dev/null || echo 'not installed')
python3: $(python3 --version 2>/dev/null || echo 'not installed')
node: $(node --version 2>/dev/null || echo 'not installed')
go: $(/usr/local/go/bin/go version 2>/dev/null || echo 'not installed')
aws: $(aws --version 2>/dev/null || echo 'not installed')
kubectl: $(kubectl version --client -o json 2>/dev/null | jq -r '.clientVersion.gitVersion' || echo 'not installed')
helm: $(helm version --short 2>/dev/null || echo 'not installed')
jq: $(jq --version 2>/dev/null || echo 'not installed')
yq: $(yq --version 2>/dev/null || echo 'not installed')
git-lfs: $(git lfs version 2>/dev/null || echo 'not installed')
cmake: $(cmake --version 2>/dev/null | head -1 || echo 'not installed')
gcc: $(gcc --version 2>/dev/null | head -1 || echo 'not installed')
make: $(make --version 2>/dev/null | head -1 || echo 'not installed')
MANIFEST

echo "=== jit-runners: AMI setup complete (runner v${RUNNER_VERSION}) ==="
