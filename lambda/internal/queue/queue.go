// Package queue defines the cloud-agnostic message queue contract used between
// the webhook and scaleup functions.
package queue

import "context"

// Msg is the payload exchanged between Publisher and Consumer.
type Msg struct {
	// Body is the raw JSON-encoded payload (e.g. a workflow_job event subset).
	Body []byte
	// ReceiptHandle is the implementation-specific token used to acknowledge
	// receipt. Empty when the message is being published.
	ReceiptHandle string
}

// Publisher publishes Msg to the queue. Used by the webhook function.
type Publisher interface {
	Publish(ctx context.Context, m Msg) error
}

// Consumer pulls Msg from the queue. Used by the scaleup function on the AWS
// path; on the GCP path scaleup receives messages via Pub/Sub push and uses
// the Consumer only to acknowledge.
type Consumer interface {
	Receive(ctx context.Context, max int) ([]Msg, error)
	Ack(ctx context.Context, handle string) error
}
