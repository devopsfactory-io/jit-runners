package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
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

// SecretsReader abstracts Secrets Manager for testing.
type SecretsReader interface {
	GetSecretValue(ctx context.Context, input *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// Load reads config from environment variables and optionally Secrets Manager.
//
// Required env vars: GITHUB_APP_ID, DYNAMODB_TABLE_NAME.
// Optional env vars: SQS_QUEUE_URL (consumed by webhook + scaledown re-enqueue;
// not used by the lifecycle handler — its absence must not crash that lambda).
// Secrets: GITHUB_APP_WEBHOOK_SECRET_ARN / GITHUB_APP_WEBHOOK_SECRET,
//
//	GITHUB_APP_PRIVATE_KEY_SECRET_ARN / GITHUB_APP_PRIVATE_KEY.
func Load(ctx context.Context) (*Config, error) {
	return LoadWithClient(ctx, nil)
}

// LoadWithClient reads config using the provided SecretsReader (nil uses default).
func LoadWithClient(ctx context.Context, client SecretsReader) (*Config, error) {
	cfg := &Config{
		AppID:              os.Getenv("GITHUB_APP_ID"),
		QueueURL:           os.Getenv("SQS_QUEUE_URL"),
		TableName:          os.Getenv("DYNAMODB_TABLE_NAME"),
		SecurityGroupID:    os.Getenv("EC2_SECURITY_GROUP_ID"),
		IAMInstanceProfile: os.Getenv("EC2_IAM_INSTANCE_PROFILE"),
		DefaultAMI:         os.Getenv("EC2_DEFAULT_AMI"),
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

	if err := loadSecrets(ctx, cfg, client); err != nil {
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

// loadSecrets loads webhook secret and private key from Secrets Manager or environment.
func loadSecrets(ctx context.Context, cfg *Config, client SecretsReader) error {
	webhookSecretARN := os.Getenv("GITHUB_APP_WEBHOOK_SECRET_ARN")
	privateKeyARN := os.Getenv("GITHUB_APP_PRIVATE_KEY_SECRET_ARN")

	if webhookSecretARN != "" || privateKeyARN != "" {
		if client == nil {
			awsCfg, err := config.LoadDefaultConfig(ctx)
			if err != nil {
				return fmt.Errorf("load AWS config: %w", err)
			}
			client = secretsmanager.NewFromConfig(awsCfg)
		}
		if webhookSecretARN != "" {
			secret, err := getSecret(ctx, client, webhookSecretARN)
			if err != nil {
				return fmt.Errorf("webhook secret: %w", err)
			}
			cfg.WebhookSecret = secret
		} else {
			cfg.WebhookSecret = os.Getenv("GITHUB_APP_WEBHOOK_SECRET")
		}
		if privateKeyARN != "" {
			secret, err := getSecret(ctx, client, privateKeyARN)
			if err != nil {
				return fmt.Errorf("private key: %w", err)
			}
			cfg.PrivateKey = secret
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

func getSecret(ctx context.Context, client SecretsReader, arn string) (string, error) {
	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(arn),
	})
	if err != nil {
		return "", err
	}
	if out.SecretString != nil {
		return *out.SecretString, nil
	}
	return "", fmt.Errorf("secret %s has no SecretString", arn)
}
