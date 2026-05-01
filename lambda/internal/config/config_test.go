package config

import (
	"context"
	"testing"
)

type fakeLoader struct {
	values map[string][]byte
	err    error
}

func (f *fakeLoader) Load(_ context.Context, name string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	v, ok := f.values[name]
	if !ok {
		return nil, nil
	}
	return v, nil
}

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
			name: "missing DYNAMODB_TABLE_NAME (SQS_QUEUE_URL is now optional)",
			env: map[string]string{
				"GITHUB_APP_ID": "12345",
			},
			errMsg: "DYNAMODB_TABLE_NAME is required",
		},
		// Note: webhook secret and private key are no longer validated at config load.
		// Each cmd handler validates the secrets it actually needs (webhook needs
		// WebhookSecret; scaleup/scaledown/lifecycle need PrivateKey; lifecycle
		// does not need WebhookSecret).
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
	if cfg.InstallationID != 0 {
		t.Errorf("InstallationID = %d, want 0 (unset)", cfg.InstallationID)
	}
}

func TestLoad_InstallationID(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("GITHUB_APP_ID", "12345")
		t.Setenv("SQS_QUEUE_URL", "https://sqs.us-east-1.amazonaws.com/123/queue")
		t.Setenv("DYNAMODB_TABLE_NAME", "runners")
		t.Setenv("GITHUB_APP_WEBHOOK_SECRET", "my-secret")
		t.Setenv("GITHUB_APP_PRIVATE_KEY", "my-private-key")
		t.Setenv("GITHUB_INSTALLATION_ID", "987654")

		cfg, err := Load(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.InstallationID != 987654 {
			t.Errorf("InstallationID = %d, want 987654", cfg.InstallationID)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("GITHUB_APP_ID", "12345")
		t.Setenv("SQS_QUEUE_URL", "https://sqs.us-east-1.amazonaws.com/123/queue")
		t.Setenv("DYNAMODB_TABLE_NAME", "runners")
		t.Setenv("GITHUB_APP_WEBHOOK_SECRET", "my-secret")
		t.Setenv("GITHUB_APP_PRIVATE_KEY", "my-private-key")
		t.Setenv("GITHUB_INSTALLATION_ID", "not-a-number")

		_, err := Load(context.Background())
		if err == nil {
			t.Fatal("expected parse error, got nil")
		}
	})
}

func TestLoadWith_FakeLoader(t *testing.T) {
	clearEnv(t)
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("SQS_QUEUE_URL", "https://sqs.us-east-1.amazonaws.com/123/queue")
	t.Setenv("DYNAMODB_TABLE_NAME", "runners")
	t.Setenv("GITHUB_APP_WEBHOOK_SECRET_ARN", "arn:aws:secret/webhook")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_SECRET_ARN", "arn:aws:secret/private-key")

	loader := &fakeLoader{values: map[string][]byte{
		"arn:aws:secret/webhook":     []byte("secret-from-loader"),
		"arn:aws:secret/private-key": []byte("private-key-from-loader"),
	}}

	cfg, err := LoadWith(context.Background(), loader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WebhookSecret != "secret-from-loader" {
		t.Errorf("WebhookSecret = %q", cfg.WebhookSecret)
	}
	if cfg.PrivateKey != "private-key-from-loader" {
		t.Errorf("PrivateKey = %q", cfg.PrivateKey)
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	envVars := []string{
		"GITHUB_APP_ID", "SQS_QUEUE_URL", "DYNAMODB_TABLE_NAME",
		"GITHUB_APP_WEBHOOK_SECRET", "GITHUB_APP_WEBHOOK_SECRET_ARN",
		"GITHUB_APP_PRIVATE_KEY", "GITHUB_APP_PRIVATE_KEY_SECRET_ARN",
		"GITHUB_INSTALLATION_ID",
		"EC2_SUBNET_IDS", "EC2_SECURITY_GROUP_ID", "EC2_IAM_INSTANCE_PROFILE",
		"EC2_DEFAULT_AMI", "LABEL_MAPPINGS",
	}
	for _, k := range envVars {
		t.Setenv(k, "")
	}
}
