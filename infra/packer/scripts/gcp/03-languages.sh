#!/bin/bash
set -euo pipefail

# jit-runners: Language runtimes (Python 3, Node.js LTS, Go).
# Node.js + Go install via official tarballs (cloud-agnostic).
# Python 3 via apt (Ubuntu 24.04 ships python3.12).

GO_VERSION="${GO_VERSION:-1.23.6}"
NODE_MAJOR="${NODE_MAJOR:-22}"

# --- Python 3 (Ubuntu 24.04 ships python3.12) ---
echo "=== jit-runners: installing Python 3 ==="
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y \
  python3 python3-pip python3-dev python3-venv

# --break-system-packages on apt-managed Pythons (PEP 668 enforcement).
# --ignore-installed to skip uninstalling debian-managed packages whose
# RECORD file is missing (e.g. wheel 0.42.0 from Ubuntu 24.04 apt).
sudo python3 -m pip install --upgrade --break-system-packages --ignore-installed \
  pip setuptools wheel

# --- Node.js LTS (official binary tarball from nodejs.org) ---
echo "=== jit-runners: installing Node.js ${NODE_MAJOR}.x LTS ==="

SHASUMS=$(curl -fsSL "https://nodejs.org/dist/latest-v${NODE_MAJOR}.x/SHASUMS256.txt")
NODE_FULL_VERSION=$(echo "${SHASUMS}" \
  | grep -oP 'node-v\K[0-9]+\.[0-9]+\.[0-9]+(?=-linux-x64\.tar\.xz)' | head -1)

if [ -z "${NODE_FULL_VERSION}" ]; then
  echo "ERROR: Could not resolve Node.js ${NODE_MAJOR}.x latest version"
  exit 1
fi

echo "Resolved Node.js v${NODE_FULL_VERSION}"
NODE_TAR="node-v${NODE_FULL_VERSION}-linux-x64.tar.xz"
curl -fsSL "https://nodejs.org/dist/v${NODE_FULL_VERSION}/${NODE_TAR}" -o "/tmp/${NODE_TAR}"
echo "${SHASUMS}" | grep "${NODE_TAR}" | sha256sum -c -
sudo tar -C /usr/local --strip-components=1 -xJf "/tmp/${NODE_TAR}"
rm -f "/tmp/${NODE_TAR}"

node --version
npm --version
sudo corepack enable || true

# --- Go ---
echo "=== jit-runners: installing Go ${GO_VERSION} ==="
GO_TAR="go${GO_VERSION}.linux-amd64.tar.gz"
curl -fsSL "https://go.dev/dl/${GO_TAR}" -o "/tmp/${GO_TAR}"
EXPECTED_SHA=$(curl -fsSL "https://go.dev/dl/?mode=json&include=all" \
  | jq -r ".[] | select(.version==\"go${GO_VERSION}\") \
           | .files[] | select(.filename==\"${GO_TAR}\") | .sha256")
echo "${EXPECTED_SHA}  /tmp/${GO_TAR}" | sha256sum -c -
sudo tar -C /usr/local -xzf "/tmp/${GO_TAR}"
rm -f "/tmp/${GO_TAR}"

# Make Go available system-wide
cat <<'GOPATH' | sudo tee /etc/profile.d/go.sh > /dev/null
export PATH=$PATH:/usr/local/go/bin
GOPATH

echo "=== jit-runners: language runtimes installed ==="
