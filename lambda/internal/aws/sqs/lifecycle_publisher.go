package sqs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/lifecycle"
)

// LifecyclePublisher sends workflow_job lifecycle events
// (in_progress, completed) to the lifecycle SQS queue.
//
// Mirrors Publisher in shape, but targets a different queue and uses no
// delivery delay: lifecycle events should be processed promptly so the
// runner state machine in DDB stays in sync with GitHub.
type LifecyclePublisher struct {
	client   Sender
	queueURL string
}

// NewLifecyclePublisher creates a LifecyclePublisher for the given queue URL.
func NewLifecyclePublisher(client Sender, queueURL string) *LifecyclePublisher {
	return &LifecyclePublisher{
		client:   client,
		queueURL: queueURL,
	}
}

// Publish sends a lifecycle.Message to the SQS queue with no delay.
func (p *LifecyclePublisher) Publish(ctx context.Context, msg *lifecycle.Message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal lifecycle message: %w", err)
	}

	_, err = p.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(p.queueURL),
		MessageBody: aws.String(string(body)),
	})
	if err != nil {
		return fmt.Errorf("send lifecycle SQS message: %w", err)
	}
	return nil
}
