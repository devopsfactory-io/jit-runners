package gce

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateStartupScript(t *testing.T) {
	tests := []struct {
		name      string
		params    *StartupScriptParams
		wantErr   bool
		wantParts []string
	}{
		{
			name: "valid params",
			params: &StartupScriptParams{
				RunnerVersion: "2.332.0",
				JITConfig:     "encoded-jit-config-string",
				RunnerID:      42,
			},
			wantParts: []string{
				"#!/bin/bash",
				`RUNNER_VERSION="2.332.0"`,
				`JIT_CONFIG="encoded-jit-config-string"`,
				"export RUNNER_ID=42",
				"Metadata-Flavor: Google",
				"metadata.google.internal/computeMetadata/v1/instance/id",
				"./run.sh --jitconfig",
				"gcloud compute instances delete",
				"apt-get install",
				"/var/log/jit-runner-userdata.log",
				"sleep 5",
			},
		},
		{
			name:    "missing runner version",
			params:  &StartupScriptParams{JITConfig: "x", RunnerID: 1},
			wantErr: true,
		},
		{
			name:    "missing JIT config",
			params:  &StartupScriptParams{RunnerVersion: "2.332.0", RunnerID: 1},
			wantErr: true,
		},
		{
			name:    "missing runner id",
			params:  &StartupScriptParams{RunnerVersion: "2.332.0", JITConfig: "x"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GenerateStartupScript(tt.params)
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
					t.Errorf("rendered startup script missing %q\n--- body ---\n%s", want, body)
				}
			}
		})
	}
}
