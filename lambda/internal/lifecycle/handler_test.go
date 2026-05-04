package lifecycle

import (
	"context"
	"errors"
	"log"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// fakeStore implements state.RunnerStore for tests.
type fakeStore struct {
	get       state.Runner
	getErr    error
	updates   []updateCall
	updateErr error
}

type updateCall struct {
	id     string
	fields state.RunnerUpdate
}

func (f *fakeStore) Get(_ context.Context, _ string) (state.Runner, error) {
	return f.get, f.getErr
}

func (f *fakeStore) GetByInstanceID(_ context.Context, _ string) (state.Runner, error) {
	return state.Runner{}, state.ErrNotFound
}

func (f *fakeStore) Put(_ context.Context, _ state.Runner) error { return nil }

func (f *fakeStore) List(_ context.Context, _ state.Filter) ([]state.Runner, error) {
	return nil, nil
}

func (f *fakeStore) Delete(_ context.Context, _ string) error { return nil }

func (f *fakeStore) Update(_ context.Context, id string, u state.RunnerUpdate) error {
	f.updates = append(f.updates, updateCall{id: id, fields: u})
	return f.updateErr
}

func (f *fakeStore) ListActiveRepos(_ context.Context, _ time.Time) ([]string, error) {
	return nil, nil
}

// fakeGitHub records DeregisterRunner calls.
type fakeGitHub struct {
	calls []dereg
	err   error
}

type dereg struct {
	repo     string
	runnerID int64
}

func (f *fakeGitHub) DeregisterRunner(_ context.Context, repo string, runnerID int64) error {
	f.calls = append(f.calls, dereg{repo: repo, runnerID: runnerID})
	return f.err
}

func testLogger() *log.Logger {
	return log.New(os.Stderr, "test ", 0)
}

func TestHandleSQS_TransitionTable(t *testing.T) {
	cases := []struct {
		name           string
		current        string
		action         string
		ghRunnerID     int64
		wantNextStatus string
		wantUpdate     bool
		wantDeregister bool
	}{
		{"pending+in_progress -> running", state.StatusPending, "in_progress", 99, state.StatusRunning, true, false},
		{"running+completed -> completed (deregister)", state.StatusRunning, "completed", 99, state.StatusCompleted, true, true},
		{"pending+completed -> completed (out-of-order, deregister)", state.StatusPending, "completed", 99, state.StatusCompleted, true, true},
		{"completed+completed -> no-op", state.StatusCompleted, "completed", 99, state.StatusCompleted, false, false},
		{"failed+in_progress -> drop (backward)", state.StatusFailed, "in_progress", 99, state.StatusFailed, false, false},
		{"completed+in_progress -> drop (backward)", state.StatusCompleted, "in_progress", 99, state.StatusCompleted, false, false},
		{"running+in_progress -> no-op", state.StatusRunning, "in_progress", 99, state.StatusRunning, false, false},
		{"completed+unknown_action -> drop", state.StatusCompleted, "waiting", 99, state.StatusCompleted, false, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{get: state.Runner{
				ID:             "99",
				Status:         tc.current,
				GitHubRunnerID: tc.ghRunnerID,
				Repository:     "owner/repo",
			}}
			gh := &fakeGitHub{}
			h := &Handler{Store: store, GitHub: gh, Compute: &fakeCompute{}, Logger: testLogger()}

			body := []byte(`{"job_id":1,"repo":"owner/repo","runner_id":99,"action":"` + tc.action + `"}`)
			if err := h.HandleSQS(context.Background(), body); err != nil {
				t.Fatalf("HandleSQS: %v", err)
			}

			gotUpdate := len(store.updates) == 1
			if gotUpdate != tc.wantUpdate {
				t.Errorf("update: got %v want %v (updates=%+v)", gotUpdate, tc.wantUpdate, store.updates)
			}
			if tc.wantUpdate {
				if store.updates[0].id != "99" {
					t.Errorf("update key: got %q want %q", store.updates[0].id, "99")
				}
				if store.updates[0].fields.Status == nil {
					t.Fatal("Status: expected non-nil pointer")
				}
				if got := *store.updates[0].fields.Status; got != tc.wantNextStatus {
					t.Errorf("status: got %q want %q", got, tc.wantNextStatus)
				}
				if store.updates[0].fields.LastAttemptAt == nil {
					t.Error("LastAttemptAt: expected non-nil")
				}
			}

			gotDereg := len(gh.calls) == 1
			if gotDereg != tc.wantDeregister {
				t.Errorf("deregister: got %v want %v (calls=%+v)", gotDereg, tc.wantDeregister, gh.calls)
			}
		})
	}
}

func TestHandleSQS_UnknownRecordDrops(t *testing.T) {
	store := &fakeStore{getErr: state.ErrNotFound}
	gh := &fakeGitHub{}
	h := &Handler{Store: store, GitHub: gh, Compute: &fakeCompute{}, Logger: testLogger()}

	body := []byte(`{"job_id":1,"repo":"owner/repo","runner_id":99,"action":"completed"}`)
	if err := h.HandleSQS(context.Background(), body); err != nil {
		t.Fatalf("HandleSQS: %v", err)
	}
	if len(store.updates) != 0 {
		t.Errorf("expected no updates, got %+v", store.updates)
	}
	if len(gh.calls) != 0 {
		t.Errorf("expected no deregister, got %+v", gh.calls)
	}
}

func TestHandleSQS_DropsWhenRunnerIDZero(t *testing.T) {
	store := &fakeStore{}
	gh := &fakeGitHub{}
	h := &Handler{Store: store, GitHub: gh, Compute: &fakeCompute{}, Logger: testLogger()}

	// runner_id == 0 is the defensive case: GitHub did not include a
	// runner ID in the workflow_job payload. The handler must drop the
	// message without attempting a Get (avoids a meaningless lookup at
	// key "0") and without returning an error (the message is ack'd).
	body := []byte(`{"job_id":1,"repo":"owner/repo","runner_id":0,"action":"completed"}`)
	if err := h.HandleSQS(context.Background(), body); err != nil {
		t.Fatalf("HandleSQS: %v", err)
	}
	if len(store.updates) != 0 {
		t.Errorf("expected no updates, got %+v", store.updates)
	}
	if len(gh.calls) != 0 {
		t.Errorf("expected no deregister, got %+v", gh.calls)
	}
}

func TestHandleSQS_DeregisterErrorDoesNotFail(t *testing.T) {
	store := &fakeStore{get: state.Runner{ID: "99", Status: state.StatusRunning, GitHubRunnerID: 99, Repository: "owner/repo"}}
	gh := &fakeGitHub{err: errors.New("network blip")}
	h := &Handler{Store: store, GitHub: gh, Compute: &fakeCompute{}, Logger: testLogger()}

	body := []byte(`{"job_id":1,"repo":"owner/repo","runner_id":99,"action":"completed"}`)
	if err := h.HandleSQS(context.Background(), body); err != nil {
		t.Fatalf("HandleSQS: %v", err)
	}
	if len(store.updates) != 1 {
		t.Errorf("DDB should still be updated when deregister fails; got %d updates", len(store.updates))
	}
	if len(gh.calls) != 1 {
		t.Errorf("expected one deregister attempt, got %d", len(gh.calls))
	}
}

func TestHandleSQS_BadJSONReturnsError(t *testing.T) {
	store := &fakeStore{}
	gh := &fakeGitHub{}
	h := &Handler{Store: store, GitHub: gh, Compute: &fakeCompute{}, Logger: testLogger()}

	if err := h.HandleSQS(context.Background(), []byte(`{not json`)); err == nil {
		t.Fatal("expected parse error")
	}
}

// fakeCompute records Terminate calls and can be configured to return an error.
type fakeCompute struct {
	terminated [][]string
	err        error
}

func (f *fakeCompute) Terminate(_ context.Context, ids []string) error {
	if f.err != nil {
		return f.err
	}
	f.terminated = append(f.terminated, ids)
	return nil
}

func TestHandle_TerminatesOnCompleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := &fakeStore{
		get: state.Runner{
			ID:             "99",
			InstanceID:     "i-aaa",
			GitHubRunnerID: 99,
			Status:         state.StatusRunning,
		},
	}
	gh := &fakeGitHub{}
	comp := &fakeCompute{}

	h := &Handler{Store: store, GitHub: gh, Compute: comp, Logger: testLogger()}

	body := []byte(`{"job_id":1,"repo":"owner/repo","runner_id":99,"action":"completed"}`)
	if err := h.HandleSQS(ctx, body); err != nil {
		t.Fatalf("HandleSQS: %v", err)
	}

	if len(comp.terminated) != 1 {
		t.Fatalf("expected 1 Terminate call, got %d", len(comp.terminated))
	}
	if got, want := comp.terminated[0], []string{"i-aaa"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Terminate ids = %v, want %v", got, want)
	}
}

func TestHandle_DoesNotTerminateOnInProgress(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := &fakeStore{
		get: state.Runner{
			ID:             "99",
			InstanceID:     "i-aaa",
			GitHubRunnerID: 99,
			Status:         state.StatusPending,
		},
	}
	comp := &fakeCompute{}
	h := &Handler{Store: store, GitHub: &fakeGitHub{}, Compute: comp, Logger: testLogger()}

	body := []byte(`{"job_id":1,"repo":"owner/repo","runner_id":99,"action":"in_progress"}`)
	if err := h.HandleSQS(ctx, body); err != nil {
		t.Fatalf("HandleSQS: %v", err)
	}

	if len(comp.terminated) != 0 {
		t.Errorf("expected 0 Terminate calls on in_progress, got %d", len(comp.terminated))
	}
}

func TestHandle_TerminateErrorIsLogged_NoFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := &fakeStore{
		get: state.Runner{
			ID:             "99",
			InstanceID:     "i-aaa",
			GitHubRunnerID: 99,
			Status:         state.StatusRunning,
		},
	}
	comp := &fakeCompute{err: errors.New("EC2 throttled")}
	h := &Handler{Store: store, GitHub: &fakeGitHub{}, Compute: comp, Logger: testLogger()}

	body := []byte(`{"job_id":1,"repo":"owner/repo","runner_id":99,"action":"completed"}`)
	if err := h.HandleSQS(ctx, body); err != nil {
		t.Fatalf("HandleSQS: %v (best-effort terminate must not fail the message)", err)
	}
}

func TestHandle_NoTerminateWhenInstanceIDEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := &fakeStore{
		get: state.Runner{
			ID:             "99",
			InstanceID:     "",
			GitHubRunnerID: 99,
			Status:         state.StatusRunning,
		},
	}
	comp := &fakeCompute{}
	h := &Handler{Store: store, GitHub: &fakeGitHub{}, Compute: comp, Logger: testLogger()}

	body := []byte(`{"job_id":1,"repo":"owner/repo","runner_id":99,"action":"completed"}`)
	if err := h.HandleSQS(ctx, body); err != nil {
		t.Fatalf("HandleSQS: %v", err)
	}
	if len(comp.terminated) != 0 {
		t.Errorf("expected 0 Terminate calls when InstanceID empty, got %d", len(comp.terminated))
	}
}
