package sqs

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/queue"
)

// LifecyclePublisher publishes generic queue.Msg payloads to the lifecycle
// SQS queue. Callers use queue.PublishLifecycle for the typed entry point.
type LifecyclePublisher struct {
	client   Sender
	queueURL string
}

// NewLifecyclePublisher returns a queue.Publisher bound to the lifecycle
// queue URL.
func NewLifecyclePublisher(client Sender, queueURL string) *LifecyclePublisher {
	return &LifecyclePublisher{client: client, queueURL: queueURL}
}

// Publish sends m.Body as the body of a new SQS message in the lifecycle queue.
// Empty bodies are rejected so callers can distinguish accidental empty
// publishes from genuine zero-length payloads. Use queue.PublishLifecycle for
// the typed entry point.
func (p *LifecyclePublisher) Publish(ctx context.Context, m queue.Msg) error {
	if len(m.Body) == 0 {
		return fmt.Errorf("aws/sqs: lifecycle publish: empty body")
	}
	_, err := p.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(p.queueURL),
		MessageBody: aws.String(string(m.Body)),
	})
	if err != nil {
		return fmt.Errorf("aws/sqs: lifecycle publish: %w", err)
	}
	return nil
}

// Compile-time assertion that *LifecyclePublisher satisfies queue.Publisher.
var _ queue.Publisher = (*LifecyclePublisher)(nil)
