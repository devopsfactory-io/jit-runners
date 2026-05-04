package runner

import (
	"strconv"
	"testing"
)

func TestIDFromGitHubRunnerID(t *testing.T) {
	tests := []struct {
		ghID int64
		want string
	}{
		{12345, "12345"},
		{1, "1"},
		{0, "0"},
	}
	for _, tt := range tests {
		got := IDFromGitHubRunnerID(tt.ghID)
		if got != tt.want {
			t.Errorf("IDFromGitHubRunnerID(%d) = %q, want %q", tt.ghID, got, tt.want)
		}
	}
}

func TestNew(t *testing.T) {
	r := New("org/repo", 99887766, "i-abc123", 12345, 67890, []string{"self-hosted", "large"})

	if r.ID != "99887766" {
		t.Errorf("ID = %q, want %q", r.ID, "99887766")
	}
	if r.GitHubRunnerID != 99887766 {
		t.Errorf("GitHubRunnerID = %d, want 99887766", r.GitHubRunnerID)
	}
	if r.JobID != 12345 {
		t.Errorf("JobID = %d, want 12345", r.JobID)
	}
	if r.WorkflowRunID != 67890 {
		t.Errorf("WorkflowRunID = %d, want 67890", r.WorkflowRunID)
	}
	if r.InstanceID != "i-abc123" {
		t.Errorf("InstanceID = %q", r.InstanceID)
	}
	if r.Repository != "org/repo" {
		t.Errorf("Repository = %q", r.Repository)
	}
	if r.Status != StatusPending {
		t.Errorf("Status = %q, want %q", r.Status, StatusPending)
	}
	if r.LaunchedAt.IsZero() {
		t.Error("LaunchedAt should be non-zero")
	}
	if !r.TTL.After(r.LaunchedAt) {
		t.Error("TTL should be after LaunchedAt")
	}
	// Defensive: ID and GitHubRunnerID must be consistent.
	if r.ID != strconv.FormatInt(r.GitHubRunnerID, 10) {
		t.Errorf("ID and GitHubRunnerID inconsistent: ID=%q GitHubRunnerID=%d", r.ID, r.GitHubRunnerID)
	}
}
