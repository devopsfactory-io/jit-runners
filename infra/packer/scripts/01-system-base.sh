#!/bin/bash
set -euo pipefail

# jit-runners: System packages, build tools, and compression utilities.
# Mirrors the baseline available on GitHub's ubuntu-latest runner image.

echo "=== jit-runners: installing system packages ==="

# Runner runtime dependencies
sudo dnf install -y libicu lttng-ust openssl-libs krb5-libs zlib

# Core utilities
sudo dnf install -y \
  git make tar gzip unzip zip bzip2 xz zstd lz4 \
  curl wget rsync tree findutils which diffutils patch \
  procps-ng sudo shadow-utils \
  openssl gnupg2 openssh-clients

# Build tools (gcc, g++, autoconf, automake, etc.)
echo "=== jit-runners: installing development tools ==="
sudo dnf groupinstall -y "Development Tools"
sudo dnf install -y cmake

echo "=== jit-runners: system packages complete ==="
