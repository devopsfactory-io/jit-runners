package config

import (
	"context"
	"testing"
)

func TestLoad_RequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		env    map[string]string
		errMsg string
	}{
		{
			name:   "missing GITHUB_APP_ID",
			env:    map[string]string{},
			errMsg: "GITHUB_APP_ID is required",
		},
		{
			name: "missing SQS_QUEUE_URL",
			env: map[string]string{
				"GITHUB_APP_ID": "12345",
			},
			errMsg: "SQS_QUEUE_URL is required",
		},
		{
			name: "missing DYNAMODB_TABLE_NAME",
			env: map[string]string{
				"GITHUB_APP_ID": "12345",
				"SQS_QUEUE_URL": "https://sqs.us-east-1.amazonaws.com/123/queue",
			},
			errMsg: "DYNAMODB_TABLE_NAME is required",
		},
		{
			name: "missing webhook secret",
			env: map[string]string{
				"GITHUB_APP_ID":       "12345",
				"SQS_QUEUE_URL":       "https://sqs.us-east-1.amazonaws.com/123/queue",
				"DYNAMODB_TABLE_NAME": "runners",
			},
			errMsg: "webhook secret is required (GITHUB_APP_WEBHOOK_SECRET or GITHUB_APP_WEBHOOK_SECRET_ARN)",
		},
		{
			name: "missing private key",
			env: map[string]string{
				"GITHUB_APP_ID":             "12345",
				"SQS_QUEUE_URL":             "https://sqs.us-east-1.amazonaws.com/123/queue",
				"DYNAMODB_TABLE_NAME":       "runners",
				"GITHUB_APP_WEBHOOK_SECRET": "secret",
			},
			errMsg: "private key is required (GITHUB_APP_PRIVATE_KEY or GITHUB_APP_PRIVATE_KEY_SECRET_ARN)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all relevant env vars.
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			_, err := Load(context.Background())
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err.Error() != tt.errMsg {
				t.Errorf("error = %q, want %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestLoad_Success(t *testing.T) {
	clearEnv(t)
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("SQS_QUEUE_URL", "https://sqs.us-east-1.amazonaws.com/123/queue")
	t.Setenv("DYNAMODB_TABLE_NAME", "runners")
	t.Setenv("GITHUB_APP_WEBHOOK_SECRET", "my-secret")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "my-private-key")
	t.Setenv("EC2_SUBNET_IDS", "subnet-1,subnet-2")
	t.Setenv("LABEL_MAPPINGS", `[{"label":"gpu","instance_type":"g4dn.xlarge"}]`)

	cfg, err := Load(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.AppID != "12345" {
		t.Errorf("AppID = %q, want %q", cfg.AppID, "12345")
	}
	if cfg.QueueURL != "https://sqs.us-east-1.amazonaws.com/123/queue" {
		t.Errorf("QueueURL = %q", cfg.QueueURL)
	}
	if cfg.WebhookSecret != "my-secret" {
		t.Errorf("WebhookSecret = %q", cfg.WebhookSecret)
	}
	if len(cfg.SubnetIDs) != 2 {
		t.Errorf("SubnetIDs len = %d, want 2", len(cfg.SubnetIDs))
	}
	if len(cfg.LabelMappings) != 1 || cfg.LabelMappings[0].Label != "gpu" {
		t.Errorf("LabelMappings = %v", cfg.LabelMappings)
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	envVars := []string{
		"GITHUB_APP_ID", "SQS_QUEUE_URL", "DYNAMODB_TABLE_NAME",
		"GITHUB_APP_WEBHOOK_SECRET", "GITHUB_APP_WEBHOOK_SECRET_ARN",
		"GITHUB_APP_PRIVATE_KEY", "GITHUB_APP_PRIVATE_KEY_SECRET_ARN",
		"EC2_SUBNET_IDS", "EC2_SECURITY_GROUP_ID", "EC2_IAM_INSTANCE_PROFILE",
		"EC2_DEFAULT_AMI", "LABEL_MAPPINGS",
	}
	for _, k := range envVars {
		t.Setenv(k, "")
	}
}
