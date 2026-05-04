#!/bin/bash
set -euo pipefail

# jit-runners: Language runtimes (Python 3, Node.js LTS, Go).
# These provide a base that actions/setup-* can override with specific versions.

GO_VERSION="${GO_VERSION:-1.23.6}"
NODE_MAJOR="${NODE_MAJOR:-22}"

# --- Python 3 (AL2023 ships python3.9) ---
echo "=== jit-runners: installing Python 3 ==="
sudo dnf install -y python3 python3-pip python3-devel
# --ignore-installed avoids conflicts with RPM-managed packages (e.g. packaging)
sudo python3 -m pip install --upgrade --ignore-installed pip setuptools wheel

# --- Node.js LTS (official binary tarball from nodejs.org) ---
echo "=== jit-runners: installing Node.js ${NODE_MAJOR}.x LTS ==="

# Resolve the latest LTS version for the requested major
NODE_FULL_VERSION=$(curl -sSL "https://nodejs.org/dist/latest-v${NODE_MAJOR}.x/" \
  | grep -oP 'node-v\K[0-9]+\.[0-9]+\.[0-9]+' | head -1)

if [ -z "${NODE_FULL_VERSION}" ]; then
  echo "ERROR: Could not resolve Node.js ${NODE_MAJOR}.x latest version"
  exit 1
fi

echo "Resolved Node.js v${NODE_FULL_VERSION}"
curl -sSL "https://nodejs.org/dist/v${NODE_FULL_VERSION}/node-v${NODE_FULL_VERSION}-linux-x64.tar.xz" \
  | sudo tar -C /usr/local --strip-components=1 -xJf -

node --version
npm --version
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
