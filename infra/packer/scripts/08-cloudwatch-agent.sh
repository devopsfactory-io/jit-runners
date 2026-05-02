#!/bin/bash
set -euo pipefail

# jit-runners: install and configure the AWS CloudWatch agent.
#
# The agent tails the runner-agent diagnostic logs and the userdata
# script's stdout; the userdata starts the agent at boot AFTER exporting
# RUNNER_ID so the stream name resolves to <runner_id>/<instance_id>.

echo "=== jit-runners: installing amazon-cloudwatch-agent ==="
sudo dnf install -y https://amazoncloudwatch-agent.s3.amazonaws.com/amazon_linux/amd64/latest/amazon-cloudwatch-agent.rpm

echo "=== jit-runners: writing cloudwatch agent config ==="
sudo mkdir -p /opt/aws/amazon-cloudwatch-agent/etc
sudo tee /opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json > /dev/null <<'EOF'
{
  "logs": {
    "logs_collected": {
      "files": {
        "collect_list": [
          {
            "file_path": "/home/runner/actions-runner/_diag/Runner_*.log",
            "log_group_name": "/jit-runners/runner-agent",
            "log_stream_name": "${RUNNER_ID}/{instance_id}",
            "timestamp_format": "%Y-%m-%d %H:%M:%SZ",
            "retention_in_days": 14
          },
          {
            "file_path": "/home/runner/actions-runner/_diag/Worker_*.log",
            "log_group_name": "/jit-runners/runner-agent",
            "log_stream_name": "${RUNNER_ID}/{instance_id}",
            "timestamp_format": "%Y-%m-%d %H:%M:%SZ",
            "retention_in_days": 14
          },
          {
            "file_path": "/var/log/jit-runner-userdata.log",
            "log_group_name": "/jit-runners/userdata",
            "log_stream_name": "${RUNNER_ID}/{instance_id}",
            "retention_in_days": 14
          }
        ]
      }
    },
    "force_flush_interval": 5
  }
}
EOF

echo "=== jit-runners: enabling cloudwatch agent (deferred start) ==="
# Enable but do NOT start now. Userdata starts the unit AFTER exporting
# RUNNER_ID so the stream-name placeholder resolves.
sudo systemctl enable amazon-cloudwatch-agent

echo "=== jit-runners: cloudwatch agent installed ==="
