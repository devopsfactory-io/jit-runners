// Package pubsub provides a GCP Pub/Sub-backed implementation of
// queue.Publisher. Used by the webhook, scaledown re-enqueue, and rebalancer
// Lambdas on the GCP path.
package pubsub

import (
	"context"
	"fmt"

	"cloud.google.com/go/pubsub/v2"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/queue"
)

// Publisher publishes queue.Msg bodies to a single Pub/Sub topic.
type Publisher struct {
	publisher *pubsub.Publisher
}

// NewPublisher returns a Publisher bound to the given topic. topicName must be
// a fully-qualified topic resource name of the form
// "projects/<project>/topics/<topic>". The caller owns the *pubsub.Client
// lifecycle; stopping the Publisher before closing the client is recommended.
func NewPublisher(client *pubsub.Client, topicName string) *Publisher {
	return &Publisher{publisher: client.Publisher(topicName)}
}

// Publish marshals m.Body into a Pub/Sub message and publishes synchronously.
// The Pub/Sub Go SDK applies internal batching across concurrent calls
// (default: 10ms delay, 100 msg count, 1MB byte). One synchronous call per
// message keeps the API surface symmetric with awssqs.Publisher.
func (p *Publisher) Publish(ctx context.Context, m queue.Msg) error {
	if len(m.Body) == 0 {
		return fmt.Errorf("gcp/pubsub: publish: empty body")
	}
	result := p.publisher.Publish(ctx, &pubsub.Message{Data: m.Body})
	if _, err := result.Get(ctx); err != nil {
		return fmt.Errorf("gcp/pubsub: publish: %w", err)
	}
	return nil
}

// Compile-time assertion that *Publisher satisfies queue.Publisher.
var _ queue.Publisher = (*Publisher)(nil)
