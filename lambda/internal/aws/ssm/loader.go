// Package ssm is a thin AWS Systems Manager Parameter Store reader with a
// TTL cache. Production callers use it for low-frequency runtime toggles
// such as /jit-runners/runner-log-level.
package ssm

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// API abstracts the SSM GetParameter operation for testing. The real
// *ssm.Client satisfies this interface.
type API interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// Loader fetches SSM parameters with a TTL cache. Use one Loader per process;
// the cache is keyed by parameter name and is safe for concurrent callers.
type Loader struct {
	client API
	ttl    time.Duration

	mu      sync.Mutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	value    string
	expires  time.Time
	hasValue bool
}

// New constructs a Loader. ttl is the cache window for successful reads.
// A ttl of zero disables caching.
func New(client API, ttl time.Duration) *Loader {
	return &Loader{
		client:  client,
		ttl:     ttl,
		entries: map[string]cacheEntry{},
	}
}

// Get returns the parameter's string value or fallback when the parameter
// does not exist or the API call fails. When the cache holds a non-expired
// hit, it is returned directly without an API call. When the cache has an
// expired hit and the API call fails, the previous value is returned (last-
// known-good) so transient throttles do not collapse to fallback. A first-
// call API failure returns fallback.
func (l *Loader) Get(ctx context.Context, name string, fallback string) (string, error) {
	now := time.Now()

	l.mu.Lock()
	if e, ok := l.entries[name]; ok && e.hasValue && now.Before(e.expires) {
		l.mu.Unlock()
		return e.value, nil
	}
	prev := l.entries[name]
	l.mu.Unlock()

	out, err := l.client.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name)})
	if err != nil {
		var notFound *types.ParameterNotFound
		if errors.As(err, &notFound) {
			log.Printf("ssm: parameter %s missing, defaulting to %q", name, fallback)
			return fallback, nil
		}
		if prev.hasValue {
			log.Printf("ssm: get %s failed (%v), returning last cached value %q", name, err, prev.value)
			return prev.value, nil
		}
		log.Printf("ssm: get %s failed (%v) on cold cache, defaulting to %q", name, err, fallback)
		return fallback, nil
	}
	value := aws.ToString(out.Parameter.Value)

	l.mu.Lock()
	l.entries[name] = cacheEntry{
		value:    value,
		expires:  now.Add(l.ttl),
		hasValue: true,
	}
	l.mu.Unlock()

	return value, nil
}
