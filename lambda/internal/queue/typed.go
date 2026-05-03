package queue

import (
	"context"
	"encoding/json"
	"fmt"
)

// PublishScaleUp marshals msg to JSON and publishes it via p. The wire
// format matches the AWS SQS body shape that scaleup's parser expects.
// Both AWS SQS and GCP Pub/Sub publishers satisfy queue.Publisher.
func PublishScaleUp(ctx context.Context, p Publisher, msg *ScaleUpMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal scaleup message: %w", err)
	}
	return p.Publish(ctx, Msg{Body: body})
}

// PublishLifecycle marshals msg to JSON and publishes it via p.
func PublishLifecycle(ctx context.Context, p Publisher, msg *LifecycleMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal lifecycle message: %w", err)
	}
	return p.Publish(ctx, Msg{Body: body})
}

// ParseScaleUp decodes a queue body into a ScaleUpMessage and validates
// the required fields are present (non-zero). Returns a structured error
// for missing fields so the caller can distinguish malformed JSON from
// missing-field errors. Used by the scaleup function on both AWS (SQS
// body) and GCP (Pub/Sub message data).
func ParseScaleUp(body []byte) (*ScaleUpMessage, error) {
	var m ScaleUpMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parse scaleup message: %w", err)
	}
	if m.JobID == 0 {
		return nil, fmt.Errorf("parse scaleup message: missing job_id")
	}
	if m.RepositoryFull == "" {
		return nil, fmt.Errorf("parse scaleup message: missing repository_full")
	}
	if m.InstallationID == 0 {
		return nil, fmt.Errorf("parse scaleup message: missing installation_id")
	}
	return &m, nil
}

// ParseLifecycle decodes a queue body into a LifecycleMessage.
func ParseLifecycle(body []byte) (*LifecycleMessage, error) {
	var m LifecycleMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parse lifecycle message: %w", err)
	}
	return &m, nil
}
