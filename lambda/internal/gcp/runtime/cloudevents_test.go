package runtime

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	cloudevents "github.com/cloudevents/sdk-go/v2"
)

func TestDecodePubSubData_ExtractsBase64Body(t *testing.T) {
	// Eventarc Pub/Sub events wrap the message in a JSON envelope:
	//   {"message":{"data":"<base64>","attributes":{...}},"subscription":"..."}
	// We test the extraction returns the base64-decoded data bytes.
	body := []byte(`{"job_id":7,"repository_full":"owner/repo","installation_id":1}`)
	envelope := map[string]any{
		"message": map[string]any{
			"data":       base64.StdEncoding.EncodeToString(body),
			"attributes": map[string]string{"k": "v"},
		},
		"subscription": "projects/p/subscriptions/s",
	}
	envelopeJSON, _ := json.Marshal(envelope)

	e := cloudevents.NewEvent()
	e.SetType("google.cloud.pubsub.topic.v1.messagePublished")
	e.SetSource("//pubsub.googleapis.com/projects/p/topics/t")
	if err := e.SetData(cloudevents.ApplicationJSON, envelopeJSON); err != nil {
		t.Fatalf("SetData: %v", err)
	}

	got, err := DecodePubSubData(e)
	if err != nil {
		t.Fatalf("DecodePubSubData: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("body mismatch: got %q want %q", got, body)
	}
}

func TestDecodePubSubData_BadEnvelopeReturnsError(t *testing.T) {
	e := cloudevents.NewEvent()
	e.SetType("google.cloud.pubsub.topic.v1.messagePublished")
	e.SetSource("//pubsub.googleapis.com/projects/p/topics/t")
	_ = e.SetData(cloudevents.ApplicationJSON, []byte("not json"))

	if _, err := DecodePubSubData(e); err == nil {
		t.Fatal("expected error on bad envelope")
	}
}

func TestDecodePubSubData_BadBase64ReturnsError(t *testing.T) {
	envelope := map[string]any{
		"message": map[string]any{
			"data": "not-valid-base64!!",
		},
	}
	envelopeJSON, _ := json.Marshal(envelope)
	e := cloudevents.NewEvent()
	e.SetType("google.cloud.pubsub.topic.v1.messagePublished")
	e.SetSource("//pubsub.googleapis.com/projects/p/topics/t")
	_ = e.SetData(cloudevents.ApplicationJSON, envelopeJSON)

	if _, err := DecodePubSubData(e); err == nil {
		t.Fatal("expected error on bad base64")
	}
}
