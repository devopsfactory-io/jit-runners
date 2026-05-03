package sqs

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/queue"
)

// Receiver abstracts the SQS Receive/Delete APIs for testing.
type Receiver interface {
	ReceiveMessage(ctx context.Context, input *sqs.ReceiveMessageInput, opts ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, input *sqs.DeleteMessageInput, opts ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}

// Consumer pulls messages from SQS. It satisfies queue.Consumer.
//
// In the AWS path the scaleup Lambda is invoked with an events.SQSEvent and
// does not call Receive directly; this Consumer exists so AWS can satisfy the
// cloud-agnostic interface and to support out-of-Lambda use cases (tests,
// local replay).
type Consumer struct {
	client   Receiver
	queueURL string
}

// Compile-time assertion that Consumer satisfies queue.Consumer.
var _ queue.Consumer = (*Consumer)(nil)

// NewConsumer creates a Consumer for the given queue URL.
func NewConsumer(client Receiver, queueURL string) *Consumer {
	return &Consumer{
		client:   client,
		queueURL: queueURL,
	}
}

// Receive pulls up to max messages from the queue. ReceiptHandle round-trips
// to Ack via the Msg.ReceiptHandle field.
func (c *Consumer) Receive(ctx context.Context, max int) ([]queue.Msg, error) {
	if max <= 0 {
		max = 1
	}
	if max > 10 { // SQS hard cap
		max = 10
	}
	out, err := c.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(c.queueURL),
		MaxNumberOfMessages: int32(max), //nolint:gosec // bounded above by 10
	})
	if err != nil {
		return nil, fmt.Errorf("receive SQS messages: %w", err)
	}
	msgs := make([]queue.Msg, 0, len(out.Messages))
	for _, m := range out.Messages {
		msgs = append(msgs, queue.Msg{
			Body:          []byte(aws.ToString(m.Body)),
			ReceiptHandle: aws.ToString(m.ReceiptHandle),
		})
	}
	return msgs, nil
}

// Ack deletes the message identified by handle from the queue.
func (c *Consumer) Ack(ctx context.Context, handle string) error {
	if handle == "" {
		return fmt.Errorf("empty receipt handle")
	}
	_, err := c.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(c.queueURL),
		ReceiptHandle: aws.String(handle),
	})
	if err != nil {
		return fmt.Errorf("delete SQS message: %w", err)
	}
	return nil
}

