#!/bin/bash
set -euo pipefail

# jit-runners: Language runtimes (Python 3, Node.js LTS, Go).
# These provide a base that actions/setup-* can override with specific versions.

GO_VERSION="${GO_VERSION:-1.23.6}"
NODE_MAJOR="${NODE_MAJOR:-22}"

# --- Python 3 (AL2023 ships python3.11+) ---
echo "=== jit-runners: installing Python 3 ==="
sudo dnf install -y python3 python3-pip python3-devel
sudo python3 -m pip install --upgrade pip setuptools wheel

# --- Node.js LTS via NodeSource ---
echo "=== jit-runners: installing Node.js ${NODE_MAJOR}.x LTS ==="
sudo dnf install -y \
  "https://rpm.nodesource.com/pub_${NODE_MAJOR}.x/nodistro/repo/nodesource-release-nodistro-1.noarch.rpm" \
  || true
sudo dnf install -y nodejs --setopt=nodesource-nodejs.module_hotfixes=1
sudo corepack enable || true

# --- Go ---
echo "=== jit-runners: installing Go ${GO_VERSION} ==="
curl -sSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" \
  | sudo tar -C /usr/local -xzf -

# Make Go available system-wide
cat <<'GOPATH' | sudo tee /etc/profile.d/go.sh > /dev/null
export PATH=$PATH:/usr/local/go/bin
GOPATH

echo "=== jit-runners: language runtimes installed ==="
