#!/bin/bash
set -euo pipefail

# jit-runners: System packages, build tools, and compression utilities.
# Mirrors the baseline available on GitHub's ubuntu-latest runner image.

echo "=== jit-runners: installing system packages ==="

# Update apt index once
sudo DEBIAN_FRONTEND=noninteractive apt-get update -y

# Runner runtime dependencies (mirrors the AL2023 list: libicu, lttng-ust,
# openssl-libs, krb5-libs, zlib).
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y \
  libicu74 \
  liblttng-ust1t64 \
  libssl3t64 \
  libkrb5-3 \
  zlib1g

# Core utilities (parity with aws/01-system-base.sh).
# jq is needed early by 03-languages.sh (Go checksum lookup) — it runs
# before 05-cli-tools.sh which would otherwise own jq installation.
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y \
  curl wget jq \
  git make tar gzip unzip zip bzip2 xz-utils zstd lz4 \
  rsync tree findutils diffutils patch \
  procps sudo passwd \
  openssl openssh-client \
  ca-certificates gnupg2 software-properties-common

# Build tools (gcc, g++, autoconf, etc.).
echo "=== jit-runners: installing development tools ==="
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y \
  build-essential \
  cmake \
  pkg-config

echo "=== jit-runners: system packages complete ==="
