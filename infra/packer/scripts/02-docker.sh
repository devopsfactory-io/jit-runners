#!/bin/bash
set -euo pipefail

# jit-runners: Docker CE, Compose v2 plugin, and Buildx plugin.
# Uses the Docker CE Fedora repo (compatible with AL2023).

echo "=== jit-runners: installing Docker CE ==="

sudo dnf install -y dnf-plugins-core
sudo dnf config-manager --add-repo https://download.docker.com/linux/fedora/docker-ce.repo

sudo dnf install -y docker-ce docker-ce-cli containerd.io \
  docker-buildx-plugin docker-compose-plugin

# Enable Docker service (starts on boot)
sudo systemctl enable docker

# Add runner user to docker group so workflows don't need sudo
sudo usermod -aG docker runner 2>/dev/null || true

echo "=== jit-runners: Docker $(docker --version) installed ==="
