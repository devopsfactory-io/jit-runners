package sqs

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/queue"
)

type mockSQSSender struct {
	lastInput *sqs.SendMessageInput
	err       error
}

func (m *mockSQSSender) SendMessage(_ context.Context, input *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	m.lastInput = input
	return &sqs.SendMessageOutput{}, m.err
}

func TestPublisher_Publish(t *testing.T) {
	tests := []struct {
		name    string
		msg     *queue.ScaleUpMessage
		sendErr error
		wantErr bool
	}{
		{
			name: "successful publish",
			msg: &queue.ScaleUpMessage{
				EventAction:    "queued",
				JobID:          123,
				RunID:          456,
				RepositoryFull: "org/repo",
				Labels:         []string{"self-hosted", "linux"},
				InstallationID: 789,
			},
		},
		{
			name: "send error",
			msg: &queue.ScaleUpMessage{
				EventAction:    "queued",
				JobID:          123,
				RepositoryFull: "org/repo",
				InstallationID: 789,
			},
			sendErr: context.DeadlineExceeded,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockSQSSender{err: tt.sendErr}
			pub := NewPublisher(mock, "https://sqs.us-east-1.amazonaws.com/123456789/test-queue")

			body, err := json.Marshal(tt.msg)
			if err != nil {
				t.Fatalf("marshal test msg: %v", err)
			}

			err = pub.Publish(context.Background(), queue.Msg{Body: body})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify the message was sent correctly.
			if mock.lastInput == nil {
				t.Fatal("no message sent")
			}
			if mock.lastInput.DelaySeconds != 30 {
				t.Errorf("delay = %d, want 30", mock.lastInput.DelaySeconds)
			}

			// Verify message body can be deserialized.
			var got queue.ScaleUpMessage
			if err := json.Unmarshal([]byte(*mock.lastInput.MessageBody), &got); err != nil {
				t.Fatalf("unmarshal sent message: %v", err)
			}
			if got.JobID != tt.msg.JobID {
				t.Errorf("jobID = %d, want %d", got.JobID, tt.msg.JobID)
			}
			if got.RepositoryFull != tt.msg.RepositoryFull {
				t.Errorf("repo = %q, want %q", got.RepositoryFull, tt.msg.RepositoryFull)
			}
		})
	}
}

func TestPublisher_EmptyBody(t *testing.T) {
	mock := &mockSQSSender{}
	pub := NewPublisher(mock, "https://sqs.us-east-1.amazonaws.com/123/q")
	if err := pub.Publish(context.Background(), queue.Msg{}); err == nil {
		t.Fatal("expected error for empty body, got nil")
	}
}
