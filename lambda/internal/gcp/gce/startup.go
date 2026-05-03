// Package gce provides a GCP Compute Engine-backed implementation of
// compute.Launcher and the startup-script template that runs on each
// ephemeral runner VM.
package gce

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"text/template"
)

// StartupScriptParams mirrors awsec2.UserDataParams in shape but is GCP-specific.
type StartupScriptParams struct {
	RunnerVersion string
	JITConfig     string
	// RunnerID is the GitHub-assigned int64 runner_id (the partition key for
	// the per-runner runner-store record). Surfaced as an env var so the
	// startup script can include it in log output.
	RunnerID int64
}

const startupTemplate = `#!/bin/bash
set -uo pipefail

RUNNER_VERSION="{{.RunnerVersion}}"
JIT_CONFIG="{{.JITConfig}}"
export RUNNER_ID={{.RunnerID}}

# GCP metadata server: single GET with Metadata-Flavor: Google header.
# No IMDSv2 token exchange required (unlike AWS).
INSTANCE_ID=$(curl -sf -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/id)
INSTANCE_NAME=$(curl -sf -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/name)
INSTANCE_ZONE=$(curl -sf -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/zone | awk -F/ '{print $NF}')

echo "=== jit-runners: configuring ephemeral runner on ${INSTANCE_NAME} (${INSTANCE_ZONE}) ==="
echo "Runner version: ${RUNNER_VERSION}"
echo "Runner ID: ${RUNNER_ID}"

# Install dependencies (Ubuntu 24.04 / apt-based).
apt-get update -y
apt-get install -y curl tar gzip jq git unzip wget gnupg2 openssh-client ca-certificates

# Install GitHub CLI.
mkdir -p -m 755 /etc/apt/keyrings
out=$(mktemp) && wget -nv -O$out https://cli.github.com/packages/githubcli-archive-keyring.gpg && cat $out | tee /etc/apt/keyrings/githubcli-archive-keyring.gpg > /dev/null
chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | tee /etc/apt/sources.list.d/github-cli.list > /dev/null
apt-get update -y
apt-get install -y gh

# Create runner user.
useradd -m -s /bin/bash runner || true

# Download and install runner agent.
cd /home/runner
mkdir -p actions-runner && cd actions-runner
curl -sL -o runner.tar.gz "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz"
tar xzf runner.tar.gz
rm -f runner.tar.gz
chown -R runner:runner /home/runner/actions-runner

# Start the runner with JIT config (runs one job, then exits).
echo "Starting runner with JIT config..."
START_TIME=$(date +%s)
set +e
su - runner -c "cd /home/runner/actions-runner && ./run.sh --jitconfig '${JIT_CONFIG}'" 2>&1 | tee /var/log/jit-runner-userdata.log
RUNNER_EXIT=${PIPESTATUS[0]}
set -e
END_TIME=$(date +%s)
RUNTIME_SECS=$((END_TIME - START_TIME))
echo "=== jit-runners: runner exited with code ${RUNNER_EXIT} after ${RUNTIME_SECS}s ==="

# Sleep so Cloud Logging agent can flush before the instance terminates.
sleep 5

# Self-terminate. The runner SA needs compute.instances.delete on its own
# resource (granted by Phase D Terraform module).
echo "=== jit-runners: terminating instance ==="
gcloud compute instances delete "${INSTANCE_NAME}" --zone "${INSTANCE_ZONE}" --quiet || true
`

// GenerateStartupScript renders the startup-script template and returns it
// base64-encoded. The result is passed via metadata.startup-script when
// calling instances.insert.
func GenerateStartupScript(params *StartupScriptParams) (string, error) {
	if params.RunnerVersion == "" {
		return "", fmt.Errorf("runner version is required")
	}
	if params.JITConfig == "" {
		return "", fmt.Errorf("JIT config is required")
	}
	if params.RunnerID == 0 {
		return "", fmt.Errorf("runner id is required")
	}

	tmpl, err := template.New("startup").Parse(startupTemplate)
	if err != nil {
		return "", fmt.Errorf("parse startup template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("execute startup template: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
