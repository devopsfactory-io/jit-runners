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
curl -fsSL "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_amd64" \
  -o /tmp/yq
# yq's `checksums` file is a multi-algorithm table (one row per asset, one
# column per hash algorithm). Column order is in `checksums_hashes_order`;
# SHA-256 is column 19 of the row (filename in col 1, then 18 hash columns
# before SHA-256: CRC32, MD4, MD5, SHA1, TIGER, TTH, BTIH, ED2K, AICH,
# WHIRLPOOL, RIPEMD-160, GOST94, GOST94-CRYPTOPRO, HAS-160, GOST12-256,
# GOST12-512, SHA-224, then SHA-256).
YQ_SHA256=$(curl -fsSL \
  "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/checksums" \
  | awk '$1 == "yq_linux_amd64" {print $19}')
if [ -z "${YQ_SHA256}" ]; then
  echo "ERROR: could not extract SHA-256 for yq_linux_amd64 from yq ${YQ_VERSION} checksums file" >&2
  exit 1
fi
echo "${YQ_SHA256}  /tmp/yq" | sha256sum -c -
sudo install -m 755 /tmp/yq /usr/local/bin/yq
rm -f /tmp/yq

# --- yamllint (via pip; PEP 668 enforcement on Ubuntu 24.04) ---
echo "=== jit-runners: installing yamllint ==="
sudo python3 -m pip install --break-system-packages yamllint

echo "=== jit-runners: CLI tools installed ==="
