// Package runtime provides HTTP entry-point helpers for Cloud Run functions.
// Specifically, it decodes Eventarc-delivered Pub/Sub CloudEvents into the
// raw message body our queue.Parse* helpers consume.
package runtime

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	cloudevents "github.com/cloudevents/sdk-go/v2"
)

// pubsubEnvelope mirrors the Eventarc Pub/Sub event payload shape.
//
// Eventarc Pub/Sub triggers wrap each message in:
//
//	{
//	  "message": {
//	    "data": "<base64-encoded body>",
//	    "attributes": {"k": "v"},
//	    "messageId": "..."
//	  },
//	  "subscription": "projects/<p>/subscriptions/<s>"
//	}
type pubsubEnvelope struct {
	Message struct {
		Data       string            `json:"data"`
		Attributes map[string]string `json:"attributes"`
		MessageID  string            `json:"messageId"`
	} `json:"message"`
	Subscription string `json:"subscription"`
}

// DecodePubSubData unwraps a CloudEvent of type
// google.cloud.pubsub.topic.v1.messagePublished and returns the
// base64-decoded message body. The body is what publishers sent via
// queue.PublishScaleUp / queue.PublishLifecycle.
func DecodePubSubData(e cloudevents.Event) ([]byte, error) {
	var env pubsubEnvelope
	if err := json.Unmarshal(e.Data(), &env); err != nil {
		return nil, fmt.Errorf("gcp/runtime: decode pubsub envelope: %w", err)
	}
	if env.Message.Data == "" {
		return nil, fmt.Errorf("gcp/runtime: pubsub envelope has no data field")
	}
	body, err := base64.StdEncoding.DecodeString(env.Message.Data)
	if err != nil {
		return nil, fmt.Errorf("gcp/runtime: base64 decode message data: %w", err)
	}
	return body, nil
}
