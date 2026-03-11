package sqs

import (
	"encoding/json"
	"fmt"
)

// ParseMessage deserializes an SQS message body into a ScaleUpMessage.
func ParseMessage(body string) (*ScaleUpMessage, error) {
	var msg ScaleUpMessage
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		return nil, fmt.Errorf("unmarshal SQS message: %w", err)
	}
	if msg.JobID == 0 {
		return nil, fmt.Errorf("SQS message missing job_id")
	}
	if msg.RepositoryFull == "" {
		return nil, fmt.Errorf("SQS message missing repository_full")
	}
	if msg.InstallationID == 0 {
		return nil, fmt.Errorf("SQS message missing installation_id")
	}
	return &msg, nil
}
