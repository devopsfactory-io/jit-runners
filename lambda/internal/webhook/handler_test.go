package webhook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		fixture     string
		wantAction  string
		wantScale   bool
		wantErr     bool
	}{
		{
			name:       "queued event triggers scaling",
			fixture:    "workflow_job.queued.json",
			wantAction: "queued",
			wantScale:  true,
		},
		{
			name:       "completed event does not trigger scaling",
			fixture:    "workflow_job.completed.json",
			wantAction: "completed",
			wantScale:  false,
		},
		{
			name:       "in_progress event does not trigger scaling",
			fixture:    "workflow_job.in_progress.json",
			wantAction: "in_progress",
			wantScale:  false,
		},
		{
			name:    "malformed JSON",
			fixture: "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body []byte
			if tt.fixture != "" {
				var err error
				body, err = os.ReadFile(filepath.Join("..", "..", "mocks", tt.fixture))
				if err != nil {
					t.Fatalf("read fixture: %v", err)
				}
			} else {
				body = []byte(`{invalid json}`)
			}

			result, err := Parse(body)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Action != tt.wantAction {
				t.Errorf("action = %q, want %q", result.Action, tt.wantAction)
			}
			if result.ShouldScale != tt.wantScale {
				t.Errorf("shouldScale = %v, want %v", result.ShouldScale, tt.wantScale)
			}
		})
	}
}

func TestParse_NoSelfHostedLabel(t *testing.T) {
	body := []byte(`{
		"action": "queued",
		"workflow_job": {
			"id": 1,
			"run_id": 2,
			"name": "build",
			"labels": ["ubuntu-latest"],
			"status": "queued"
		},
		"repository": {"id": 1, "full_name": "org/repo", "private": true},
		"installation": {"id": 123}
	}`)

	result, err := Parse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ShouldScale {
		t.Error("expected ShouldScale=false for non-self-hosted labels")
	}
}

func TestParse_MissingInstallation(t *testing.T) {
	body := []byte(`{
		"action": "queued",
		"workflow_job": {
			"id": 1,
			"run_id": 2,
			"name": "build",
			"labels": ["self-hosted", "linux"],
			"status": "queued"
		},
		"repository": {"id": 1, "full_name": "org/repo", "private": true}
	}`)

	_, err := Parse(body)
	if err == nil {
		t.Fatal("expected error for missing installation")
	}
}

func TestCustomLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		want   []string
	}{
		{
			name:   "filters standard labels",
			labels: []string{"self-hosted", "linux", "x64", "gpu"},
			want:   []string{"gpu"},
		},
		{
			name:   "all standard",
			labels: []string{"self-hosted", "linux", "x64"},
			want:   nil,
		},
		{
			name:   "multiple custom",
			labels: []string{"self-hosted", "linux", "gpu", "large"},
			want:   []string{"gpu", "large"},
		},
		{
			name:   "empty",
			labels: nil,
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CustomLabels(tt.labels)
			if len(got) != len(tt.want) {
				t.Errorf("len = %d, want %d", len(got), len(tt.want))
				return
			}
			for i, v := range got {
				if v != tt.want[i] {
					t.Errorf("label[%d] = %q, want %q", i, v, tt.want[i])
				}
			}
		})
	}
}
