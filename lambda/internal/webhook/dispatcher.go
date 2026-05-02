package webhook

import (
	"context"
	"fmt"

	internalsqs "github.com/devopsfactory-io/jit-runners/lambda/internal/aws/sqs"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/github"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/lifecycle"
)

// scaleUpPublisher is the minimal surface the Handler needs from a
// scale-up queue publisher. *internalsqs.Publisher satisfies this via its
// PublishScaleUp helper.
type scaleUpPublisher interface {
	PublishScaleUp(ctx context.Context, msg *internalsqs.ScaleUpMessage) error
}

// lifecyclePublisher is the minimal surface the Handler needs from the
// lifecycle queue publisher. *internalsqs.LifecyclePublisher satisfies this.
type lifecyclePublisher interface {
	Publish(ctx context.Context, msg *lifecycle.Message) error
}

// Handler dispatches GitHub workflow_job webhook events to the correct
// downstream queue based on ev.Action.
//
//   - queued      -> publishes a ScaleUpMessage on the scale-up queue.
//   - in_progress -> publishes a lifecycle.Message on the lifecycle queue.
//   - completed   -> publishes a lifecycle.Message on the lifecycle queue.
//   - other       -> 200 OK, no publish.
type Handler struct {
	ScaleUpPublisher   scaleUpPublisher
	LifecyclePublisher lifecyclePublisher
	WebhookSecret      []byte
}

// NewHandler builds a Handler with the two publishers and the webhook
// signing secret. webhookSecret is the raw bytes of the shared HMAC key.
func NewHandler(scaleUp scaleUpPublisher, lifecyclePub lifecyclePublisher, webhookSecret []byte) *Handler {
	return &Handler{
		ScaleUpPublisher:   scaleUp,
		LifecyclePublisher: lifecyclePub,
		WebhookSecret:      webhookSecret,
	}
}

// Response is the abstract outcome of handling a webhook delivery.
// Callers (cmd/webhook/main.go for Lambda, an http.Handler shim if/when
// added) translate this into transport-specific responses.
type Response struct {
	Status int
	Body   string
}

// Handle processes one webhook delivery. It performs:
//  1. HMAC signature verification (401 on failure).
//  2. Dispatch by event type (only workflow_job is acted on).
//  3. JSON parse (400 on failure).
//  4. Action dispatch (queued/in_progress/completed/other).
//
// eventType is the value of the X-GitHub-Event header.
// signature is the value of the X-Hub-Signature-256 header.
// body is the raw request body the signature was computed over.
func (h *Handler) Handle(ctx context.Context, eventType, signature string, body []byte) Response {
	if err := github.VerifyWebhookSignature(body, signature, string(h.WebhookSecret)); err != nil {
		return Response{Status: 401, Body: "Invalid signature"}
	}

	if eventType != "workflow_job" {
		return Response{Status: 200, Body: "OK"}
	}

	result, err := Parse(body)
	if err != nil {
		return Response{Status: 400, Body: "Bad payload"}
	}

	switch result.Action {
	case ActionQueued:
		return h.handleQueued(ctx, result)
	case ActionInProgress, ActionCompleted:
		return h.handleLifecycle(ctx, result)
	default:
		return Response{Status: 200, Body: "OK"}
	}
}

// handleQueued publishes a scale-up message when Parse marked the event
// as needing a new runner. Otherwise it returns 200 OK without publishing
// (e.g. non-self-hosted job, missing data flagged but recoverable).
func (h *Handler) handleQueued(ctx context.Context, result *ParseResult) Response {
	if !result.ShouldScale {
		return Response{Status: 200, Body: "OK"}
	}

	msg := &internalsqs.ScaleUpMessage{
		EventAction:    result.Action,
		JobID:          result.Event.WorkflowJob.ID,
		RunID:          result.Event.WorkflowJob.RunID,
		RepositoryFull: result.Event.Repository.FullName,
		Labels:         result.Event.WorkflowJob.Labels,
		InstallationID: result.Event.Installation.ID,
		Source:         internalsqs.SourceWebhook,
	}
	if err := h.ScaleUpPublisher.PublishScaleUp(ctx, msg); err != nil {
		return Response{Status: 500, Body: "Queue error"}
	}
	return Response{Status: 200, Body: "OK"}
}

// handleLifecycle publishes a lifecycle.Message for in_progress / completed
// events that target self-hosted runners. Non-self-hosted jobs are dropped
// (200 OK) because they have no associated runner state to update.
func (h *Handler) handleLifecycle(ctx context.Context, result *ParseResult) Response {
	if !HasSelfHostedLabel(result.Event.WorkflowJob.Labels) {
		return Response{Status: 200, Body: "OK"}
	}

	if h.LifecyclePublisher == nil {
		return Response{Status: 500, Body: "Lifecycle publisher not configured"}
	}

	msg := &lifecycle.Message{
		JobID:      result.Event.WorkflowJob.ID,
		Repo:       result.Event.Repository.FullName,
		RunnerID:   result.Event.WorkflowJob.RunnerID,
		Action:     result.Action,
		Conclusion: result.Event.WorkflowJob.Conclusion,
	}
	if err := h.LifecyclePublisher.Publish(ctx, msg); err != nil {
		return Response{Status: 500, Body: "Queue error"}
	}
	return Response{Status: 202, Body: "Accepted"}
}

// String returns a short description of the response for log lines.
func (r Response) String() string {
	return fmt.Sprintf("status=%d body=%q", r.Status, r.Body)
}
