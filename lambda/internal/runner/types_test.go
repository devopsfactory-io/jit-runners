package runner

import "testing"

func TestNewRecord(t *testing.T) {
	rec := NewRecord("org/repo", 123, 456, "i-abc123", []string{"self-hosted", "linux"})

	if rec.RunnerID != "org/repo#123" {
		t.Errorf("RunnerID = %q, want %q", rec.RunnerID, "org/repo#123")
	}
	if rec.InstanceID != "i-abc123" {
		t.Errorf("InstanceID = %q", rec.InstanceID)
	}
	if rec.JobID != 123 {
		t.Errorf("JobID = %d, want 123", rec.JobID)
	}
	if rec.RunID != 456 {
		t.Errorf("RunID = %d, want 456", rec.RunID)
	}
	if rec.Status != StatusPending {
		t.Errorf("Status = %q, want %q", rec.Status, StatusPending)
	}
	if rec.CreatedAt == 0 {
		t.Error("CreatedAt should be non-zero")
	}
	if rec.TTL <= rec.CreatedAt {
		t.Error("TTL should be greater than CreatedAt")
	}
}

func TestRunnerID(t *testing.T) {
	tests := []struct {
		repo  string
		jobID int64
		want  string
	}{
		{"org/repo", 123, "org/repo#123"},
		{"user/project", 0, "user/project#0"},
		{"a/b", 999999, "a/b#999999"},
	}
	for _, tt := range tests {
		got := runnerID(tt.repo, tt.jobID)
		if got != tt.want {
			t.Errorf("runnerID(%q, %d) = %q, want %q", tt.repo, tt.jobID, got, tt.want)
		}
	}
}
