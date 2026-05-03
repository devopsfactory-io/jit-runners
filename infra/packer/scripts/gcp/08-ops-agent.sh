#!/bin/bash
set -euo pipefail

# jit-runners: Google Cloud Ops Agent install for log + metric forwarding.
# Parallel to AWS's CloudWatch agent. The Ops Agent runs as a system
# service and auto-discovers Cloud Logging + Cloud Monitoring sinks
# from the instance's service account credentials.

echo "=== jit-runners: installing Google Cloud Ops Agent ==="

# Official one-liner install per https://cloud.google.com/logging/docs/agent/ops-agent/install-index
curl -sSO https://dl.google.com/cloudagents/add-google-cloud-ops-agent-repo.sh
sudo bash add-google-cloud-ops-agent-repo.sh --also-install
rm -f add-google-cloud-ops-agent-repo.sh

# Default Ops Agent config tails /var/log/syslog + auth.log + the runner's
# diag logs into Cloud Logging. Job-level GitHub-Actions logs ship via the
# runner agent's own log stream (CloudLogging service automatically picks
# up stdout from systemd-journald).

# Drop-in config: include /home/runner/actions-runner/_diag/*.log so silent
# runner failures (issue #55 territory) emit to Cloud Logging.
sudo mkdir -p /etc/google-cloud-ops-agent
sudo tee /etc/google-cloud-ops-agent/config.yaml > /dev/null <<'CONFIG'
logging:
  receivers:
    runner_diag:
      type: files
      include_paths:
        - /home/runner/actions-runner/_diag/*.log
    runner_userdata:
      type: files
      include_paths:
        - /var/log/jit-runner-userdata.log
  service:
    pipelines:
      runner_diag_pipeline:
        receivers: [runner_diag]
      runner_userdata_pipeline:
        receivers: [runner_userdata]
CONFIG

# Restart the Ops Agent so it picks up the config.
sudo systemctl restart google-cloud-ops-agent

echo "=== jit-runners: Google Cloud Ops Agent installed ==="
