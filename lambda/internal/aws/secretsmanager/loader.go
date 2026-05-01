// Package secretsmanager is the AWS Secrets Manager implementation of the
// secrets.Loader contract defined in internal/secrets.
package secretsmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/secrets"
)

// ErrEmptySecret is returned when the secret exists but has neither a string
// nor binary payload.
var ErrEmptySecret = errors.New("secret has no value")

// API abstracts the Secrets Manager GetSecretValue operation for testing. The
// real *secretsmanager.Client satisfies this interface.
type API interface {
	GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// Loader fetches secrets from AWS Secrets Manager. It satisfies secrets.Loader.
type Loader struct {
	client API
}

// Compile-time assertion that Loader satisfies secrets.Loader.
var _ secrets.Loader = (*Loader)(nil)

// New creates a Loader from the given client. Pass *secretsmanager.Client in
// production code.
func New(client API) *Loader {
	return &Loader{client: client}
}

// Load fetches the secret identified by name (typically an ARN) and returns
// the raw payload bytes. SecretString takes precedence over SecretBinary.
// Returns ErrEmptySecret when both are nil.
func (l *Loader) Load(ctx context.Context, name string) ([]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("secret name is required")
	}
	out, err := l.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(name),
	})
	if err != nil {
		return nil, fmt.Errorf("get secret %s: %w", name, err)
	}
	if out.SecretString != nil {
		return []byte(*out.SecretString), nil
	}
	if len(out.SecretBinary) > 0 {
		return out.SecretBinary, nil
	}
	return nil, fmt.Errorf("%s: %w", name, ErrEmptySecret)
}
