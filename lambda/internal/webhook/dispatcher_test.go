package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/queue"
)

// fakeScaleUpPublisher captures the last published ScaleUpMessage (decoded
// from the generic queue.Msg body) so tests can assert on typed fields.
type fakeScaleUpPublisher struct {
	calls   int
	last    *queue.ScaleUpMessage
	failErr error
}

func (f *fakeScaleUpPublisher) Publish(_ context.Context, m queue.Msg) error {
	if f.failErr != nil {
		f.calls++
		return f.failErr
	}
	var msg queue.ScaleUpMessage
	if err := json.Unmarshal(m.Body, &msg); err != nil {
		return err
	}
	f.calls++
	f.last = &msg
	return nil
}

// fakeLifecyclePublisher captures the last queue.LifecycleMessage seen.
type fakeLifecyclePublisher struct {
	calls   int
	last    *queue.LifecycleMessage
	rawBody []byte // raw body bytes, for wire-shape assertions
	failErr error
}

func (f *fakeLifecyclePublisher) Publish(_ context.Context, m queue.Msg) error {
	if f.failErr != nil {
		f.calls++
		return f.failErr
	}
	var msg queue.LifecycleMessage
	if err := json.Unmarshal(m.Body, &msg); err != nil {
		return err
	}
	f.calls++
	f.last = &msg
	f.rawBody = m.Body
	return nil
}

const testSecret = "shhh"

func sign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "mocks", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

func newTestHandler() (*Handler, *fakeScaleUpPublisher, *fakeLifecyclePublisher) {
	scaleUp := &fakeScaleUpPublisher{}
	lifeP := &fakeLifecyclePublisher{}
	return NewHandler(scaleUp, lifeP, []byte(testSecret)), scaleUp, lifeP
}

func TestHandler_Handle_QueuedPublishesToScaleUp(t *testing.T) {
	body := loadFixture(t, "workflow_job.queued.json")
	h, scaleUp, lifeP := newTestHandler()

	resp := h.Handle(context.Background(), "workflow_job", sign(body), body)

	if resp.Status != 200 {
		t.Fatalf("status = %d, want 200", resp.Status)
	}
	if scaleUp.calls != 1 {
		t.Errorf("scaleUp.calls = %d, want 1", scaleUp.calls)
	}
	if lifeP.calls != 0 {
		t.Errorf("lifecycle.calls = %d, want 0 (no lifecycle publish on queued)", lifeP.calls)
	}
	if scaleUp.last == nil {
		t.Fatal("no scale-up message captured")
	}
	if scaleUp.last.JobID != 123456789 {
		t.Errorf("scaleUp.JobID = %d, want 123456789", scaleUp.last.JobID)
	}
	if scaleUp.last.RepositoryFull != "devopsfactory-io/jit-runners" {
		t.Errorf("scaleUp.RepositoryFull = %q", scaleUp.last.RepositoryFull)
	}
	if scaleUp.last.EventAction != "queued" {
		t.Errorf("scaleUp.EventAction = %q, want queued", scaleUp.last.EventAction)
	}
	if scaleUp.last.Source != queue.SourceWebhook {
		t.Errorf("Source = %q, want %q", scaleUp.last.Source, queue.SourceWebhook)
	}
}

func TestHandler_Handle_QueuedPublishError(t *testing.T) {
	body := loadFixture(t, "workflow_job.queued.json")
	h, scaleUp, _ := newTestHandler()
	scaleUp.failErr = errors.New("boom")

	resp := h.Handle(context.Background(), "workflow_job", sign(body), body)

	if resp.Status != 500 {
		t.Errorf("status = %d, want 500", resp.Status)
	}
}

func TestHandler_Handle_InProgressPublishesToLifecycle(t *testing.T) {
	body := loadFixture(t, "workflow_job.in_progress.json")
	h, scaleUp, lifeP := newTestHandler()

	resp := h.Handle(context.Background(), "workflow_job", sign(body), body)

	if resp.Status != 202 {
		t.Errorf("status = %d, want 202 Accepted", resp.Status)
	}
	if scaleUp.calls != 0 {
		t.Errorf("scaleUp.calls = %d, want 0", scaleUp.calls)
	}
	if lifeP.calls != 1 {
		t.Fatalf("lifecycle.calls = %d, want 1", lifeP.calls)
	}

	// Assert wire shape: round-trip the published bytes and check fields.
	var got queue.LifecycleMessage
	if err := json.Unmarshal(lifeP.rawBody, &got); err != nil {
		t.Fatalf("unmarshal published lifecycle msg: %v", err)
	}
	if got.Action != "in_progress" {
		t.Errorf("Action = %q, want in_progress", got.Action)
	}
	if got.JobID != 123456789 {
		t.Errorf("JobID = %d, want 123456789", got.JobID)
	}
	if got.Repo != "devopsfactory-io/jit-runners" {
		t.Errorf("Repo = %q", got.Repo)
	}
	// Conclusion is empty for in_progress.
	if got.Conclusion != "" {
		t.Errorf("Conclusion = %q, want \"\"", got.Conclusion)
	}
}

func TestHandler_Handle_CompletedPublishesToLifecycle(t *testing.T) {
	body := loadFixture(t, "workflow_job.completed.json")
	h, scaleUp, lifeP := newTestHandler()

	resp := h.Handle(context.Background(), "workflow_job", sign(body), body)

	if resp.Status != 202 {
		t.Errorf("status = %d, want 202", resp.Status)
	}
	if scaleUp.calls != 0 {
		t.Errorf("scaleUp.calls = %d, want 0", scaleUp.calls)
	}
	if lifeP.calls != 1 {
		t.Fatalf("lifecycle.calls = %d, want 1", lifeP.calls)
	}
	if lifeP.last.Action != "completed" {
		t.Errorf("Action = %q, want completed", lifeP.last.Action)
	}
}

func TestHandler_Handle_LifecyclePublishError(t *testing.T) {
	body := loadFixture(t, "workflow_job.in_progress.json")
	h, _, lifeP := newTestHandler()
	lifeP.failErr = errors.New("boom")

	resp := h.Handle(context.Background(), "workflow_job", sign(body), body)

	if resp.Status != 500 {
		t.Errorf("status = %d, want 500 on publish failure", resp.Status)
	}
}

func TestHandler_Handle_LifecycleNonSelfHostedDropped(t *testing.T) {
	// In-progress event with no self-hosted label.
	raw := []byte(`{
		"action": "in_progress",
		"workflow_job": {
			"id": 42,
			"run_id": 100,
			"name": "build",
			"labels": ["ubuntu-latest"],
			"runner_name": "github-hosted",
			"runner_id": 99,
			"status": "in_progress"
		},
		"repository": {"id": 1, "full_name": "org/repo", "private": true},
		"installation": {"id": 1}
	}`)
	h, scaleUp, lifeP := newTestHandler()

	resp := h.Handle(context.Background(), "workflow_job", sign(raw), raw)

	if resp.Status != 200 {
		t.Errorf("status = %d, want 200 for non-self-hosted lifecycle event", resp.Status)
	}
	if scaleUp.calls != 0 {
		t.Errorf("scaleUp.calls = %d, want 0", scaleUp.calls)
	}
	if lifeP.calls != 0 {
		t.Errorf("lifecycle.calls = %d, want 0 (non-self-hosted should be dropped)", lifeP.calls)
	}
}

func TestHandler_Handle_UnknownActionNoPublish(t *testing.T) {
	raw := []byte(`{
		"action": "waiting",
		"workflow_job": {
			"id": 42,
			"run_id": 100,
			"name": "build",
			"labels": ["self-hosted", "linux"],
			"status": "waiting"
		},
		"repository": {"id": 1, "full_name": "org/repo", "private": true},
		"installation": {"id": 1}
	}`)
	h, scaleUp, lifeP := newTestHandler()

	resp := h.Handle(context.Background(), "workflow_job", sign(raw), raw)

	if resp.Status != 200 {
		t.Errorf("status = %d, want 200", resp.Status)
	}
	if scaleUp.calls != 0 || lifeP.calls != 0 {
		t.Errorf("expected no publishes; scaleUp=%d lifecycle=%d", scaleUp.calls, lifeP.calls)
	}
}

func TestHandler_Handle_NonWorkflowJobEventIgnored(t *testing.T) {
	body := []byte(`{}`)
	h, scaleUp, lifeP := newTestHandler()

	resp := h.Handle(context.Background(), "ping", sign(body), body)

	if resp.Status != 200 {
		t.Errorf("status = %d, want 200 OK for non-workflow_job event", resp.Status)
	}
	if scaleUp.calls != 0 || lifeP.calls != 0 {
		t.Errorf("expected no publishes; scaleUp=%d lifecycle=%d", scaleUp.calls, lifeP.calls)
	}
}

func TestHandler_Handle_BadSignature(t *testing.T) {
	body := loadFixture(t, "workflow_job.queued.json")
	h, scaleUp, lifeP := newTestHandler()

	resp := h.Handle(context.Background(), "workflow_job", "sha256=deadbeef", body)

	if resp.Status != 401 {
		t.Errorf("status = %d, want 401", resp.Status)
	}
	if scaleUp.calls != 0 || lifeP.calls != 0 {
		t.Error("no publish should occur on invalid signature")
	}
}

func TestHandler_Handle_BadJSON(t *testing.T) {
	body := []byte(`{not json}`)
	h, scaleUp, lifeP := newTestHandler()

	resp := h.Handle(context.Background(), "workflow_job", sign(body), body)

	if resp.Status != 400 {
		t.Errorf("status = %d, want 400", resp.Status)
	}
	if scaleUp.calls != 0 || lifeP.calls != 0 {
		t.Error("no publish should occur on bad JSON")
	}
}

func TestHandler_Handle_QueuedNonSelfHostedNoPublish(t *testing.T) {
	// queued event but with no self-hosted label -> Parse marks ShouldScale=false.
	raw := []byte(`{
		"action": "queued",
		"workflow_job": {
			"id": 1,
			"run_id": 2,
			"name": "build",
			"labels": ["ubuntu-latest"],
			"status": "queued"
		},
		"repository": {"id": 1, "full_name": "org/repo", "private": true},
		"installation": {"id": 1}
	}`)
	h, scaleUp, lifeP := newTestHandler()

	resp := h.Handle(context.Background(), "workflow_job", sign(raw), raw)

	if resp.Status != 200 {
		t.Errorf("status = %d, want 200", resp.Status)
	}
	if scaleUp.calls != 0 || lifeP.calls != 0 {
		t.Errorf("expected no publishes; scaleUp=%d lifecycle=%d", scaleUp.calls, lifeP.calls)
	}
}

func TestHandler_Handle_LifecyclePlumbsRunnerID(t *testing.T) {
	// Synthetic in_progress workflow_job event with runner_id populated.
	// The dispatcher must publish a lifecycle.Message whose RunnerID
	// matches the event payload — this guards against accidental drops
	// of the field during refactors (see issue #52).
	body := []byte(`{
		"action": "in_progress",
		"workflow_job": {
			"id": 1234,
			"run_id": 5678,
			"labels": ["self-hosted","large"],
			"runner_name": "jit-abc",
			"runner_id": 99887766,
			"status": "in_progress"
		},
		"repository": {"id": 1, "full_name": "owner/repo", "private": false},
		"installation": {"id": 42}
	}`)

	h, _, lifeP := newTestHandler()
	resp := h.Handle(context.Background(), "workflow_job", sign(body), body)
	if resp.Status != 202 {
		t.Fatalf("status = %d, want 202; body=%q", resp.Status, resp.Body)
	}
	if lifeP.calls != 1 {
		t.Fatalf("lifecycle.calls = %d, want 1", lifeP.calls)
	}

	var got queue.LifecycleMessage
	if err := json.Unmarshal(lifeP.rawBody, &got); err != nil {
		t.Fatalf("unmarshal published lifecycle msg: %v", err)
	}
	if got.RunnerID != 99887766 {
		t.Errorf("RunnerID = %d, want 99887766", got.RunnerID)
	}
	if got.JobID != 1234 {
		t.Errorf("JobID = %d, want 1234", got.JobID)
	}
	if got.Repo != "owner/repo" {
		t.Errorf("Repo = %q, want owner/repo", got.Repo)
	}
}
