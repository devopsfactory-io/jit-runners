#!/bin/bash
set -euo pipefail

# jit-runners: Cloud CLI tools — gcloud SDK, kubectl, Helm.

# --- gcloud SDK ---
# Install via the Google Cloud apt repo. cloud-cli ships gcloud + bq + gsutil.
echo "=== jit-runners: installing gcloud SDK ==="
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg \
  | sudo gpg --dearmor -o /etc/apt/keyrings/cloud.google.gpg
echo "deb [signed-by=/etc/apt/keyrings/cloud.google.gpg] \
  https://packages.cloud.google.com/apt cloud-sdk main" \
  | sudo tee /etc/apt/sources.list.d/google-cloud-sdk.list > /dev/null

sudo DEBIAN_FRONTEND=noninteractive apt-get update -y
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y google-cloud-cli

# --- kubectl (latest stable, same source as AWS) ---
echo "=== jit-runners: installing kubectl ==="
KUBECTL_VERSION=$(curl -sSL https://dl.k8s.io/release/stable.txt)
sudo curl -sSL "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl" \
  -o /usr/local/bin/kubectl
sudo chmod +x /usr/local/bin/kubectl

# --- Helm 3 (cloud-agnostic install script) ---
echo "=== jit-runners: installing Helm ==="
curl -sSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | sudo bash

echo "=== jit-runners: cloud tools installed ==="
