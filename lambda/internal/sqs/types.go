package sqs

// ScaleUpMessage is the SQS message sent from the webhook Lambda to the scale-up Lambda.
type ScaleUpMessage struct {
	EventAction    string   `json:"event_action"`
	JobID          int64    `json:"job_id"`
	RunID          int64    `json:"run_id"`
	RepositoryFull string   `json:"repository_full"`
	Labels         []string `json:"labels"`
	InstallationID int64    `json:"installation_id"`
}
