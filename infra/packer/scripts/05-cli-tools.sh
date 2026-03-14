#!/bin/bash
set -euo pipefail

# jit-runners: Developer CLI tools — gh, jq, yq, git-lfs, yamllint.

# --- GitHub CLI ---
echo "=== jit-runners: installing GitHub CLI ==="
sudo dnf install -y 'dnf-command(config-manager)'
sudo dnf config-manager --add-repo https://cli.github.com/packages/rpm/gh-cli.repo
sudo dnf install -y gh

# --- git-lfs ---
echo "=== jit-runners: installing git-lfs ==="
sudo dnf install -y git-lfs
sudo git lfs install --system

# --- jq ---
echo "=== jit-runners: installing jq ==="
sudo dnf install -y jq

# --- yq (not in AL2023 repos; install from GitHub release) ---
echo "=== jit-runners: installing yq ==="
YQ_VERSION="v4.44.6"
sudo curl -sSL "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_amd64" \
  -o /usr/local/bin/yq
sudo chmod +x /usr/local/bin/yq

# --- yamllint ---
echo "=== jit-runners: installing yamllint ==="
sudo python3 -m pip install yamllint

echo "=== jit-runners: CLI tools installed ==="
