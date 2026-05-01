package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// fakeDDBClient is an in-memory stand-in for the DynamoDB API used by Store.
type fakeDDBClient struct {
	putCalls        int
	updateCalls     int
	getCalls        int
	scanCalls       int
	lastUpdateNames map[string]bool
	lastUpdateExpr  string
	// items keyed by runner_id holds the last marshalled Put payload so Get
	// can return it for round-trip assertions.
	items map[string]map[string]types.AttributeValue
}

func (f *fakeDDBClient) PutItem(_ context.Context, input *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.putCalls++
	if f.items == nil {
		f.items = map[string]map[string]types.AttributeValue{}
	}
	if rid, ok := input.Item["runner_id"].(*types.AttributeValueMemberS); ok {
		f.items[rid.Value] = input.Item
	}
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeDDBClient) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.getCalls++
	if f.items == nil {
		return &dynamodb.GetItemOutput{}, nil
	}
	rid, ok := input.Key["runner_id"].(*types.AttributeValueMemberS)
	if !ok {
		return &dynamodb.GetItemOutput{}, nil
	}
	item, ok := f.items[rid.Value]
	if !ok {
		return &dynamodb.GetItemOutput{}, nil
	}
	return &dynamodb.GetItemOutput{Item: item}, nil
}

func (f *fakeDDBClient) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updateCalls++
	if f.lastUpdateNames == nil {
		f.lastUpdateNames = map[string]bool{}
	}
	for _, name := range in.ExpressionAttributeNames {
		f.lastUpdateNames[name] = true
	}
	if in.UpdateExpression != nil {
		f.lastUpdateExpr = *in.UpdateExpression
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *fakeDDBClient) Scan(_ context.Context, _ *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	f.scanCalls++
	return &dynamodb.ScanOutput{}, nil
}

func TestStore_Update_PartialFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		update      state.RunnerUpdate
		wantAttrSet []string
		wantNoOp    bool
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
			name: "all four fields",
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
			t.Parallel()
			fake := &fakeDDBClient{}
			s := NewStore(fake, "t")
			if err := s.Update(context.Background(), "r1", tc.update); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if tc.wantNoOp {
				if fake.updateCalls != 0 {
					t.Fatalf("expected no UpdateItem calls, got %d", fake.updateCalls)
				}
				return
			}
			if fake.updateCalls != 1 {
				t.Fatalf("expected 1 UpdateItem call, got %d", fake.updateCalls)
			}
			for _, attr := range tc.wantAttrSet {
				if !fake.lastUpdateNames[attr] {
					t.Errorf("expected UpdateExpression to set %q; saw names=%v", attr, fake.lastUpdateNames)
				}
			}
		})
	}
}

func TestStore_PutGet_RoundTripsNewFields(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Unix()
	rec := &Record{
		RunnerID:          "owner/repo#123",
		InstanceID:        "i-1",
		JobID:             123,
		RunID:             456,
		Repository:        "owner/repo",
		Labels:            []string{"self-hosted", "linux"},
		Status:            StatusPending,
		CreatedAt:         now,
		UpdatedAt:         now,
		TTL:               now + 3600,
		GitHubRunnerID:    42,
		ReEnqueueAttempts: 1,
		LastAttemptAt:     now,
	}
	fake := &fakeDDBClient{}
	s := NewStore(fake, "t")

	if err := s.Put(context.Background(), rec); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(context.Background(), "owner/repo", 123)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil record")
	}
	if got.GitHubRunnerID != 42 {
		t.Errorf("GitHubRunnerID = %d, want 42", got.GitHubRunnerID)
	}
	if got.ReEnqueueAttempts != 1 {
		t.Errorf("ReEnqueueAttempts = %d, want 1", got.ReEnqueueAttempts)
	}
	if got.LastAttemptAt != now {
		t.Errorf("LastAttemptAt = %d, want %d", got.LastAttemptAt, now)
	}
}

func TestStore_PutGet_LegacyRecordWithoutNewFields(t *testing.T) {
	t.Parallel()

	// Simulates a legacy DDB record that predates the new lifecycle fields.
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Unix()
	legacy := map[string]types.AttributeValue{
		"runner_id":   &types.AttributeValueMemberS{Value: "owner/repo#1"},
		"instance_id": &types.AttributeValueMemberS{Value: "i-legacy"},
		"job_id":      &types.AttributeValueMemberN{Value: "1"},
		"run_id":      &types.AttributeValueMemberN{Value: "2"},
		"repository":  &types.AttributeValueMemberS{Value: "owner/repo"},
		"status":      &types.AttributeValueMemberS{Value: StatusPending},
		"created_at":  &types.AttributeValueMemberN{Value: itoa(now)},
		"updated_at":  &types.AttributeValueMemberN{Value: itoa(now)},
		"ttl":         &types.AttributeValueMemberN{Value: itoa(now + 3600)},
	}
	fake := &fakeDDBClient{items: map[string]map[string]types.AttributeValue{
		"owner/repo#1": legacy,
	}}
	s := NewStore(fake, "t")

	got, err := s.Get(context.Background(), "owner/repo", 1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil record")
	}
	if got.GitHubRunnerID != 0 || got.ReEnqueueAttempts != 0 || got.LastAttemptAt != 0 {
		t.Errorf("legacy record should have zero new fields, got %+v", got)
	}

	// Sanity: round-trip via marshal to confirm omitempty does not write zero values.
	item, err := attributevalue.MarshalMap(got)
	if err != nil {
		t.Fatalf("MarshalMap: %v", err)
	}
	if _, present := item["gh_runner_id"]; present {
		t.Error("gh_runner_id should be omitted when zero")
	}
	if _, present := item["re_enqueue_attempts"]; present {
		t.Error("re_enqueue_attempts should be omitted when zero")
	}
	if _, present := item["last_attempt_at"]; present {
		t.Error("last_attempt_at should be omitted when zero")
	}
}

func TestStore_Get_NotFoundReturnsStateErrNotFound(t *testing.T) {
	t.Parallel()

	fake := &fakeDDBClient{}
	s := NewStore(fake, "t")

	got, err := s.Get(context.Background(), "owner/repo", 999)
	if got != nil {
		t.Errorf("expected nil record, got %+v", got)
	}
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("expected state.ErrNotFound, got %v", err)
	}
}

func stringPtr(s string) *string     { return &s }
func int64Ptr(i int64) *int64        { return &i }
func intPtr(i int) *int              { return &i }
func timePtr(t time.Time) *time.Time { return &t }
