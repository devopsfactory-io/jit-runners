#!/bin/bash
set -euo pipefail

# jit-runners: Docker Engine 27.x, Compose v2, Buildx.
# Uses the official Docker apt repository (download.docker.com), which is
# the Docker-blessed install path on Ubuntu and tracks current upstream.

echo "=== jit-runners: installing Docker ==="

# Remove any pre-installed docker.* packages from the base image.
sudo DEBIAN_FRONTEND=noninteractive apt-get remove -y \
  docker docker-engine docker.io containerd runc 2>/dev/null || true

# Add Docker's official GPG key.
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
  -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc

# Add the Docker repo for our Ubuntu codename.
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] \
  https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "${VERSION_CODENAME}") stable" \
  | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

sudo DEBIAN_FRONTEND=noninteractive apt-get update -y
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y \
  docker-ce docker-ce-cli containerd.io \
  docker-buildx-plugin docker-compose-plugin

# Enable Docker service (starts on boot).
sudo systemctl enable docker

# Add runner user to docker group so workflows don't need sudo.
sudo usermod -aG docker runner 2>/dev/null || true

echo "=== jit-runners: Docker $(docker --version) installed ==="
