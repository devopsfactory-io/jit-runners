package sqs

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type mockSQSReceiver struct {
	receiveOut    *sqs.ReceiveMessageOutput
	receiveErr    error
	deletedHandle string
	deleteErr     error
}

func (m *mockSQSReceiver) ReceiveMessage(_ context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	return m.receiveOut, m.receiveErr
}

func (m *mockSQSReceiver) DeleteMessage(_ context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	m.deletedHandle = aws.ToString(in.ReceiptHandle)
	return &sqs.DeleteMessageOutput{}, m.deleteErr
}

func TestConsumer_ReceiveAck_Roundtrip(t *testing.T) {
	mock := &mockSQSReceiver{
		receiveOut: &sqs.ReceiveMessageOutput{
			Messages: []types.Message{
				{Body: aws.String(`{"job_id":1}`), ReceiptHandle: aws.String("rh-1")},
				{Body: aws.String(`{"job_id":2}`), ReceiptHandle: aws.String("rh-2")},
			},
		},
	}
	c := NewConsumer(mock, "https://sqs.us-east-1.amazonaws.com/123/q")

	msgs, err := c.Receive(context.Background(), 10)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len = %d, want 2", len(msgs))
	}
	if msgs[0].ReceiptHandle != "rh-1" {
		t.Errorf("ReceiptHandle[0] = %q, want rh-1", msgs[0].ReceiptHandle)
	}
	if string(msgs[1].Body) != `{"job_id":2}` {
		t.Errorf("Body[1] = %q", string(msgs[1].Body))
	}

	if err := c.Ack(context.Background(), msgs[0].ReceiptHandle); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if mock.deletedHandle != "rh-1" {
		t.Errorf("deleted = %q, want rh-1", mock.deletedHandle)
	}
}

func TestConsumer_AckEmptyHandle(t *testing.T) {
	c := NewConsumer(&mockSQSReceiver{}, "https://sqs.us-east-1.amazonaws.com/123/q")
	if err := c.Ack(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty handle")
	}
}
