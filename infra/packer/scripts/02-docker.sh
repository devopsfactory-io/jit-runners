#!/bin/bash
set -euo pipefail

# jit-runners: Docker Engine, Compose v2 plugin, and Buildx plugin.
# AL2023 ships Docker 25.x in its own repos. We use that and add
# Compose v2 + Buildx as CLI plugins from GitHub releases.

echo "=== jit-runners: installing Docker ==="

# AL2023 already includes Docker in its repos (25.x).
# Remove any pre-installed version first to do a clean install.
sudo dnf remove -y docker 2>/dev/null || true
sudo dnf install -y docker

# Enable Docker service (starts on boot)
sudo systemctl enable docker

# Install Docker Compose v2 as a CLI plugin
echo "=== jit-runners: installing Docker Compose v2 ==="
COMPOSE_VERSION=$(curl -fsSL https://api.github.com/repos/docker/compose/releases/latest | jq -er .tag_name)
sudo mkdir -p /usr/local/lib/docker/cli-plugins
sudo curl -fsSL "https://github.com/docker/compose/releases/download/${COMPOSE_VERSION}/docker-compose-linux-x86_64" \
  -o /usr/local/lib/docker/cli-plugins/docker-compose
sudo chmod +x /usr/local/lib/docker/cli-plugins/docker-compose

# Install Docker Buildx as a CLI plugin
echo "=== jit-runners: installing Docker Buildx ==="
BUILDX_VERSION=$(curl -fsSL https://api.github.com/repos/docker/buildx/releases/latest | jq -er .tag_name)
sudo curl -fsSL "https://github.com/docker/buildx/releases/download/${BUILDX_VERSION}/buildx-${BUILDX_VERSION}.linux-amd64" \
  -o /usr/local/lib/docker/cli-plugins/docker-buildx
sudo chmod +x /usr/local/lib/docker/cli-plugins/docker-buildx

# Add runner user to docker group so workflows don't need sudo
sudo usermod -aG docker runner 2>/dev/null || true

echo "=== jit-runners: Docker $(docker --version) installed ==="
