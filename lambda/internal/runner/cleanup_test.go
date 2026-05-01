package runner

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/sqs"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// scanFakeDDB is a richer fake that responds to Scan queries based on the
// ":status" filter value so a single fake can serve both pending and
// running list calls. It also captures every UpdateItem so tests can
// assert which fields were written.
type scanFakeDDB struct {
	pending []*Record
	running []*Record

	updates []capturedUpdate
}

type capturedUpdate struct {
	id     string
	expr   string
	values map[string]types.AttributeValue
}

func (f *scanFakeDDB) PutItem(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}

func (f *scanFakeDDB) GetItem(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{}, nil
}

func (f *scanFakeDDB) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	id := ""
	if v, ok := in.Key["runner_id"].(*types.AttributeValueMemberS); ok {
		id = v.Value
	}
	expr := ""
	if in.UpdateExpression != nil {
		expr = *in.UpdateExpression
	}
	f.updates = append(f.updates, capturedUpdate{
		id:     id,
		expr:   expr,
		values: in.ExpressionAttributeValues,
	})
	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *scanFakeDDB) Scan(_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	statusVal := ""
	if v, ok := in.ExpressionAttributeValues[":status"].(*types.AttributeValueMemberS); ok {
		statusVal = v.Value
	}
	var src []*Record
	switch statusVal {
	case StatusPending:
		src = f.pending
	case StatusRunning:
		src = f.running
	}
	out := &dynamodb.ScanOutput{}
	for _, r := range src {
		item, err := attributevalue.MarshalMap(r)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// fakeLauncher records terminate calls and may inject an error.
type fakeLauncher struct {
	terminated   []string
	terminateErr error
}

func (f *fakeLauncher) Terminate(_ context.Context, ids ...string) error {
	f.terminated = append(f.terminated, ids...)
	return f.terminateErr
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
	msgs []*sqs.ScaleUpMessage
	err  error
}

func (f *fakePub) Publish(_ context.Context, m *sqs.ScaleUpMessage) error {
	f.msgs = append(f.msgs, m)
	return f.err
}

// helpers ---------------------------------------------------------------

// fixedNow returns a Time provider that always yields t.
func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

// stalePendingRecord builds a status=pending record whose CreatedAt is
// staleAfter*2 ago so it is well past the cutoff.
func stalePendingRecord(now time.Time, attempts int) *Record {
	return &Record{
		RunnerID:          "owner/repo#1",
		InstanceID:        "i-pending",
		JobID:             1,
		RunID:             10,
		Repository:        "owner/repo",
		Labels:            []string{"self-hosted", "linux"},
		Status:            StatusPending,
		CreatedAt:         now.Add(-2 * time.Hour).Unix(),
		UpdatedAt:         now.Add(-2 * time.Hour).Unix(),
		TTL:               now.Add(24 * time.Hour).Unix(),
		GitHubRunnerID:    99,
		ReEnqueueAttempts: attempts,
	}
}

// staleRunningRecord builds a status=running record whose CreatedAt is far
// past MaxAge.
func staleRunningRecord(now time.Time, attempts int) *Record {
	return &Record{
		RunnerID:          "owner/repo#2",
		InstanceID:        "i-running",
		JobID:             2,
		RunID:             20,
		Repository:        "owner/repo",
		Labels:            []string{"self-hosted", "linux"},
		Status:            StatusRunning,
		CreatedAt:         now.Add(-12 * time.Hour).Unix(),
		UpdatedAt:         now.Add(-12 * time.Hour).Unix(),
		TTL:               now.Add(24 * time.Hour).Unix(),
		GitHubRunnerID:    77,
		ReEnqueueAttempts: attempts,
	}
}

// updateContains returns true if the captured update's expression
// referenced the named attribute (as set by Store.Update).
func updateContains(u capturedUpdate, attrName string) bool {
	// Store.Update emits ":s" -> status, ":i" -> instance_id, ":g" -> gh_runner_id,
	// ":r" -> re_enqueue_attempts, ":l" -> last_attempt_at, ":u" -> updated_at.
	want := ""
	switch attrName {
	case "status":
		want = ":s"
	case "instance_id":
		want = ":i"
	case "gh_runner_id":
		want = ":g"
	case "re_enqueue_attempts":
		want = ":r"
	case "last_attempt_at":
		want = ":l"
	case "updated_at":
		want = ":u"
	default:
		return false
	}
	_, ok := u.values[want]
	return ok
}

// updateStringValue returns the string of the named attribute in the
// captured update, or "" if absent.
func updateStringValue(u capturedUpdate, attrName string) string {
	want := ""
	switch attrName {
	case "status":
		want = ":s"
	case "instance_id":
		want = ":i"
	}
	v, ok := u.values[want].(*types.AttributeValueMemberS)
	if !ok {
		return ""
	}
	return v.Value
}

// updateIntValue returns the integer of the named numeric attribute in
// the captured update, or -1 if absent.
func updateIntValue(u capturedUpdate, attrName string) int64 {
	want := ""
	switch attrName {
	case "gh_runner_id":
		want = ":g"
	case "re_enqueue_attempts":
		want = ":r"
	case "last_attempt_at":
		want = ":l"
	}
	v, ok := u.values[want].(*types.AttributeValueMemberN)
	if !ok {
		return -1
	}
	n, err := strconv.ParseInt(v.Value, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// builds wires a Cleaner with sane test defaults backed by the supplied
// fakes. now is fixed so cutoff arithmetic is deterministic.
func newTestCleaner(t *testing.T, ddb DynamoDBAPI, ec2 EC2Terminator, gh ghClient, pub scaleupPublisher, maxAttempts int, now time.Time) *Cleaner {
	t.Helper()
	store := NewStore(ddb, "t")
	return &Cleaner{
		Store:                store,
		Launcher:             ec2,
		GitHub:               gh,
		ScaleUpPublisher:     pub,
		StaleAfter:           10 * time.Minute,
		MaxAge:               6 * time.Hour,
		MaxReEnqueueAttempts: maxAttempts,
		Now:                  fixedNow(now),
	}
}

// tests -----------------------------------------------------------------

func TestCleaner_StalePending_UnderBudget(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := stalePendingRecord(now, 0)
	ddb := &scanFakeDDB{pending: []*Record{rec}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, ddb, ec2, gh, pub, 3, now)

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := []string{rec.InstanceID}; len(ec2.terminated) != 1 || ec2.terminated[0] != got[0] {
		t.Errorf("terminated = %v, want %v", ec2.terminated, got)
	}
	if len(gh.calls) != 1 || gh.calls[0].runnerID != rec.GitHubRunnerID {
		t.Errorf("deregister calls = %+v, want one call with runnerID=%d", gh.calls, rec.GitHubRunnerID)
	}
	if len(pub.msgs) != 1 {
		t.Fatalf("expected 1 republish, got %d", len(pub.msgs))
	}
	if got := pub.msgs[0].ReEnqueueAttempts; got != 1 {
		t.Errorf("republished ReEnqueueAttempts = %d, want 1", got)
	}
	if pub.msgs[0].JobID != rec.JobID || pub.msgs[0].RepositoryFull != rec.Repository {
		t.Errorf("republished message body mismatch: %+v", pub.msgs[0])
	}
	if len(ddb.updates) != 1 {
		t.Fatalf("expected 1 DDB update, got %d", len(ddb.updates))
	}
	u := ddb.updates[0]
	if got := updateStringValue(u, "status"); got != StatusFailed {
		t.Errorf("update status = %q, want failed", got)
	}
	if got := updateIntValue(u, "re_enqueue_attempts"); got != 1 {
		t.Errorf("update re_enqueue_attempts = %d, want 1", got)
	}
	if !updateContains(u, "last_attempt_at") {
		t.Error("update should set last_attempt_at")
	}
	if res.Stale != 1 || res.Orphans != 1 || res.Errors != 0 {
		t.Errorf("result = %+v, want stale=1 orphans=1 errors=0", res)
	}
}

func TestCleaner_StalePending_AtBudget(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := stalePendingRecord(now, 2)
	ddb := &scanFakeDDB{pending: []*Record{rec}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, ddb, ec2, gh, pub, 3, now)

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(ec2.terminated) != 1 || len(gh.calls) != 1 {
		t.Errorf("expected 1 terminate + 1 deregister, got terminated=%v deregister=%+v", ec2.terminated, gh.calls)
	}
	if len(pub.msgs) != 1 {
		t.Fatalf("expected 1 republish, got %d", len(pub.msgs))
	}
	if got := pub.msgs[0].ReEnqueueAttempts; got != 3 {
		t.Errorf("republished ReEnqueueAttempts = %d, want 3", got)
	}
	if len(ddb.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(ddb.updates))
	}
	if got := updateIntValue(ddb.updates[0], "re_enqueue_attempts"); got != 3 {
		t.Errorf("update re_enqueue_attempts = %d, want 3", got)
	}
	if res.Stale != 1 || res.Orphans != 1 || res.Errors != 0 {
		t.Errorf("result = %+v, want stale=1 orphans=1 errors=0", res)
	}
}

func TestCleaner_StalePending_Exhausted(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := stalePendingRecord(now, 3)
	ddb := &scanFakeDDB{pending: []*Record{rec}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, ddb, ec2, gh, pub, 3, now)

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(ec2.terminated) != 1 || len(gh.calls) != 1 {
		t.Errorf("expected 1 terminate + 1 deregister, got terminated=%v deregister=%+v", ec2.terminated, gh.calls)
	}
	if len(pub.msgs) != 0 {
		t.Errorf("expected NO republish, got %+v", pub.msgs)
	}
	if len(ddb.updates) != 1 {
		t.Fatalf("expected 1 terminal-failed update, got %d", len(ddb.updates))
	}
	if got := updateStringValue(ddb.updates[0], "status"); got != StatusFailed {
		t.Errorf("update status = %q, want failed", got)
	}
	if updateContains(ddb.updates[0], "re_enqueue_attempts") {
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
	rec := staleRunningRecord(now, 0)
	ddb := &scanFakeDDB{running: []*Record{rec}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, ddb, ec2, gh, pub, 3, now)

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(ec2.terminated) != 1 || ec2.terminated[0] != rec.InstanceID {
		t.Errorf("terminated = %v, want %s", ec2.terminated, rec.InstanceID)
	}
	if len(gh.calls) != 1 || gh.calls[0].runnerID != rec.GitHubRunnerID {
		t.Errorf("deregister calls = %+v, want one with runnerID=%d", gh.calls, rec.GitHubRunnerID)
	}
	if len(pub.msgs) != 0 {
		t.Fatalf("stale-running MUST NOT re-enqueue, got %+v", pub.msgs)
	}
	if len(ddb.updates) != 1 {
		t.Fatalf("expected 1 update marking failed, got %d", len(ddb.updates))
	}
	if got := updateStringValue(ddb.updates[0], "status"); got != StatusFailed {
		t.Errorf("update status = %q, want failed", got)
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
	rec := staleRunningRecord(now, 5)
	ddb := &scanFakeDDB{running: []*Record{rec}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, ddb, ec2, gh, pub, 99, now) // huge budget

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
	rec := stalePendingRecord(now, 0)
	ddb := &scanFakeDDB{pending: []*Record{rec}}
	ec2 := &fakeLauncher{}
	// *github.Client.DeregisterRunner already maps 404 -> nil, so a fake
	// returning nil mirrors the contract.
	gh := &fakeGitHub{err: nil}
	pub := &fakePub{}
	c := newTestCleaner(t, ddb, ec2, gh, pub, 3, now)

	if _, err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(ddb.updates) != 1 {
		t.Fatalf("expected DDB update even when deregister is a no-op, got %d updates", len(ddb.updates))
	}
}

func TestCleaner_DeregisterNon404_DoesNotBlockUpdate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := stalePendingRecord(now, 0)
	ddb := &scanFakeDDB{pending: []*Record{rec}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{err: errors.New("github: 500 internal server error")}
	pub := &fakePub{}
	c := newTestCleaner(t, ddb, ec2, gh, pub, 3, now)

	if _, err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(gh.calls) != 1 {
		t.Errorf("expected 1 deregister attempt, got %d", len(gh.calls))
	}
	if len(pub.msgs) != 1 {
		t.Errorf("expected republish to proceed, got %d", len(pub.msgs))
	}
	if len(ddb.updates) != 1 {
		t.Errorf("expected DDB update to proceed despite deregister error, got %d", len(ddb.updates))
	}
}

func TestCleaner_PublishError_NoUpdate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := stalePendingRecord(now, 0)
	ddb := &scanFakeDDB{pending: []*Record{rec}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{err: errors.New("sqs: publish failed")}
	c := newTestCleaner(t, ddb, ec2, gh, pub, 3, now)

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(ec2.terminated) != 1 || len(gh.calls) != 1 {
		t.Errorf("terminate + deregister should still happen before publish")
	}
	if len(ddb.updates) != 0 {
		t.Errorf("expected NO DDB update on publish failure, got %+v", ddb.updates)
	}
	if res.Errors == 0 {
		t.Errorf("result.Errors should be incremented on publish error: %+v", res)
	}
}

func TestCleaner_TerminateError_SkipsRecord(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := stalePendingRecord(now, 0)
	ddb := &scanFakeDDB{pending: []*Record{rec}}
	ec2 := &fakeLauncher{terminateErr: errors.New("ec2: throttled")}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, ddb, ec2, gh, pub, 3, now)

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(gh.calls) != 0 || len(pub.msgs) != 0 || len(ddb.updates) != 0 {
		t.Errorf("after terminate failure no further side-effects should occur (deregister=%d publish=%d update=%d)",
			len(gh.calls), len(pub.msgs), len(ddb.updates))
	}
	if res.Errors != 1 || res.Stale != 0 || res.Orphans != 0 {
		t.Errorf("result = %+v, want errors=1 stale=0 orphans=0", res)
	}
}

func TestCleaner_MultipleRecords_Aggregates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	under := stalePendingRecord(now, 0)
	under.RunnerID = "owner/repo#1"
	under.JobID = 1
	under.GitHubRunnerID = 91
	under.InstanceID = "i-under"

	at := stalePendingRecord(now, 2)
	at.RunnerID = "owner/repo#2"
	at.JobID = 2
	at.GitHubRunnerID = 92
	at.InstanceID = "i-at"

	exhausted := stalePendingRecord(now, 3)
	exhausted.RunnerID = "owner/repo#3"
	exhausted.JobID = 3
	exhausted.GitHubRunnerID = 93
	exhausted.InstanceID = "i-exhausted"

	ddb := &scanFakeDDB{pending: []*Record{under, at, exhausted}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, ddb, ec2, gh, pub, 3, now)

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
	if len(ddb.updates) != 3 {
		t.Errorf("DDB updates %d, want 3", len(ddb.updates))
	}
	if res.Stale != 3 || res.Orphans != 3 || res.Errors != 0 {
		t.Errorf("result = %+v, want stale=3 orphans=3 errors=0", res)
	}

	// All updates land on the terminal failed status.
	for _, u := range ddb.updates {
		if got := updateStringValue(u, "status"); got != StatusFailed {
			t.Errorf("update %s status = %q, want failed", u.id, got)
		}
	}
}

func TestCleaner_RecordTooFresh_Skipped(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	fresh := stalePendingRecord(now, 0)
	// Override CreatedAt so the record is well WITHIN the StaleAfter window.
	fresh.CreatedAt = now.Add(-1 * time.Minute).Unix()
	ddb := &scanFakeDDB{pending: []*Record{fresh}}
	ec2 := &fakeLauncher{}
	gh := &fakeGitHub{}
	pub := &fakePub{}
	c := newTestCleaner(t, ddb, ec2, gh, pub, 3, now)

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(ec2.terminated) != 0 || len(gh.calls) != 0 || len(pub.msgs) != 0 || len(ddb.updates) != 0 {
		t.Errorf("fresh record should be a no-op; got terminate=%v dereg=%v publish=%v update=%v",
			ec2.terminated, gh.calls, pub.msgs, ddb.updates)
	}
	if res.Stale != 0 || res.Orphans != 0 || res.Errors != 0 {
		t.Errorf("result = %+v, want zero", res)
	}
}

// stateTypeAlignmentSentinel is a compile-time assertion that the local
// scaleupPublisher interface remains compatible with state.RunnerUpdate
// usage in cleanup.go. It exists only to surface signature drift early.
var _ = state.RunnerUpdate{}
