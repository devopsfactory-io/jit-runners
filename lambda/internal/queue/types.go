package queue

// Source values for ScaleUpMessage. Empty string is treated as SourceWebhook
// for backwards compat with in-flight messages at deploy time.
const (
	SourceWebhook    = "webhook"
	SourceRebalancer = "rebalancer"
)

// ScaleUpMessage is the payload sent from the webhook Lambda or the
// rebalancer Lambda to the scale-up queue. Source disambiguates the
// trigger so scaleup can apply the correct demand-aware decision policy:
//
//   - SourceWebhook (default for empty Source): scaleup launches AT MOST 1
//     runner per message, only when GitHub queue depth exceeds the count of
//     pending runners matching msg.Labels.
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

// LifecycleMessage is the payload published by webhook to the lifecycle
// queue. Mirrors the subset of GitHub's workflow_job event the lifecycle
// handler needs to apply state transitions and deregister the runner.
type LifecycleMessage struct {
	JobID      int64  `json:"job_id"`
	Repo       string `json:"repo"`       // "owner/repo"
	RunnerID   int64  `json:"runner_id"`  // 0 if GH didn't assign one
	Action     string `json:"action"`     // "in_progress" | "completed"
	Conclusion string `json:"conclusion"` // "" for in_progress; success/failure/cancelled/skipped for completed
}
