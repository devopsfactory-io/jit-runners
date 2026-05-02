package ec2

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateUserData(t *testing.T) {
	tests := []struct {
		name      string
		params    *UserDataParams
		wantErr   bool
		wantParts []string
		notParts  []string
	}{
		{
			name: "valid params info-level",
			params: &UserDataParams{
				RunnerVersion:  "2.321.0",
				JITConfig:      "encoded-jit-config-string",
				RunnerID:       42,
				RunnerLogLevel: "info",
			},
			wantParts: []string{
				"#!/bin/bash",
				"RUNNER_VERSION=\"2.321.0\"",
				"JIT_CONFIG=\"encoded-jit-config-string\"",
				"export RUNNER_ID=42",
				"sed -i \"s|\\${RUNNER_ID}|${RUNNER_ID}|g\" /opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json",
				"systemctl restart amazon-cloudwatch-agent",
				"./run.sh --jitconfig",
				"tee /var/log/jit-runner-userdata.log",
				"RUNTIME_SECS",
				"-lt 30",
				"JIT_NO_JOB_PICKUP runner_id=42",
				"sleep 5",
				"terminate-instances",
				"/opt/jit-runner-prebaked",
				"dnf install",
				"pre-baked AMI detected",
				"git-lfs",
				"gh-cli.repo",
				"jq",
				"rm -f runner.tar.gz",
			},
			notParts: []string{
				"export ACTIONS_RUNNER_DEBUG=true",
				"export ACTIONS_STEP_DEBUG=true",
			},
		},
		{
			name: "valid params debug-level",
			params: &UserDataParams{
				RunnerVersion:  "2.321.0",
				JITConfig:      "encoded-jit-config-string",
				RunnerID:       7,
				RunnerLogLevel: "debug",
			},
			wantParts: []string{
				"export RUNNER_ID=7",
				"export ACTIONS_RUNNER_DEBUG=true",
				"export ACTIONS_STEP_DEBUG=true",
				"JIT_NO_JOB_PICKUP runner_id=7",
			},
		},
		{
			name: "missing runner version",
			params: &UserDataParams{
				JITConfig:      "some-config",
				RunnerID:       1,
				RunnerLogLevel: "info",
			},
			wantErr: true,
		},
		{
			name: "missing JIT config",
			params: &UserDataParams{
				RunnerVersion:  "2.321.0",
				RunnerID:       1,
				RunnerLogLevel: "info",
			},
			wantErr: true,
		},
		{
			name: "missing runner id",
			params: &UserDataParams{
				RunnerVersion:  "2.321.0",
				JITConfig:      "some-config",
				RunnerLogLevel: "info",
			},
			wantErr: true,
		},
		{
			name: "empty runner log level defaults to no debug",
			params: &UserDataParams{
				RunnerVersion: "2.321.0",
				JITConfig:     "some-config",
				RunnerID:      99,
			},
			wantParts: []string{
				"export RUNNER_ID=99",
			},
			notParts: []string{
				"export ACTIONS_RUNNER_DEBUG=true",
				"export ACTIONS_STEP_DEBUG=true",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GenerateUserData(tt.params)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			decoded, derr := base64.StdEncoding.DecodeString(got)
			if derr != nil {
				t.Fatalf("decode base64: %v", derr)
			}
			body := string(decoded)
			for _, want := range tt.wantParts {
				if !strings.Contains(body, want) {
					t.Errorf("rendered userdata missing %q\n--- body ---\n%s", want, body)
				}
			}
			for _, notWant := range tt.notParts {
				if strings.Contains(body, notWant) {
					t.Errorf("rendered userdata contains forbidden %q", notWant)
				}
			}
		})
	}
}

func TestSilentFailureThresholdConstant(t *testing.T) {
	if silentFailureThresholdSecs != 30 {
		t.Errorf("silentFailureThresholdSecs = %d, want 30 (changing this value rolls over the JIT_NO_JOB_PICKUP semantics; coordinate with the operator runbook)", silentFailureThresholdSecs)
	}
}
