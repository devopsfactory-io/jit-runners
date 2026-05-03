package provider

import (
	"context"
	"fmt"
)

// Provider name constants. The empty string defaults to AWS.
const (
	AWS = "aws"
	GCP = "gcp"
)

// New returns a Bundle for the given provider name. Empty string defaults
// to AWS. Unknown names return a typed error.
func New(ctx context.Context, name string) (*Bundle, error) {
	switch name {
	case "", AWS:
		return newAWS(ctx)
	case GCP:
		return newGCP(ctx)
	default:
		return nil, fmt.Errorf("provider: unknown CLOUD_PROVIDER %q", name)
	}
}
