#!/bin/bash
set -euo pipefail

# jit-runners: GCE image provisioning orchestrator.
# Calls numbered sub-scripts in order to build a pre-baked runner GCE
# image with an ubuntu-latest-like toolchain on Ubuntu 24.04 LTS.
#
# Environment variables (set by Packer):
#   RUNNER_VERSION       — GitHub Actions runner version (default: 2.336.0)
#   GO_VERSION           — Go version to install (default: 1.23.6)
#   NODE_MAJOR           — Node.js major version / LTS (default: 22)
#   JIT_RUNNERS_VERSION  — jit-runners project version (default: dev)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=== jit-runners: starting GCE image provisioning ==="

# Execution order: system deps (01) first, then runner user (06) before
# Docker (02) which adds the user to the docker group. Ops Agent (08)
# after cleanup config files to avoid being purged.

for script in \
  "${SCRIPT_DIR}/01-system-base.sh" \
  "${SCRIPT_DIR}/06-runner-agent.sh" \
  "${SCRIPT_DIR}/02-docker.sh" \
  "${SCRIPT_DIR}/03-languages.sh" \
  "${SCRIPT_DIR}/04-cloud-tools.sh" \
  "${SCRIPT_DIR}/05-cli-tools.sh" \
  "${SCRIPT_DIR}/08-ops-agent.sh" \
  "${SCRIPT_DIR}/07-cleanup.sh"; do

  echo ""
  echo "========================================"
  echo "=== jit-runners: running $(basename "$script")"
  echo "========================================"
  bash "$script"
done

echo ""
echo "=== jit-runners: all provisioning scripts complete ==="
