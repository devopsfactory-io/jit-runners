package lifecycle

import (
	"context"
	"errors"
	"log"
	"os"
	"testing"

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

func (f *fakeStore) Put(_ context.Context, _ state.Runner) error { return nil }

func (f *fakeStore) List(_ context.Context, _ state.Filter) ([]state.Runner, error) {
	return nil, nil
}

func (f *fakeStore) Delete(_ context.Context, _ string) error { return nil }

func (f *fakeStore) Update(_ context.Context, id string, u state.RunnerUpdate) error {
	f.updates = append(f.updates, updateCall{id: id, fields: u})
	return f.updateErr
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
		{"completed with zero runnerID -> update only", state.StatusRunning, "completed", 0, state.StatusCompleted, true, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{get: state.Runner{
				ID:             "owner/repo#1",
				Status:         tc.current,
				GitHubRunnerID: tc.ghRunnerID,
			}}
			gh := &fakeGitHub{}
			h := &Handler{Store: store, GitHub: gh, Logger: testLogger()}

			body := []byte(`{"job_id":1,"repo":"owner/repo","runner_id":99,"action":"` + tc.action + `"}`)
			if err := h.HandleSQS(context.Background(), body); err != nil {
				t.Fatalf("HandleSQS: %v", err)
			}

			gotUpdate := len(store.updates) == 1
			if gotUpdate != tc.wantUpdate {
				t.Errorf("update: got %v want %v (updates=%+v)", gotUpdate, tc.wantUpdate, store.updates)
			}
			if tc.wantUpdate {
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
			if tc.wantDeregister {
				if gh.calls[0].runnerID != tc.ghRunnerID {
					t.Errorf("deregister runnerID: got %d want %d", gh.calls[0].runnerID, tc.ghRunnerID)
				}
				if gh.calls[0].repo != "owner/repo" {
					t.Errorf("deregister repo: got %q want %q", gh.calls[0].repo, "owner/repo")
				}
			}
		})
	}
}

func TestHandleSQS_UnknownRecordDrops(t *testing.T) {
	store := &fakeStore{getErr: state.ErrNotFound}
	gh := &fakeGitHub{}
	h := &Handler{Store: store, GitHub: gh, Logger: testLogger()}

	body := []byte(`{"job_id":1,"repo":"owner/repo","action":"completed"}`)
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
	store := &fakeStore{get: state.Runner{Status: state.StatusRunning, GitHubRunnerID: 99}}
	gh := &fakeGitHub{err: errors.New("network blip")}
	h := &Handler{Store: store, GitHub: gh, Logger: testLogger()}

	body := []byte(`{"job_id":1,"repo":"owner/repo","action":"completed"}`)
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
	h := &Handler{Store: store, GitHub: gh, Logger: testLogger()}

	if err := h.HandleSQS(context.Background(), []byte(`{not json`)); err == nil {
		t.Fatal("expected parse error")
	}
}
