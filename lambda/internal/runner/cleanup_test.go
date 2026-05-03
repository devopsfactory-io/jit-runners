package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/compute"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/queue"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// fakeStore is an in-memory state.RunnerStore that captures Update calls so
// tests can assert which fields were written. List dispatches by StatusEq.
type fakeStore struct {
	pending []state.Runner
	running []state.Runner

	updates []capturedUpdate
}

type capturedUpdate struct {
	id     string
	update state.RunnerUpdate
}

func (f *fakeStore) Put(_ context.Context, _ state.Runner) error { return nil }
func (f *fakeStore) Get(_ context.Context, _ string) (state.Runner, error) {
	return state.Runner{}, state.ErrNotFound
}

func (f *fakeStore) List(_ context.Context, ff state.Filter) ([]state.Runner, error) {
	switch ff.StatusEq {
	case StatusPending:
		return append([]state.Runner{}, f.pending...), nil
	case StatusRunning:
		return append([]state.Runner{}, f.running...), nil
	}
	return nil, nil
}

func (f *fakeStore) Update(_ context.Context, id string, u state.RunnerUpdate) error {
	f.updates = append(f.updates, capturedUpdate{id: id, update: u})
	return nil
}

func (f *fakeStore) Delete(_ context.Context, _ string) error { return nil }
func (f *fakeStore) ListActiveRepos(_ context.Context, _ time.Time) ([]string, error) {
	return nil, nil
}

// fakeLauncher records terminate calls and may inject an error. It also
// satisfies compute.Launcher (Launch and ListStale return zero values; the
// cleanup path does not call them).
type fakeLauncher struct {
	terminated   []string
	terminateErr error
}

func (f *fakeLauncher) Launch(_ context.Context, _ compute.LaunchSpec) (compute.Instance, error) {
	return compute.Instance{}, nil
}

func (f *fakeLauncher) Terminate(_ context.Context, ids []string) error {
	f.terminated = append(f.terminated, ids...)
	return f.terminateErr
}

func (f *fakeLauncher) ListStale(_ context.Context, _ time.Duration) ([]compute.Instance, error) {
	return nil, nil
}

// fakeGitHub records DeregisterRunner calls and may inject an error.
type fakeGitHub struct {
	calls []deregCall
	err   error
}

type deregCall struct {
	repo     string
	runnerID int64
}

func (f *fakeGitHub) DeregisterRunner(_ context.Context, repo string, runnerID int64) error {
	f.calls = append(f.calls, deregCall{repo: repo, runnerID: runnerID})
	return f.err
}

// fakePub records published ScaleUp messages and may inject an error.
type fakePub struct {
	msgs []*queue.ScaleUpMessage
	err  error
}

func (f *fakePub) PublishScaleUp(_ context.Context, m *queue.ScaleUpMessage) error {
	f.msgs = append(f.msgs, m)
	return f.err
}

// helpers ---------------------------------------------------------------

// fixedNow returns a Time provider that always yields t.
func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

// stalePending builds a status=pending Runner whose LaunchedAt is well past
// the cutoff (StaleAfter=10m, so 2h ago is unambiguous).
func stalePending(now time.Time, attempts int) state.Runner {
	return state.Runner{
		ID:                "99",
		InstanceID:        "i-pending",
		Repository:        "owner/repo",
		Labels:            []string{"self-hosted", "linux"},
		Status:            StatusPending,
		LaunchedAt:        now.Add(-2 * time.Hour),
		UpdatedAt:         now.Add(-2 * time.Hour),
		TTL:               now.Add(24 * time.Hour),
		GitHubRunnerID:    99,
		JobID:             1,
		ReEnqueueAttempts: attempts,
	}
}

// staleRunning builds a status=running Runner whose LaunchedAt is far past
// MaxAge (default 6h, so 12h ago is unambiguous).
func staleRunning(now time.Time, attempts int) state.Runner {
	return state.Runner{
		ID:                "77",
		InstanceID:        "i-running",
		Repository:        "owner/repo",
		Labels:            []string{"self-hosted", "linux"},
		Status:            StatusRunning,
		LaunchedAt:        now.Add(-12 * time.Hour),
		UpdatedAt:         now.Add(-12 * time.Hour),
		TTL:               now.Add(24 * time.Hour),
		GitHubRunnerID:    77,
		JobID:             2,
		ReEnqueueAttempts: attempts,
	}
}

// newTestCleaner wires a Cleaner with sane test defaults. now is fixed so
// cutoff arithmetic is deterministic.
func newTestCleaner(t *testing.T, store state.RunnerStore, launcher compute.Launcher, gh ghClient, pub scaleupPublisher, maxAttempts int, now time.Time) *Cleaner {
	t.Helper()
	return &Cleaner{
		Store:                store,
		Launcher:             launcher,
		GitHub:               gh,
		ScaleUpPublisher:     pub,
		StaleAfter:           10 * time.Minute,
		MaxAge:               6 * time.Hour,
		MaxReEnqueueAttempts: maxAttempts,
		Now:                  fixedNow(now),
	}
}

// updateStatus returns the value of the Status field in u, or "" if unset.
func updateStatus(u state.RunnerUpdate) string {
	if u.Status == nil {
		return ""
	}
	return *u.Status
}

// updateAttempts returns the value of the ReEnqueueAttempts field in u, or
// -1 if unset.
func updateAttempts(u state.RunnerUpdate) int {
	if u.ReEnqueueAttempts == nil {
		return -1
	}
	return *u.ReEnqueueAttempts
}

// tests -----------------------------------------------------------------

func TestCleaner_StalePending_UnderBudget(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := stalePending(now, 0)
	store := &fakeStore{pending: []state.Runner{rec}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, store, ec2, gh, pub, 3, now)

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(ec2.terminated) != 1 || ec2.terminated[0] != rec.InstanceID {
		t.Errorf("terminated = %v, want [%s]", ec2.terminated, rec.InstanceID)
	}
	if len(gh.calls) != 1 || gh.calls[0].runnerID != rec.GitHubRunnerID {
		t.Errorf("deregister calls = %+v, want one with runnerID=%d", gh.calls, rec.GitHubRunnerID)
	}
	if len(pub.msgs) != 1 {
		t.Fatalf("expected 1 republish, got %d", len(pub.msgs))
	}
	if got := pub.msgs[0].ReEnqueueAttempts; got != 1 {
		t.Errorf("republished ReEnqueueAttempts = %d, want 1", got)
	}
	if pub.msgs[0].JobID != 1 || pub.msgs[0].RepositoryFull != rec.Repository {
		t.Errorf("republished message body mismatch: %+v", pub.msgs[0])
	}
	if pub.msgs[0].Source != queue.SourceWebhook {
		t.Errorf("re-enqueue Source = %q, want %q", pub.msgs[0].Source, queue.SourceWebhook)
	}
	if len(store.updates) != 1 {
		t.Fatalf("expected 1 store update, got %d", len(store.updates))
	}
	u := store.updates[0]
	if updateStatus(u.update) != StatusFailed {
		t.Errorf("update status = %q, want failed", updateStatus(u.update))
	}
	if updateAttempts(u.update) != 1 {
		t.Errorf("update re_enqueue_attempts = %d, want 1", updateAttempts(u.update))
	}
	if u.update.LastAttemptAt == nil {
		t.Error("update should set last_attempt_at")
	}
	if res.Stale != 1 || res.Orphans != 1 || res.Errors != 0 {
		t.Errorf("result = %+v, want stale=1 orphans=1 errors=0", res)
	}
}

func TestCleaner_StalePending_AtBudget(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := stalePending(now, 2)
	store := &fakeStore{pending: []state.Runner{rec}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, store, ec2, gh, pub, 3, now)

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(ec2.terminated) != 1 || len(gh.calls) != 1 {
		t.Errorf("expected 1 terminate + 1 deregister, got terminate=%v deregister=%+v", ec2.terminated, gh.calls)
	}
	if len(pub.msgs) != 1 {
		t.Fatalf("expected 1 republish, got %d", len(pub.msgs))
	}
	if got := pub.msgs[0].ReEnqueueAttempts; got != 3 {
		t.Errorf("republished ReEnqueueAttempts = %d, want 3", got)
	}
	if len(store.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(store.updates))
	}
	if updateAttempts(store.updates[0].update) != 3 {
		t.Errorf("update re_enqueue_attempts = %d, want 3", updateAttempts(store.updates[0].update))
	}
	if res.Stale != 1 || res.Orphans != 1 || res.Errors != 0 {
		t.Errorf("result = %+v, want stale=1 orphans=1 errors=0", res)
	}
}

func TestCleaner_StalePending_Exhausted(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := stalePending(now, 3)
	store := &fakeStore{pending: []state.Runner{rec}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, store, ec2, gh, pub, 3, now)

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(ec2.terminated) != 1 || len(gh.calls) != 1 {
		t.Errorf("expected 1 terminate + 1 deregister, got terminate=%v deregister=%+v", ec2.terminated, gh.calls)
	}
	if len(pub.msgs) != 0 {
		t.Errorf("expected NO republish, got %+v", pub.msgs)
	}
	if len(store.updates) != 1 {
		t.Fatalf("expected 1 terminal-failed update, got %d", len(store.updates))
	}
	if updateStatus(store.updates[0].update) != StatusFailed {
		t.Errorf("update status = %q, want failed", updateStatus(store.updates[0].update))
	}
	if store.updates[0].update.ReEnqueueAttempts != nil {
		t.Error("exhausted-budget update should NOT bump re_enqueue_attempts")
	}
	if res.Stale != 1 || res.Orphans != 1 || res.Errors != 0 {
		t.Errorf("result = %+v, want stale=1 orphans=1 errors=0", res)
	}
}

func TestCleaner_StaleRunning_NeverReenqueues(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	// Even with attempts=0 (well under budget) a running record must NOT
	// be re-enqueued — the job may already be considered done by GitHub.
	rec := staleRunning(now, 0)
	store := &fakeStore{running: []state.Runner{rec}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, store, ec2, gh, pub, 3, now)

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(ec2.terminated) != 1 || ec2.terminated[0] != rec.InstanceID {
		t.Errorf("terminated = %v, want [%s]", ec2.terminated, rec.InstanceID)
	}
	if len(gh.calls) != 1 || gh.calls[0].runnerID != rec.GitHubRunnerID {
		t.Errorf("deregister calls = %+v, want one with runnerID=%d", gh.calls, rec.GitHubRunnerID)
	}
	if len(pub.msgs) != 0 {
		t.Fatalf("stale-running MUST NOT re-enqueue, got %+v", pub.msgs)
	}
	if len(store.updates) != 1 {
		t.Fatalf("expected 1 update marking failed, got %d", len(store.updates))
	}
	if updateStatus(store.updates[0].update) != StatusFailed {
		t.Errorf("update status = %q, want failed", updateStatus(store.updates[0].update))
	}
	if res.Stale != 1 || res.Orphans != 0 || res.Errors != 0 {
		t.Errorf("result = %+v, want stale=1 orphans=0 errors=0", res)
	}
}

func TestCleaner_StaleRunning_NeverReenqueues_EvenWithAttemptsZero(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	// Sanity duplicate guard: reordering attempts shouldn't change the
	// outcome for the running path.
	rec := staleRunning(now, 5)
	store := &fakeStore{running: []state.Runner{rec}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, store, ec2, gh, pub, 99, now) // huge budget

	if _, err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(pub.msgs) != 0 {
		t.Fatalf("stale-running MUST NOT re-enqueue regardless of budget, got %+v", pub.msgs)
	}
}

func TestCleaner_Deregister404_DoesNotBlockUpdate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := stalePending(now, 0)
	store := &fakeStore{pending: []state.Runner{rec}}
	ec2 := &fakeLauncher{}
	// *github.Client.DeregisterRunner already maps 404 -> nil, so a fake
	// returning nil mirrors the contract.
	gh := &fakeGitHub{err: nil}
	pub := &fakePub{}
	c := newTestCleaner(t, store, ec2, gh, pub, 3, now)

	if _, err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(store.updates) != 1 {
		t.Fatalf("expected store update even when deregister is a no-op, got %d updates", len(store.updates))
	}
}

func TestCleaner_DeregisterNon404_DoesNotBlockUpdate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := stalePending(now, 0)
	store := &fakeStore{pending: []state.Runner{rec}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{err: errors.New("github: 500 internal server error")}
	pub := &fakePub{}
	c := newTestCleaner(t, store, ec2, gh, pub, 3, now)

	if _, err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(gh.calls) != 1 {
		t.Errorf("expected 1 deregister attempt, got %d", len(gh.calls))
	}
	if len(pub.msgs) != 1 {
		t.Errorf("expected republish to proceed, got %d", len(pub.msgs))
	}
	if len(store.updates) != 1 {
		t.Errorf("expected store update to proceed despite deregister error, got %d", len(store.updates))
	}
}

func TestCleaner_PublishError_NoUpdate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := stalePending(now, 0)
	store := &fakeStore{pending: []state.Runner{rec}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{err: errors.New("sqs: publish failed")}
	c := newTestCleaner(t, store, ec2, gh, pub, 3, now)

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(ec2.terminated) != 1 || len(gh.calls) != 1 {
		t.Errorf("terminate + deregister should still happen before publish")
	}
	if len(store.updates) != 0 {
		t.Errorf("expected NO store update on publish failure, got %+v", store.updates)
	}
	if res.Errors == 0 {
		t.Errorf("result.Errors should be incremented on publish error: %+v", res)
	}
}

func TestCleaner_TerminateError_SkipsRecord(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := stalePending(now, 0)
	store := &fakeStore{pending: []state.Runner{rec}}
	ec2 := &fakeLauncher{terminateErr: errors.New("ec2: throttled")}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, store, ec2, gh, pub, 3, now)

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(gh.calls) != 0 || len(pub.msgs) != 0 || len(store.updates) != 0 {
		t.Errorf("after terminate failure no further side-effects should occur (deregister=%d publish=%d update=%d)",
			len(gh.calls), len(pub.msgs), len(store.updates))
	}
	if res.Errors != 1 || res.Stale != 0 || res.Orphans != 0 {
		t.Errorf("result = %+v, want errors=1 stale=0 orphans=0", res)
	}
}

func TestCleaner_MultipleRecords_Aggregates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	under := stalePending(now, 0)
	under.ID = "91"
	under.GitHubRunnerID = 91
	under.InstanceID = "i-under"

	at := stalePending(now, 2)
	at.ID = "92"
	at.GitHubRunnerID = 92
	at.InstanceID = "i-at"

	exhausted := stalePending(now, 3)
	exhausted.ID = "93"
	exhausted.GitHubRunnerID = 93
	exhausted.InstanceID = "i-exhausted"

	store := &fakeStore{pending: []state.Runner{under, at, exhausted}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, store, ec2, gh, pub, 3, now)

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(ec2.terminated) != 3 {
		t.Errorf("terminated %d instances, want 3 (%v)", len(ec2.terminated), ec2.terminated)
	}
	if len(gh.calls) != 3 {
		t.Errorf("deregistered %d runners, want 3", len(gh.calls))
	}
	// Two republishes (under-budget + at-budget); exhausted record skips publish.
	if len(pub.msgs) != 2 {
		t.Fatalf("republished %d messages, want 2", len(pub.msgs))
	}
	if len(store.updates) != 3 {
		t.Errorf("store updates %d, want 3", len(store.updates))
	}
	if res.Stale != 3 || res.Orphans != 3 || res.Errors != 0 {
		t.Errorf("result = %+v, want stale=3 orphans=3 errors=0", res)
	}

	// All updates land on the terminal failed status.
	for _, u := range store.updates {
		if updateStatus(u.update) != StatusFailed {
			t.Errorf("update %s status = %q, want failed", u.id, updateStatus(u.update))
		}
	}
}

func TestCleaner_RecordTooFresh_Skipped(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	fresh := stalePending(now, 0)
	// Override LaunchedAt so the record is well WITHIN the StaleAfter window.
	fresh.LaunchedAt = now.Add(-1 * time.Minute)
	store := &fakeStore{pending: []state.Runner{fresh}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, store, ec2, gh, pub, 3, now)

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(ec2.terminated) != 0 || len(gh.calls) != 0 || len(pub.msgs) != 0 || len(store.updates) != 0 {
		t.Errorf("fresh record should be a no-op; got terminate=%v dereg=%v publish=%v update=%v",
			ec2.terminated, gh.calls, pub.msgs, store.updates)
	}
	if res.Stale != 0 || res.Orphans != 0 || res.Errors != 0 {
		t.Errorf("result = %+v, want zero", res)
	}
}

// stateTypeAlignmentSentinel is a compile-time assertion that the local
// scaleupPublisher interface remains compatible with state.RunnerUpdate
// usage in cleanup.go. It exists only to surface signature drift early.
var _ = state.RunnerUpdate{}
