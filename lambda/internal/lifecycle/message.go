// Package lifecycle handles workflow_job in_progress and completed events.
package lifecycle

// Message is the payload published by webhook to the lifecycle queue.
// Mirrors the subset of GitHub's workflow_job event the handler needs.
type Message struct {
	JobID      int64  `json:"job_id"`
	Repo       string `json:"repo"`       // "owner/repo"
	RunnerID   int64  `json:"runner_id"`  // 0 if GH didn't assign one
	Action     string `json:"action"`     // "in_progress" | "completed"
	Conclusion string `json:"conclusion"` // "" for in_progress; success/failure/cancelled/skipped for completed
}
