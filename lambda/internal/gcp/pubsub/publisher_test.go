package pubsub

import (
	"context"
	"fmt"
	"testing"

	"cloud.google.com/go/pubsub/v2"
	pb "cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/v2/pstest"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/queue"
)

func newTestPublisher(t *testing.T, topicID string) (*Publisher, *pstest.Server, func()) {
	t.Helper()
	srv := pstest.NewServer()
	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	client, err := pubsub.NewClient(context.Background(), "test-project", option.WithGRPCConn(conn))
	if err != nil {
		t.Fatalf("pubsub.NewClient: %v", err)
	}
	topicName := fmt.Sprintf("projects/test-project/topics/%s", topicID)
	if _, err := client.TopicAdminClient.CreateTopic(context.Background(), &pb.Topic{Name: topicName}); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	pub := NewPublisher(client, topicName)
	cleanup := func() {
		_ = client.Close()
		_ = conn.Close()
		_ = srv.Close()
	}
	return pub, srv, cleanup
}

func TestPublisher_Publish_DeliversBodyToTopic(t *testing.T) {
	pub, srv, cleanup := newTestPublisher(t, "test-topic")
	defer cleanup()

	body := []byte(`{"job_id":7,"repository_full":"owner/repo"}`)
	if err := pub.Publish(context.Background(), queue.Msg{Body: body}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if string(msgs[0].Data) != string(body) {
		t.Errorf("body mismatch: got %q, want %q", msgs[0].Data, body)
	}
}

func TestPublisher_Publish_EmptyBodyReturnsError(t *testing.T) {
	pub, _, cleanup := newTestPublisher(t, "test-topic")
	defer cleanup()

	err := pub.Publish(context.Background(), queue.Msg{Body: nil})
	if err == nil {
		t.Fatal("expected error on empty body, got nil")
	}
}

func TestPublisher_Publish_PublisherClosed(t *testing.T) {
	pub, _, cleanup := newTestPublisher(t, "test-topic")
	cleanup() // close BEFORE publishing

	err := pub.Publish(context.Background(), queue.Msg{Body: []byte("x")})
	if err == nil {
		t.Fatal("expected error when client closed")
	}
}
