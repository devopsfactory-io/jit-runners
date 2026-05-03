#!/bin/bash
set -euo pipefail

# jit-runners: Developer CLI tools — gh, jq, yq, git-lfs, yamllint.

# --- GitHub CLI (deb repo) ---
echo "=== jit-runners: installing GitHub CLI ==="
sudo mkdir -p -m 755 /etc/apt/keyrings
out=$(mktemp) && wget -nv -O"$out" https://cli.github.com/packages/githubcli-archive-keyring.gpg
sudo cat "$out" | sudo tee /etc/apt/keyrings/githubcli-archive-keyring.gpg > /dev/null
sudo chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] \
  https://cli.github.com/packages stable main" \
  | sudo tee /etc/apt/sources.list.d/github-cli.list > /dev/null
sudo DEBIAN_FRONTEND=noninteractive apt-get update -y
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y gh

# --- git-lfs ---
echo "=== jit-runners: installing git-lfs ==="
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y git-lfs
sudo git lfs install --system

# --- jq ---
echo "=== jit-runners: installing jq ==="
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y jq

# --- yq (not in apt; install from GitHub release) ---
echo "=== jit-runners: installing yq ==="
YQ_VERSION="v4.44.6"
sudo curl -sSL "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_amd64" \
  -o /usr/local/bin/yq
sudo chmod +x /usr/local/bin/yq

# --- yamllint (via pip; PEP 668 enforcement on Ubuntu 24.04) ---
echo "=== jit-runners: installing yamllint ==="
sudo python3 -m pip install --break-system-packages yamllint

echo "=== jit-runners: CLI tools installed ==="
