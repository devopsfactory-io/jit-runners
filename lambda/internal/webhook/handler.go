package webhook

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Action constants for workflow_job events.
const (
	ActionQueued     = "queued"
	ActionInProgress = "in_progress"
	ActionCompleted  = "completed"
	ActionWaiting    = "waiting"
)

// ParseResult contains the parsed webhook event and routing decision.
type ParseResult struct {
	Event  *WorkflowJobEvent
	Action string
	// ShouldScale is true when a new runner should be provisioned.
	ShouldScale bool
}

// Parse parses a workflow_job webhook payload and determines if scaling is needed.
// Returns an error if the payload is malformed.
// Returns a ParseResult with ShouldScale=false for events that don't need a new runner.
func Parse(body []byte) (*ParseResult, error) {
	var event WorkflowJobEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("unmarshal workflow_job event: %w", err)
	}

	result := &ParseResult{
		Event:  &event,
		Action: event.Action,
	}

	// Only "queued" events trigger scaling.
	if event.Action != ActionQueued {
		return result, nil
	}

	// The job must request self-hosted runners.
	if !hasSelfHostedLabel(event.WorkflowJob.Labels) {
		return result, nil
	}

	// Must have an installation ID for GitHub App auth.
	if event.Installation == nil || event.Installation.ID == 0 {
		return nil, fmt.Errorf("workflow_job event missing installation ID")
	}

	result.ShouldScale = true
	return result, nil
}

// hasSelfHostedLabel checks if the labels include "self-hosted".
func hasSelfHostedLabel(labels []string) bool {
	for _, l := range labels {
		if strings.EqualFold(l, "self-hosted") {
			return true
		}
	}
	return false
}

// CustomLabels returns the job's labels excluding standard GitHub labels
// (self-hosted, linux, macos, windows, x64, arm64).
func CustomLabels(labels []string) []string {
	standard := map[string]bool{
		"self-hosted": true,
		"linux":       true,
		"macos":       true,
		"windows":     true,
		"x64":         true,
		"arm64":       true,
		"arm":         true,
	}
	var custom []string
	for _, l := range labels {
		if !standard[strings.ToLower(l)] {
			custom = append(custom, l)
		}
	}
	return custom
}
