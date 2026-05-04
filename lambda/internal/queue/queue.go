// Package queue defines the cloud-agnostic message queue contract used between
// the webhook and scaleup functions.
package queue

import "context"

// Msg is the payload published and consumed via the cloud queue.
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
