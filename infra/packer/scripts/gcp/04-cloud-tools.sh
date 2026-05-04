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
KUBECTL_VERSION=$(curl -fsSL https://dl.k8s.io/release/stable.txt)
sudo curl -fsSL "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl" \
  -o /usr/local/bin/kubectl

# Verify SHA256 before installing
curl -fsSL "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl.sha256" \
  -o /tmp/kubectl.sha256
echo "$(cat /tmp/kubectl.sha256)  /usr/local/bin/kubectl" | sha256sum --check
rm -f /tmp/kubectl.sha256

sudo chmod +x /usr/local/bin/kubectl

# --- Helm 3 (pinned version, verified tarball) ---
echo "=== jit-runners: installing Helm ==="
HELM_VERSION="v3.17.3"
HELM_ARCH="linux-amd64"
HELM_TAR="helm-${HELM_VERSION}-${HELM_ARCH}.tar.gz"
# Download with the upstream filename so the sha256sum sidecar's relative
# filename column resolves correctly under cd /tmp.
curl -fsSL "https://get.helm.sh/${HELM_TAR}" -o "/tmp/${HELM_TAR}"
curl -fsSL "https://get.helm.sh/${HELM_TAR}.sha256sum" -o "/tmp/${HELM_TAR}.sha256sum"
(cd /tmp && sha256sum --check "${HELM_TAR}.sha256sum")
tar -xzf "/tmp/${HELM_TAR}" -C /tmp
sudo install -m 0755 "/tmp/${HELM_ARCH}/helm" /usr/local/bin/helm
rm -rf "/tmp/${HELM_TAR}" "/tmp/${HELM_TAR}.sha256sum" "/tmp/${HELM_ARCH}"

echo "=== jit-runners: cloud tools installed ==="
