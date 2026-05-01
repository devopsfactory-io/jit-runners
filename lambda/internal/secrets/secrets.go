// Package secrets defines the cloud-agnostic secret-loader contract.
package secrets

import "context"

// Loader fetches a secret payload by logical name. Implementations:
// internal/aws/secretsmanager (AWS Secrets Manager) and
// internal/gcp/secretmanager (GCP Secret Manager).
type Loader interface {
	Load(ctx context.Context, name string) ([]byte, error)
}
