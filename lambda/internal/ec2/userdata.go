package ec2

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"text/template"
)

const userdataTemplate = `#!/bin/bash
set -euo pipefail

RUNNER_VERSION="{{.RunnerVersion}}"
JIT_CONFIG="{{.JITConfig}}"

# IMDSv2 token-based metadata access
IMDS_TOKEN=$(curl -sf -X PUT "http://169.254.169.254/latest/api/token" -H "X-aws-ec2-metadata-token-ttl-seconds: 300")
INSTANCE_ID=$(curl -sf -H "X-aws-ec2-metadata-token: ${IMDS_TOKEN}" http://169.254.169.254/latest/meta-data/instance-id)
REGION=$(curl -sf -H "X-aws-ec2-metadata-token: ${IMDS_TOKEN}" http://169.254.169.254/latest/meta-data/placement/region)

echo "=== jit-runners: configuring ephemeral runner on ${INSTANCE_ID} (${REGION}) ==="
echo "Runner version: ${RUNNER_VERSION}"

if [ -f /opt/jit-runner-prebaked ]; then
    PREBAKED_VERSION=$(cat /opt/jit-runner-prebaked)
    echo "=== jit-runners: pre-baked AMI detected (runner v${PREBAKED_VERSION}) ==="
    if [ "${PREBAKED_VERSION}" != "${RUNNER_VERSION}" ]; then
        echo "=== jit-runners: version mismatch, downloading runner v${RUNNER_VERSION} ==="
        cd /home/runner/actions-runner
        curl -sL -o runner.tar.gz "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz"
        tar xzf runner.tar.gz
        rm -f runner.tar.gz
        chown -R runner:runner /home/runner/actions-runner
    fi
else
    echo "=== jit-runners: stock AMI, installing dependencies ==="
    dnf install -y libicu lttng-ust openssl-libs krb5-libs zlib \
        git git-lfs make tar gzip unzip zip curl wget jq \
        openssl gnupg2 openssh-clients procps-ng sudo

    # Install GitHub CLI
    dnf install -y 'dnf-command(config-manager)'
    dnf config-manager --add-repo https://cli.github.com/packages/rpm/gh-cli.repo
    dnf install -y gh

    useradd -m -s /bin/bash runner || true
    cd /home/runner
    mkdir -p actions-runner && cd actions-runner
    curl -sL -o runner.tar.gz "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz"
    tar xzf runner.tar.gz
    rm -f runner.tar.gz
    chown -R runner:runner /home/runner/actions-runner
fi

# Start the runner with JIT config (runs one job, then exits)
echo "Starting runner with JIT config..."
su - runner -c "cd /home/runner/actions-runner && ./run.sh --jitconfig '${JIT_CONFIG}'" 2>&1
RUNNER_EXIT=$?
echo "=== jit-runners: runner exited with code ${RUNNER_EXIT} ==="

echo "=== jit-runners: terminating instance ==="
aws ec2 terminate-instances --instance-ids "${INSTANCE_ID}" --region "${REGION}" || true
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
