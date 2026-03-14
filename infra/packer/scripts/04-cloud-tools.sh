#!/bin/bash
set -euo pipefail

# jit-runners: Cloud CLI tools — AWS CLI v2, kubectl, Helm.

# --- AWS CLI v2 ---
echo "=== jit-runners: installing AWS CLI v2 ==="
curl -sSL "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o /tmp/awscliv2.zip
unzip -q /tmp/awscliv2.zip -d /tmp
sudo /tmp/aws/install
rm -rf /tmp/aws /tmp/awscliv2.zip

# --- kubectl (latest stable) ---
echo "=== jit-runners: installing kubectl ==="
KUBECTL_VERSION=$(curl -sSL https://dl.k8s.io/release/stable.txt)
sudo curl -sSL "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl" \
  -o /usr/local/bin/kubectl
sudo chmod +x /usr/local/bin/kubectl

# --- Helm 3 ---
echo "=== jit-runners: installing Helm ==="
curl -sSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | sudo bash

echo "=== jit-runners: cloud tools installed ==="
