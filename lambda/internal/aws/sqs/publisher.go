// Package sqs is the AWS SQS implementation of the queue.Publisher and
// queue.Consumer contracts defined in internal/queue.
package sqs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/queue"
)

const defaultDelaySeconds = 30

// Sender abstracts the SQS SendMessage API for testing.
type Sender interface {
	SendMessage(ctx context.Context, input *sqs.SendMessageInput, opts ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// Publisher sends scale-up messages to SQS. It satisfies queue.Publisher.
type Publisher struct {
	client   Sender
	queueURL string
}

// Compile-time assertion that Publisher satisfies queue.Publisher.
var _ queue.Publisher = (*Publisher)(nil)

// NewPublisher creates a Publisher for the given queue URL.
func NewPublisher(client Sender, queueURL string) *Publisher {
	return &Publisher{
		client:   client,
		queueURL: queueURL,
	}
}

// Publish sends the message body to the SQS queue with a delay. The body is
// expected to be a JSON-encoded payload prepared by the caller.
func (p *Publisher) Publish(ctx context.Context, m queue.Msg) error {
	if len(m.Body) == 0 {
		return fmt.Errorf("queue.Msg.Body is empty")
	}
	_, err := p.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:     aws.String(p.queueURL),
		MessageBody:  aws.String(string(m.Body)),
		DelaySeconds: defaultDelaySeconds,
	})
	if err != nil {
		return fmt.Errorf("send SQS message: %w", err)
	}
	return nil
}

// PublishScaleUp marshals msg and publishes it via Publish. Provided as a
// typed convenience for callers (webhook dispatcher, scaledown re-enqueue)
// that already speak in *queue.ScaleUpMessage rather than queue.Msg.
func (p *Publisher) PublishScaleUp(ctx context.Context, msg *queue.ScaleUpMessage) error {
	if msg == nil {
		return fmt.Errorf("ScaleUpMessage is nil")
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal scale-up message: %w", err)
	}
	return p.Publish(ctx, queue.Msg{Body: body})
}
