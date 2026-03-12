package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// Config holds the Lambda runtime configuration.
type Config struct {
	// GitHub App credentials.
	AppID         string
	PrivateKey    string
	WebhookSecret string

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
// Required env vars: GITHUB_APP_ID, SQS_QUEUE_URL, DYNAMODB_TABLE_NAME.
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

	if cfg.AppID == "" {
		return nil, fmt.Errorf("GITHUB_APP_ID is required")
	}
	if cfg.QueueURL == "" {
		return nil, fmt.Errorf("SQS_QUEUE_URL is required")
	}
	if cfg.TableName == "" {
		return nil, fmt.Errorf("DYNAMODB_TABLE_NAME is required")
	}

	// Parse subnet IDs (comma-separated).
	if subnets := os.Getenv("EC2_SUBNET_IDS"); subnets != "" {
		cfg.SubnetIDs = strings.Split(subnets, ",")
	}

	// Parse label mappings from JSON.
	if mappings := os.Getenv("LABEL_MAPPINGS"); mappings != "" {
		if err := json.Unmarshal([]byte(mappings), &cfg.LabelMappings); err != nil {
			return nil, fmt.Errorf("parse LABEL_MAPPINGS: %w", err)
		}
	}

	// Load secrets.
	webhookSecretARN := os.Getenv("GITHUB_APP_WEBHOOK_SECRET_ARN")
	privateKeyARN := os.Getenv("GITHUB_APP_PRIVATE_KEY_SECRET_ARN")

	if webhookSecretARN != "" || privateKeyARN != "" {
		if client == nil {
			awsCfg, err := config.LoadDefaultConfig(ctx)
			if err != nil {
				return nil, fmt.Errorf("load AWS config: %w", err)
			}
			client = secretsmanager.NewFromConfig(awsCfg)
		}
		if webhookSecretARN != "" {
			secret, err := getSecret(ctx, client, webhookSecretARN)
			if err != nil {
				return nil, fmt.Errorf("webhook secret: %w", err)
			}
			cfg.WebhookSecret = secret
		} else {
			cfg.WebhookSecret = os.Getenv("GITHUB_APP_WEBHOOK_SECRET")
		}
		if privateKeyARN != "" {
			secret, err := getSecret(ctx, client, privateKeyARN)
			if err != nil {
				return nil, fmt.Errorf("private key: %w", err)
			}
			cfg.PrivateKey = secret
		} else {
			cfg.PrivateKey = os.Getenv("GITHUB_APP_PRIVATE_KEY")
		}
	} else {
		cfg.WebhookSecret = os.Getenv("GITHUB_APP_WEBHOOK_SECRET")
		cfg.PrivateKey = os.Getenv("GITHUB_APP_PRIVATE_KEY")
	}

	if cfg.WebhookSecret == "" {
		return nil, fmt.Errorf("webhook secret is required (GITHUB_APP_WEBHOOK_SECRET or GITHUB_APP_WEBHOOK_SECRET_ARN)")
	}
	if cfg.PrivateKey == "" {
		return nil, fmt.Errorf("private key is required (GITHUB_APP_PRIVATE_KEY or GITHUB_APP_PRIVATE_KEY_SECRET_ARN)")
	}
	return cfg, nil
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
