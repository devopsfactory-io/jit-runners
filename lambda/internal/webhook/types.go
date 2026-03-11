package webhook

// WorkflowJobEvent represents the GitHub workflow_job webhook event payload.
type WorkflowJobEvent struct {
	Action       string        `json:"action"`
	WorkflowJob  WorkflowJob   `json:"workflow_job"`
	Repository   Repository    `json:"repository"`
	Organization *Organization `json:"organization,omitempty"`
	Installation *Installation `json:"installation,omitempty"`
}

// WorkflowJob contains the job details from the webhook event.
type WorkflowJob struct {
	ID         int64    `json:"id"`
	RunID      int64    `json:"run_id"`
	Name       string   `json:"name"`
	Labels     []string `json:"labels"`
	RunnerName string   `json:"runner_name"`
	Status     string   `json:"status"`
}

// Repository identifies the repository that triggered the workflow.
type Repository struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
}

// Organization identifies the organization (if any).
type Organization struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

// Installation identifies the GitHub App installation.
type Installation struct {
	ID int64 `json:"id"`
}
