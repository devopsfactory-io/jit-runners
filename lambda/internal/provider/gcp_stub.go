package provider

import (
	"context"
	"errors"
)

// newGCP is the GCP factory stub. It returns a typed error until Phase C
// lands the real Pub/Sub / Firestore / GCE / Secret Manager wiring. Having
// the symbol present keeps the dispatch in provider.New honest.
func newGCP(_ context.Context) (*Bundle, error) {
	return nil, errors.New("provider: gcp factory not yet implemented (Phase C)")
}
