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
	}{
		{
			name: "valid params",
			params: &UserDataParams{
				RunnerVersion: "2.321.0",
				JITConfig:     "encoded-jit-config-string",
			},
			wantParts: []string{
				"#!/bin/bash",
				"RUNNER_VERSION=\"2.321.0\"",
				"JIT_CONFIG=\"encoded-jit-config-string\"",
				"./run.sh --jitconfig",
				"terminate-instances",
			},
		},
		{
			name: "missing runner version",
			params: &UserDataParams{
				JITConfig: "some-config",
			},
			wantErr: true,
		},
		{
			name: "missing JIT config",
			params: &UserDataParams{
				RunnerVersion: "2.321.0",
			},
			wantErr: true,
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

			// Decode base64 and verify content.
			decoded, err := base64.StdEncoding.DecodeString(got)
			if err != nil {
				t.Fatalf("base64 decode: %v", err)
			}
			script := string(decoded)

			for _, part := range tt.wantParts {
				if !strings.Contains(script, part) {
					t.Errorf("user-data missing %q", part)
				}
			}
		})
	}
}
