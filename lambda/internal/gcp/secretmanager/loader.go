// Package secretmanager provides a GCP Secret Manager-backed implementation
// of secrets.Loader.
package secretmanager

import (
	"context"
	"fmt"

	"github.com/googleapis/gax-go/v2"

	smpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/secrets"
)

// secretManagerAPI is the narrow Secret Manager API surface Loader uses.
// *secretmanager.Client (from cloud.google.com/go/secretmanager/apiv1)
// satisfies this interface.
type secretManagerAPI interface {
	AccessSecretVersion(ctx context.Context, req *smpb.AccessSecretVersionRequest, opts ...gax.CallOption) (*smpb.AccessSecretVersionResponse, error)
}

// Loader fetches secret payloads from GCP Secret Manager.
type Loader struct {
	api secretManagerAPI
}

// New returns a Loader bound to the given Secret Manager client.
func New(api secretManagerAPI) *Loader {
	return &Loader{api: api}
}

// Load returns the payload bytes of the named secret version. Name must be
// a fully-qualified resource of the form:
//
//	projects/<project>/secrets/<secret>/versions/<version>
//
// Use "latest" for the version to track the most recent published version.
func (l *Loader) Load(ctx context.Context, name string) ([]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("gcp/secretmanager: load: empty name")
	}
	resp, err := l.api.AccessSecretVersion(ctx, &smpb.AccessSecretVersionRequest{Name: name})
	if err != nil {
		return nil, fmt.Errorf("gcp/secretmanager: access %q: %w", name, err)
	}
	return resp.GetPayload().GetData(), nil
}

// Compile-time assertion that *Loader satisfies secrets.Loader.
var _ secrets.Loader = (*Loader)(nil)
