package dynamo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

type mockDDB struct {
	putInput    *dynamodb.PutItemInput
	getInput    *dynamodb.GetItemInput
	scanInput   *dynamodb.ScanInput
	updateInput *dynamodb.UpdateItemInput
	deleteInput *dynamodb.DeleteItemInput
	getOut      *dynamodb.GetItemOutput
	scanOut     *dynamodb.ScanOutput
	err         error
}

func (m *mockDDB) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	m.putInput = in
	return &dynamodb.PutItemOutput{}, m.err
}

func (m *mockDDB) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	m.getInput = in
	if m.getOut != nil {
		return m.getOut, m.err
	}
	return &dynamodb.GetItemOutput{}, m.err
}

func (m *mockDDB) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	m.updateInput = in
	return &dynamodb.UpdateItemOutput{}, m.err
}

func (m *mockDDB) Scan(_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	m.scanInput = in
	if m.scanOut != nil {
		return m.scanOut, m.err
	}
	return &dynamodb.ScanOutput{}, m.err
}

func (m *mockDDB) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	m.deleteInput = in
	return &dynamodb.DeleteItemOutput{}, m.err
}

func TestStore_Put_PreservesAttributeNames(t *testing.T) {
	mock := &mockDDB{}
	s := NewStore(mock, "runners")

	now := time.Now()
	r := state.Runner{
		ID:         "org/repo#123",
		InstanceID: "i-abc",
		Repository: "org/repo",
		Labels:     []string{"self-hosted", "linux"},
		Status:     "pending",
		LaunchedAt: now,
		UpdatedAt:  now,
		TTL:        now.Add(24 * time.Hour),
	}
	if err := s.Put(context.Background(), r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if mock.putInput == nil {
		t.Fatal("PutItem was not called")
	}
	if aws.ToString(mock.putInput.TableName) != "runners" {
		t.Errorf("table = %q, want runners", aws.ToString(mock.putInput.TableName))
	}
	for _, key := range []string{"runner_id", "instance_id", "repository", "status", "created_at", "updated_at", "ttl"} {
		if _, ok := mock.putInput.Item[key]; !ok {
			t.Errorf("missing attribute %q in PutItem", key)
		}
	}
}

func TestStore_Get_RoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second).UTC()
	rec := dbRecord{
		RunnerID:   "org/repo#123",
		InstanceID: "i-abc",
		Repository: "org/repo",
		Labels:     []string{"self-hosted"},
		Status:     "pending",
		CreatedAt:  now.Unix(),
		UpdatedAt:  now.Unix(),
		TTL:        now.Add(24 * time.Hour).Unix(),
	}
	item, err := attributevalue.MarshalMap(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	mock := &mockDDB{getOut: &dynamodb.GetItemOutput{Item: item}}
	s := NewStore(mock, "runners")

	got, err := s.Get(context.Background(), "org/repo#123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "org/repo#123" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.InstanceID != "i-abc" {
		t.Errorf("InstanceID = %q", got.InstanceID)
	}
	if !got.LaunchedAt.Equal(now) {
		t.Errorf("LaunchedAt = %v, want %v", got.LaunchedAt, now)
	}
}

func TestStore_Get_NotFoundReturnsErrNotFound(t *testing.T) {
	mock := &mockDDB{getOut: &dynamodb.GetItemOutput{}}
	s := NewStore(mock, "runners")
	got, err := s.Get(context.Background(), "missing")
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected state.ErrNotFound, got %v", err)
	}
	if got.ID != "" {
		t.Errorf("expected zero Runner, got ID=%q", got.ID)
	}
}

func TestStore_Update_PartialFields(t *testing.T) {
	cases := []struct {
		name        string
		update      state.RunnerUpdate
		wantNoOp    bool
		wantAttrSet []string
	}{
		{
			name:     "no fields = no-op",
			update:   state.RunnerUpdate{},
			wantNoOp: true,
		},
		{
			name:        "only status",
			update:      state.RunnerUpdate{Status: stringPtr(state.StatusRunning)},
			wantAttrSet: []string{"status", "updated_at"},
		},
		{
			name: "all four lifecycle fields",
			update: state.RunnerUpdate{
				Status:            stringPtr(state.StatusFailed),
				GitHubRunnerID:    int64Ptr(99),
				ReEnqueueAttempts: intPtr(2),
				LastAttemptAt:     timePtr(time.Unix(1714564800, 0)),
			},
			wantAttrSet: []string{"status", "gh_runner_id", "re_enqueue_attempts", "last_attempt_at", "updated_at"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockDDB{}
			s := NewStore(mock, "t")
			if err := s.Update(context.Background(), "r1", tc.update); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if tc.wantNoOp {
				if mock.updateInput != nil {
					t.Fatalf("expected no UpdateItem call, got %+v", mock.updateInput)
				}
				return
			}
			if mock.updateInput == nil {
				t.Fatal("expected UpdateItem call")
			}
			seen := map[string]bool{}
			for _, name := range mock.updateInput.ExpressionAttributeNames {
				seen[name] = true
			}
			for _, want := range tc.wantAttrSet {
				if !seen[want] {
					t.Errorf("expected UpdateExpression to set %q; saw names=%v", want, seen)
				}
			}
		})
	}
}

func TestStore_List_StatusFilter(t *testing.T) {
	rec1, _ := attributevalue.MarshalMap(dbRecord{
		RunnerID: "a", Status: "pending", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(), TTL: time.Now().Add(time.Hour).Unix(),
	})
	mock := &mockDDB{scanOut: &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{rec1}}}
	s := NewStore(mock, "runners")
	got, err := s.List(context.Background(), state.Filter{StatusEq: "pending"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if mock.scanInput.FilterExpression == nil {
		t.Error("expected FilterExpression to be set")
	}
}

func TestStore_Delete(t *testing.T) {
	mock := &mockDDB{}
	s := NewStore(mock, "runners")
	if err := s.Delete(context.Background(), "org/repo#1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if mock.deleteInput == nil {
		t.Fatal("DeleteItem was not called")
	}
}

func stringPtr(s string) *string     { return &s }
func int64Ptr(i int64) *int64        { return &i }
func intPtr(i int) *int              { return &i }
func timePtr(t time.Time) *time.Time { return &t }
