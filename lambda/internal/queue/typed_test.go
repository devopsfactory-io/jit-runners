package queue

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type captureSink struct {
	got Msg
	err error
}

func (s *captureSink) Publish(_ context.Context, m Msg) error {
	if s.err != nil {
		return s.err
	}
	s.got = m
	return nil
}

func TestPublishScaleUp_RoundTripsViaJSON(t *testing.T) {
	in := &ScaleUpMessage{
		JobID:          7,
		RepositoryFull: "owner/repo",
		InstallationID: 12345,
		Labels:         []string{"self-hosted", "large"},
		Source:         SourceWebhook,
	}
	sink := &captureSink{}
	if err := PublishScaleUp(context.Background(), sink, in); err != nil {
		t.Fatalf("PublishScaleUp: %v", err)
	}
	got, err := ParseScaleUp(sink.got.Body)
	if err != nil {
		t.Fatalf("ParseScaleUp: %v", err)
	}
	if got.JobID != in.JobID || got.RepositoryFull != in.RepositoryFull || got.Source != in.Source {
		t.Errorf("round-trip mismatch: in=%+v got=%+v", in, got)
	}
	// double-check the raw JSON keys match the struct tags
	var raw map[string]any
	if err := json.Unmarshal(sink.got.Body, &raw); err != nil {
		t.Fatalf("raw unmarshal: %v", err)
	}
	if raw["repository_full"] != "owner/repo" {
		t.Errorf("expected repository_full json key, got %v", raw)
	}
}

func TestPublishScaleUp_PropagatesPublishError(t *testing.T) {
	sink := &captureSink{err: errors.New("boom")}
	err := PublishScaleUp(context.Background(), sink, &ScaleUpMessage{JobID: 1})
	if err == nil {
		t.Fatal("expected publish error to propagate")
	}
}

func TestPublishLifecycle_RoundTripsViaJSON(t *testing.T) {
	in := &LifecycleMessage{
		JobID:    42,
		Repo:     "owner/repo",
		RunnerID: 99,
		Action:   "completed",
	}
	sink := &captureSink{}
	if err := PublishLifecycle(context.Background(), sink, in); err != nil {
		t.Fatalf("PublishLifecycle: %v", err)
	}
	got, err := ParseLifecycle(sink.got.Body)
	if err != nil {
		t.Fatalf("ParseLifecycle: %v", err)
	}
	if *got != *in {
		t.Errorf("round-trip mismatch: in=%+v got=%+v", in, got)
	}
}

func TestParseScaleUp_BadJSONReturnsError(t *testing.T) {
	if _, err := ParseScaleUp([]byte("not json")); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseLifecycle_BadJSONReturnsError(t *testing.T) {
	if _, err := ParseLifecycle([]byte("not json")); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseScaleUp_MissingJobIDReturnsError(t *testing.T) {
	body := []byte(`{"repository_full":"o/r","installation_id":1,"labels":["self-hosted"]}`)
	_, err := ParseScaleUp(body)
	if err == nil {
		t.Fatal("expected missing-job_id error, got nil")
	}
	if !strings.Contains(err.Error(), "job_id") {
		t.Errorf("error message should mention job_id, got %v", err)
	}
}

func TestParseScaleUp_MissingRepositoryFullReturnsError(t *testing.T) {
	body := []byte(`{"job_id":7,"installation_id":1,"labels":["self-hosted"]}`)
	_, err := ParseScaleUp(body)
	if err == nil {
		t.Fatal("expected missing-repository_full error, got nil")
	}
	if !strings.Contains(err.Error(), "repository_full") {
		t.Errorf("error message should mention repository_full, got %v", err)
	}
}

func TestParseScaleUp_MissingInstallationIDReturnsError(t *testing.T) {
	body := []byte(`{"job_id":7,"repository_full":"o/r","labels":["self-hosted"]}`)
	_, err := ParseScaleUp(body)
	if err == nil {
		t.Fatal("expected missing-installation_id error, got nil")
	}
	if !strings.Contains(err.Error(), "installation_id") {
		t.Errorf("error message should mention installation_id, got %v", err)
	}
}
