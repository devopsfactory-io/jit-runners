// Package config loads jit-runners Lambda runtime configuration from
// environment variables and a secrets.Loader. The Loader abstracts the secret
// store (AWS Secrets Manager today, GCP Secret Manager on the GCP path).
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	smloader "github.com/devopsfactory-io/jit-runners/lambda/internal/aws/secretsmanager"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/secrets"
)

// Config holds the Lambda runtime configuration.
type Config struct {
	// GitHub App credentials.
	AppID         string
	PrivateKey    string //nolint:gosec // G117: not a hardcoded credential, loaded from env/secrets manager
	WebhookSecret string

	// InstallationID is the GitHub App installation this Lambda authenticates
	// against when minting installation access tokens at startup (used by the
	// scaledown Cleaner and lifecycle handler, which operate on records that
	// do not carry a per-message installation_id). Optional: when zero the
	// caller falls back to a tokenless GitHub client and DeregisterRunner
	// calls will 401 in production. Scaleup is unaffected because it derives
	// the installation from the SQS message payload directly.
	InstallationID int64

	// SQS queue URL for scale-up messages.
	QueueURL string

	// DynamoDB table name for runner state.
	TableName string

	// Runner configuration.
	LabelMappings []LabelMapping

	// EC2 configuration.
	SubnetIDs          []string
	SecurityGroupID    string
	IAMInstanceProfile string
	DefaultAMI         string

	// DefaultInstanceType is the cloud-specific machine type used when no
	// label mapping matches. AWS uses an EC2 type (e.g. t3.large); GCP uses
	// a GCE machine type (e.g. n2-standard-2). Required.
	DefaultInstanceType string

	// Scale-down configuration.
	MaxRunnerAgeMinutes   int
	StaleThresholdMinutes int
}

// LabelMapping maps a runner label to an EC2 instance type.
type LabelMapping struct {
	Label        string `json:"label"`
	InstanceType string `json:"instance_type"`
	AMI          string `json:"ami,omitempty"`
}

// Load reads config from environment variables. If any *_SECRET_ARN env var
// is set, an AWS Secrets Manager Loader is constructed lazily and used.
//
// Required env vars: GITHUB_APP_ID, DYNAMODB_TABLE_NAME.
// Optional env vars: SQS_QUEUE_URL (consumed by webhook + scaledown re-enqueue;
// not used by the lifecycle handler — its absence must not crash that lambda).
// Secrets: GITHUB_APP_WEBHOOK_SECRET_ARN / GITHUB_APP_WEBHOOK_SECRET,
//
//	GITHUB_APP_PRIVATE_KEY_SECRET_ARN / GITHUB_APP_PRIVATE_KEY.
func Load(ctx context.Context) (*Config, error) {
	return LoadWith(ctx, nil)
}

// LoadWith reads config using the provided secrets.Loader. Pass nil to use
// the default AWS Secrets Manager loader (constructed lazily only if needed).
func LoadWith(ctx context.Context, loader secrets.Loader) (*Config, error) {
	cfg := &Config{
		AppID:               os.Getenv("GITHUB_APP_ID"),
		QueueURL:            os.Getenv("SQS_QUEUE_URL"),
		TableName:           os.Getenv("DYNAMODB_TABLE_NAME"),
		SecurityGroupID:     os.Getenv("EC2_SECURITY_GROUP_ID"),
		IAMInstanceProfile:  os.Getenv("EC2_IAM_INSTANCE_PROFILE"),
		DefaultAMI:          os.Getenv("EC2_DEFAULT_AMI"),
		DefaultInstanceType: os.Getenv("DEFAULT_INSTANCE_TYPE"),
	}

	if err := validateRequiredEnv(cfg); err != nil {
		return nil, err
	}

	// Parse subnet IDs (comma-separated).
	if subnets := os.Getenv("EC2_SUBNET_IDS"); subnets != "" {
		cfg.SubnetIDs = strings.Split(subnets, ",")
	}

	// Parse optional GitHub App installation ID. Required for the scaledown
	// Cleaner and lifecycle handler to mint installation tokens for
	// DeregisterRunner; ignored by webhook + scaleup.
	if v := os.Getenv("GITHUB_INSTALLATION_ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse GITHUB_INSTALLATION_ID %q: %w", v, err)
		}
		cfg.InstallationID = n
	}

	// Parse label mappings from JSON.
	if mappings := os.Getenv("LABEL_MAPPINGS"); mappings != "" {
		if err := json.Unmarshal([]byte(mappings), &cfg.LabelMappings); err != nil {
			return nil, fmt.Errorf("parse LABEL_MAPPINGS: %w", err)
		}
	}

	if err := loadSecrets(ctx, cfg, loader); err != nil {
		return nil, err
	}

	return cfg, nil
}

// validateRequiredEnv checks that required environment variables are set on the config.
func validateRequiredEnv(cfg *Config) error {
	if cfg.AppID == "" {
		return fmt.Errorf("GITHUB_APP_ID is required")
	}
	if cfg.TableName == "" {
		return fmt.Errorf("DYNAMODB_TABLE_NAME is required")
	}
	return nil
}

// loadSecrets loads webhook secret and private key from a secrets.Loader (when
// any *_SECRET_ARN env var is set) or from plain env vars.
func loadSecrets(ctx context.Context, cfg *Config, loader secrets.Loader) error {
	webhookSecretARN := os.Getenv("GITHUB_APP_WEBHOOK_SECRET_ARN")
	privateKeyARN := os.Getenv("GITHUB_APP_PRIVATE_KEY_SECRET_ARN")

	if webhookSecretARN != "" || privateKeyARN != "" {
		if loader == nil {
			awsCfg, err := config.LoadDefaultConfig(ctx)
			if err != nil {
				return fmt.Errorf("load AWS config: %w", err)
			}
			loader = smloader.New(awssm.NewFromConfig(awsCfg))
		}
		if webhookSecretARN != "" {
			secret, err := loader.Load(ctx, webhookSecretARN)
			if err != nil {
				return fmt.Errorf("webhook secret: %w", err)
			}
			cfg.WebhookSecret = string(secret)
		} else {
			cfg.WebhookSecret = os.Getenv("GITHUB_APP_WEBHOOK_SECRET")
		}
		if privateKeyARN != "" {
			secret, err := loader.Load(ctx, privateKeyARN)
			if err != nil {
				return fmt.Errorf("private key: %w", err)
			}
			cfg.PrivateKey = string(secret)
		} else {
			cfg.PrivateKey = os.Getenv("GITHUB_APP_PRIVATE_KEY")
		}
	} else {
		cfg.WebhookSecret = os.Getenv("GITHUB_APP_WEBHOOK_SECRET")
		cfg.PrivateKey = os.Getenv("GITHUB_APP_PRIVATE_KEY")
	}

	// Webhook secret and private key are validated by individual cmd handlers
	// rather than at config load. The webhook lambda needs the WebhookSecret
	// for HMAC verification; scaleup, scaledown, and lifecycle need the
	// PrivateKey for installation-token minting; lifecycle does not need the
	// WebhookSecret. Pushing the validation down to each cmd avoids crashing
	// lambdas at startup over secrets they never use.
	return nil
}
