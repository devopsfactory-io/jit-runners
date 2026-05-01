package webhook

import "strings"

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
	RunnerID   int64    `json:"runner_id"`
	Status     string   `json:"status"`
	Conclusion string   `json:"conclusion"`
}

// HasSelfHostedLabel returns true when the labels include "self-hosted"
// (case-insensitive). Exposed for use by the lifecycle dispatch path so
// we drop events that target GitHub-hosted runners.
func HasSelfHostedLabel(labels []string) bool {
	for _, l := range labels {
		if strings.EqualFold(l, "self-hosted") {
			return true
		}
	}
	return false
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
