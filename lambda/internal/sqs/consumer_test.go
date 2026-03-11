package sqs

import "testing"

func TestParseMessage(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid message",
			body: `{"event_action":"queued","job_id":123,"run_id":456,"repository_full":"org/repo","labels":["self-hosted"],"installation_id":789}`,
		},
		{
			name:    "invalid JSON",
			body:    `{invalid}`,
			wantErr: true,
		},
		{
			name:    "missing job_id",
			body:    `{"event_action":"queued","run_id":456,"repository_full":"org/repo","installation_id":789}`,
			wantErr: true,
			errMsg:  "SQS message missing job_id",
		},
		{
			name:    "missing repository_full",
			body:    `{"event_action":"queued","job_id":123,"run_id":456,"installation_id":789}`,
			wantErr: true,
			errMsg:  "SQS message missing repository_full",
		},
		{
			name:    "missing installation_id",
			body:    `{"event_action":"queued","job_id":123,"run_id":456,"repository_full":"org/repo"}`,
			wantErr: true,
			errMsg:  "SQS message missing installation_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := ParseMessage(tt.body)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("error = %q, want %q", err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if msg.JobID != 123 {
				t.Errorf("jobID = %d, want 123", msg.JobID)
			}
			if msg.RepositoryFull != "org/repo" {
				t.Errorf("repo = %q, want %q", msg.RepositoryFull, "org/repo")
			}
		})
	}
}
