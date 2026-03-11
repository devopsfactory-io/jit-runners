package sqs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

const defaultDelaySeconds = 30

// SQSSender abstracts the SQS SendMessage API for testing.
type SQSSender interface {
	SendMessage(ctx context.Context, input *sqs.SendMessageInput, opts ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// Publisher sends scale-up messages to SQS.
type Publisher struct {
	client   SQSSender
	queueURL string
}

// NewPublisher creates a Publisher for the given queue URL.
func NewPublisher(client SQSSender, queueURL string) *Publisher {
	return &Publisher{
		client:   client,
		queueURL: queueURL,
	}
}

// Publish sends a ScaleUpMessage to the SQS queue with a delay.
func (p *Publisher) Publish(ctx context.Context, msg *ScaleUpMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal SQS message: %w", err)
	}

	_, err = p.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:     aws.String(p.queueURL),
		MessageBody:  aws.String(string(body)),
		DelaySeconds: defaultDelaySeconds,
	})
	if err != nil {
		return fmt.Errorf("send SQS message: %w", err)
	}
	return nil
}
