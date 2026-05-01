package runner

import "testing"

func TestNew(t *testing.T) {
	r := New("org/repo", 123, "i-abc123", []string{"self-hosted", "linux"})

	if r.ID != "org/repo#123" {
		t.Errorf("ID = %q, want %q", r.ID, "org/repo#123")
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
}

func TestID(t *testing.T) {
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
		got := ID(tt.repo, tt.jobID)
		if got != tt.want {
			t.Errorf("ID(%q, %d) = %q, want %q", tt.repo, tt.jobID, got, tt.want)
		}
	}
}
