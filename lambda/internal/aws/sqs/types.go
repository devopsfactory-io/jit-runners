package sqs

// Source values for ScaleUpMessage. Empty string is treated as SourceWebhook
// for backwards compat with in-flight messages at deploy time.
const (
	SourceWebhook    = "webhook"
	SourceRebalancer = "rebalancer"
)

// ScaleUpMessage is the SQS message sent from the webhook Lambda or the
// rebalancer Lambda to the scale-up Lambda. Source disambiguates the
// trigger so scaleup can apply the correct demand-aware decision policy:
//
//   - SourceWebhook (default for empty Source): scaleup launches AT MOST 1
//     runner per message, only when GitHub queue depth exceeds the count of
//     DDB pending runners matching msg.Labels.
//   - SourceRebalancer: scaleup launches 1 runner unconditionally per
//     message — the rebalancer pre-counted the gap and publishes one
//     message per missing slot.
type ScaleUpMessage struct {
	EventAction    string   `json:"event_action"`
	JobID          int64    `json:"job_id"`
	RunID          int64    `json:"run_id"`
	RepositoryFull string   `json:"repository_full"`
	Labels         []string `json:"labels"`
	InstallationID int64    `json:"installation_id"`

	// Source identifies which Lambda published this message. See the
	// constants above. Empty Source is treated as SourceWebhook.
	Source string `json:"source,omitempty"`

	// ReEnqueueAttempts is incremented by scaledown each time a stuck pending
	// runner triggers a re-enqueue. Scaleup uses it as a budget check (skip
	// launch when attempts >= MaxReEnqueueAttempts). Default zero.
	ReEnqueueAttempts int `json:"re_enqueue_attempts,omitempty"`
}
