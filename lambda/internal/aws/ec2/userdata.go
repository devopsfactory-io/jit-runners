package ec2

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"text/template"
)

// silentFailureThresholdSecs is the wall-clock budget (seconds) below which a
// run.sh exit with no Worker file written is treated as a silent failure
// and emits JIT_NO_JOB_PICKUP. The default 30 is sized below the observed
// minimum healthy job duration (lint job from PR #54: 84s pickup→complete).
// Retune based on healthy-pickup p99 latency once we have data.
const silentFailureThresholdSecs = 30

const userdataTemplate = `#!/bin/bash
set -uo pipefail

RUNNER_VERSION="{{.RunnerVersion}}"
JIT_CONFIG="{{.JITConfig}}"
export RUNNER_ID={{.RunnerID}}
{{if eq .RunnerLogLevel "debug"}}export ACTIONS_RUNNER_DEBUG=true
export ACTIONS_STEP_DEBUG=true
{{end}}
# IMDSv2 token-based metadata access
IMDS_TOKEN=$(curl -sf -X PUT "http://169.254.169.254/latest/api/token" -H "X-aws-ec2-metadata-token-ttl-seconds: 300")
INSTANCE_ID=$(curl -sf -H "X-aws-ec2-metadata-token: ${IMDS_TOKEN}" http://169.254.169.254/latest/meta-data/instance-id)
REGION=$(curl -sf -H "X-aws-ec2-metadata-token: ${IMDS_TOKEN}" http://169.254.169.254/latest/meta-data/placement/region)

echo "=== jit-runners: configuring ephemeral runner on ${INSTANCE_ID} (${REGION}) ==="
echo "Runner version: ${RUNNER_VERSION}"
echo "Runner ID: ${RUNNER_ID}"

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

# Start the CloudWatch agent so runner-agent _diag/* logs and this script's
# stdout (via tee) ship to CloudWatch even if run.sh exits within seconds.
# The agent's log_stream_name does NOT auto-expand shell env vars (only
# its own placeholders like {instance_id}); substitute ${RUNNER_ID} into
# the config file at runtime so the stream resolves to <runner_id>/<instance_id>.
echo "=== jit-runners: starting CloudWatch agent ==="
sed -i "s|\${RUNNER_ID}|${RUNNER_ID}|g" /opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json
if ! systemctl start amazon-cloudwatch-agent; then
    echo "WARN: amazon-cloudwatch-agent failed to start; continuing without remote logs"
fi

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

# Detect silent-failure: runner returned 0 but never wrote a Worker_*.log,
# and the wall-clock budget is below the threshold. Worker file presence is
# the load-bearing signal because the runner forks a worker per job.
JOB_PICKED_UP=1
if compgen -G "/home/runner/actions-runner/_diag/Worker_*.log" >/dev/null; then
    JOB_PICKED_UP=0
fi
if [ "${RUNNER_EXIT}" -eq 0 ] && [ "${RUNTIME_SECS}" -lt {{.SilentFailureThresholdSecs}} ] && [ "${JOB_PICKED_UP}" -ne 0 ]; then
    MSG="JIT_NO_JOB_PICKUP runner_id={{.RunnerID}} runtime=${RUNTIME_SECS}s"
    echo "${MSG}"
    logger -t jit-runners "${MSG}"
    echo "${MSG}" >> /var/log/jit-runner-userdata.log
fi

# Sleep so the CloudWatch agent (default 1s polling) can ship the last lines
# before the instance terminates.
sleep 5

echo "=== jit-runners: terminating instance ==="
aws ec2 terminate-instances --instance-ids "${INSTANCE_ID}" --region "${REGION}" || true
`

// UserDataParams contains the parameters for the user-data script template.
type UserDataParams struct {
	RunnerVersion string
	JITConfig     string
	// RunnerID is the GitHub-assigned int64 runner_id (the partition key
	// for the per-runner DynamoDB record). Plumbed into the userdata so the
	// CloudWatch agent can stamp it into the log stream name.
	RunnerID int64
	// RunnerLogLevel is "info" or "debug". When "debug", the userdata
	// exports ACTIONS_RUNNER_DEBUG=true and ACTIONS_STEP_DEBUG=true so the
	// runner agent emits debug-level logs. Empty string is treated as "info".
	RunnerLogLevel string
}

// GenerateUserData renders the user-data script and returns it base64-encoded.
func GenerateUserData(params *UserDataParams) (string, error) {
	if params.RunnerVersion == "" {
		return "", fmt.Errorf("runner version is required")
	}
	if params.JITConfig == "" {
		return "", fmt.Errorf("JIT config is required")
	}
	if params.RunnerID == 0 {
		return "", fmt.Errorf("runner id is required")
	}

	tmpl, err := template.New("userdata").Parse(userdataTemplate)
	if err != nil {
		return "", fmt.Errorf("parse userdata template: %w", err)
	}

	var buf bytes.Buffer
	data := struct {
		*UserDataParams
		SilentFailureThresholdSecs int
	}{
		UserDataParams:             params,
		SilentFailureThresholdSecs: silentFailureThresholdSecs,
	}
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute user-data template: %w", err)
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
