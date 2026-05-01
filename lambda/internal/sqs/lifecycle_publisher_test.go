package sqs

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/lifecycle"
)

func TestLifecyclePublisher_Publish(t *testing.T) {
	tests := []struct {
		name    string
		msg     *lifecycle.Message
		sendErr error
		wantErr bool
	}{
		{
			name: "in_progress publish",
			msg: &lifecycle.Message{
				JobID:    111,
				Repo:     "org/repo",
				RunnerID: 22,
				Action:   "in_progress",
			},
		},
		{
			name: "completed publish carries conclusion",
			msg: &lifecycle.Message{
				JobID:      111,
				Repo:       "org/repo",
				RunnerID:   22,
				Action:     "completed",
				Conclusion: "success",
			},
		},
		{
			name: "send error",
			msg: &lifecycle.Message{
				JobID:  111,
				Repo:   "org/repo",
				Action: "in_progress",
			},
			sendErr: context.DeadlineExceeded,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockSQSSender{err: tt.sendErr}
			pub := NewLifecyclePublisher(mock, "https://sqs.us-east-1.amazonaws.com/123456789/lifecycle-queue")

			err := pub.Publish(context.Background(), tt.msg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if mock.lastInput == nil {
				t.Fatal("no message sent")
			}
			// Lifecycle queue: no delivery delay, unlike scaleup.
			if mock.lastInput.DelaySeconds != 0 {
				t.Errorf("delay = %d, want 0", mock.lastInput.DelaySeconds)
			}

			var got lifecycle.Message
			if err := json.Unmarshal([]byte(*mock.lastInput.MessageBody), &got); err != nil {
				t.Fatalf("unmarshal sent message: %v", err)
			}
			if got.JobID != tt.msg.JobID {
				t.Errorf("jobID = %d, want %d", got.JobID, tt.msg.JobID)
			}
			if got.Repo != tt.msg.Repo {
				t.Errorf("repo = %q, want %q", got.Repo, tt.msg.Repo)
			}
			if got.RunnerID != tt.msg.RunnerID {
				t.Errorf("runner_id = %d, want %d", got.RunnerID, tt.msg.RunnerID)
			}
			if got.Action != tt.msg.Action {
				t.Errorf("action = %q, want %q", got.Action, tt.msg.Action)
			}
			if got.Conclusion != tt.msg.Conclusion {
				t.Errorf("conclusion = %q, want %q", got.Conclusion, tt.msg.Conclusion)
			}
		})
	}
}
