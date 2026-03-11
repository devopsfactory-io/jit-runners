package ec2

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"text/template"
)

const userdataTemplate = `#!/bin/bash
set -euo pipefail

# jit-runners: EC2 user-data script for ephemeral GitHub Actions runner

RUNNER_VERSION="{{.RunnerVersion}}"
JIT_CONFIG="{{.JITConfig}}"
INSTANCE_ID=$(curl -sf http://169.254.169.254/latest/meta-data/instance-id)

echo "=== jit-runners: configuring ephemeral runner on ${INSTANCE_ID} ==="

# Create runner user
useradd -m -s /bin/bash runner || true

# Download and extract runner
cd /home/runner
mkdir -p actions-runner && cd actions-runner
curl -sL "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz" | tar xz

# Set ownership
chown -R runner:runner /home/runner/actions-runner

# Start the runner with JIT config (runs one job, then exits)
su - runner -c "cd /home/runner/actions-runner && ./run.sh --jitconfig ${JIT_CONFIG}" || true

echo "=== jit-runners: runner finished, terminating instance ==="

# Self-terminate
aws ec2 terminate-instances --instance-ids "${INSTANCE_ID}" --region "$(curl -sf http://169.254.169.254/latest/meta-data/placement/region)" || true
`

// UserDataParams contains the parameters for the user-data script template.
type UserDataParams struct {
	RunnerVersion string
	JITConfig     string
}

// GenerateUserData renders the user-data script and returns it base64-encoded.
func GenerateUserData(params *UserDataParams) (string, error) {
	if params.RunnerVersion == "" {
		return "", fmt.Errorf("runner version is required")
	}
	if params.JITConfig == "" {
		return "", fmt.Errorf("JIT config is required")
	}

	tmpl, err := template.New("userdata").Parse(userdataTemplate)
	if err != nil {
		return "", fmt.Errorf("parse userdata template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("execute userdata template: %w", err)
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
