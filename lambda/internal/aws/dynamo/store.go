// Package dynamo is the AWS DynamoDB implementation of the state.RunnerStore
// contract defined in internal/state.
package dynamo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// API abstracts the DynamoDB operations for testing.
type API interface {
	PutItem(ctx context.Context, input *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	GetItem(ctx context.Context, input *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	UpdateItem(ctx context.Context, input *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	Scan(ctx context.Context, input *dynamodb.ScanInput, opts ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	Query(ctx context.Context, input *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	DeleteItem(ctx context.Context, input *dynamodb.DeleteItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

// Store manages runner state in DynamoDB and satisfies state.RunnerStore.
type Store struct {
	client    API
	tableName string
}

// Compile-time assertion that Store satisfies state.RunnerStore.
var _ state.RunnerStore = (*Store)(nil)

// NewStore creates a Store for the given DynamoDB table.
func NewStore(client API, tableName string) *Store {
	return &Store{
		client:    client,
		tableName: tableName,
	}
}

// dbRecord is the on-disk DynamoDB shape. The attribute names match the
// pre-refactor schema so the live table does not require migration.
//
// New lifecycle fields (gh_runner_id, re_enqueue_attempts, last_attempt_at)
// use omitempty so legacy records that pre-date #47 round-trip without
// emitting zero-valued attributes.
type dbRecord struct {
	RunnerID string `dynamodbav:"runner_id"`
	// InstanceID is the key of the instance_id-index GSI. It uses omitempty so
	// pending records (written before the EC2 instance exists, so InstanceID is
	// empty) are not added to the GSI — DynamoDB rejects an empty-string value
	// for a GSI key attribute. The instance ID is set later via Update once the
	// instance launches, at which point the record joins the sparse index.
	InstanceID        string   `dynamodbav:"instance_id,omitempty"`
	JobID             int64    `dynamodbav:"job_id,omitempty"`
	RunID             int64    `dynamodbav:"run_id,omitempty"`
	Repository        string   `dynamodbav:"repository"`
	Labels            []string `dynamodbav:"labels"`
	Status            string   `dynamodbav:"status"`
	CreatedAt         int64    `dynamodbav:"created_at"`
	UpdatedAt         int64    `dynamodbav:"updated_at"`
	TTL               int64    `dynamodbav:"ttl"`
	GitHubRunnerID    int64    `dynamodbav:"gh_runner_id,omitempty"`
	ReEnqueueAttempts int      `dynamodbav:"re_enqueue_attempts,omitempty"`
	LastAttemptAt     int64    `dynamodbav:"last_attempt_at,omitempty"`
}

func toDB(r state.Runner) dbRecord {
	createdAt := r.LaunchedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	updatedAt := r.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	ttl := r.TTL
	if ttl.IsZero() {
		ttl = createdAt.Add(24 * time.Hour)
	}
	var lastAttempt int64
	if !r.LastAttemptAt.IsZero() {
		lastAttempt = r.LastAttemptAt.Unix()
	}
	return dbRecord{
		RunnerID:          r.ID,
		InstanceID:        r.InstanceID,
		JobID:             r.JobID,
		RunID:             r.WorkflowRunID,
		Repository:        r.Repository,
		Labels:            r.Labels,
		Status:            r.Status,
		CreatedAt:         createdAt.Unix(),
		UpdatedAt:         updatedAt.Unix(),
		TTL:               ttl.Unix(),
		GitHubRunnerID:    r.GitHubRunnerID,
		ReEnqueueAttempts: r.ReEnqueueAttempts,
		LastAttemptAt:     lastAttempt,
	}
}

func fromDB(d dbRecord) state.Runner {
	var lastAttempt time.Time
	if d.LastAttemptAt > 0 {
		lastAttempt = time.Unix(d.LastAttemptAt, 0).UTC()
	}
	return state.Runner{
		ID:                d.RunnerID,
		InstanceID:        d.InstanceID,
		Repository:        d.Repository,
		Labels:            d.Labels,
		Status:            d.Status,
		LaunchedAt:        time.Unix(d.CreatedAt, 0).UTC(),
		UpdatedAt:         time.Unix(d.UpdatedAt, 0).UTC(),
		TTL:               time.Unix(d.TTL, 0).UTC(),
		GitHubRunnerID:    d.GitHubRunnerID,
		JobID:             d.JobID,
		WorkflowRunID:     d.RunID,
		ReEnqueueAttempts: d.ReEnqueueAttempts,
		LastAttemptAt:     lastAttempt,
	}
}

// Put writes a runner record to DynamoDB. It overwrites any existing record
// with the same ID; idempotency is the caller's responsibility (e.g. the
// scaleup handler does a Get before Put).
func (s *Store) Put(ctx context.Context, r state.Runner) error {
	if r.ID == "" {
		return fmt.Errorf("runner ID is required")
	}
	rec := toDB(r)
	// Always bump UpdatedAt to now so status transitions reflect a fresh
	// timestamp — preserves prior UpdateStatus behavior.
	now := time.Now().Unix()
	if rec.UpdatedAt < now {
		rec.UpdatedAt = now
	}
	item, err := attributevalue.MarshalMap(rec)
	if err != nil {
		return fmt.Errorf("marshal runner record: %w", err)
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("put runner record: %w", err)
	}
	return nil
}

// Get retrieves a runner record by its ID. Returns state.ErrNotFound when
// no record exists.
func (s *Store) Get(ctx context.Context, id string) (state.Runner, error) {
	if id == "" {
		return state.Runner{}, fmt.Errorf("runner ID is required")
	}
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"runner_id": &types.AttributeValueMemberS{Value: id},
		},
	})
	if err != nil {
		return state.Runner{}, fmt.Errorf("get runner record: %w", err)
	}
	if out.Item == nil {
		return state.Runner{}, state.ErrNotFound
	}
	var rec dbRecord
	if err := attributevalue.UnmarshalMap(out.Item, &rec); err != nil {
		return state.Runner{}, fmt.Errorf("unmarshal runner record: %w", err)
	}
	return fromDB(rec), nil
}

// GetByInstanceID queries the instance_id-index GSI to look up a runner
// by its cloud instance ID. Returns state.ErrNotFound if no row matches.
// Used by the scaledown orphan sweep (issue #74).
func (s *Store) GetByInstanceID(ctx context.Context, instanceID string) (state.Runner, error) {
	if instanceID == "" {
		return state.Runner{}, state.ErrNotFound
	}
	out, err := s.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		IndexName:              aws.String("instance_id-index"),
		KeyConditionExpression: aws.String("instance_id = :iid"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":iid": &types.AttributeValueMemberS{Value: instanceID},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return state.Runner{}, fmt.Errorf("query instance_id-index: %w", err)
	}
	if len(out.Items) == 0 {
		return state.Runner{}, state.ErrNotFound
	}
	var rec dbRecord
	if err := attributevalue.UnmarshalMap(out.Items[0], &rec); err != nil {
		return state.Runner{}, fmt.Errorf("unmarshal runner record: %w", err)
	}
	return fromDB(rec), nil
}

// List returns runner records matching the filter. StatusEq narrows by
// status; OlderThan filters records whose LaunchedAt is older than now-OlderThan.
func (s *Store) List(ctx context.Context, f state.Filter) ([]state.Runner, error) {
	input := &dynamodb.ScanInput{TableName: aws.String(s.tableName)}
	if f.StatusEq != "" {
		input.FilterExpression = aws.String("#status = :status")
		input.ExpressionAttributeNames = map[string]string{"#status": "status"}
		input.ExpressionAttributeValues = map[string]types.AttributeValue{
			":status": &types.AttributeValueMemberS{Value: f.StatusEq},
		}
	}
	out, err := s.client.Scan(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("scan runners: %w", err)
	}
	var runners []state.Runner
	cutoff := time.Time{}
	if f.OlderThan > 0 {
		cutoff = time.Now().Add(-f.OlderThan)
	}
	for _, item := range out.Items {
		var rec dbRecord
		if err := attributevalue.UnmarshalMap(item, &rec); err != nil {
			return nil, fmt.Errorf("unmarshal runner record: %w", err)
		}
		r := fromDB(rec)
		if !cutoff.IsZero() && !r.LaunchedAt.Before(cutoff) {
			continue
		}
		runners = append(runners, r)
	}
	return runners, nil
}

// Update applies a partial update to a runner record by composite ID.
// Only non-nil fields in u are written. UpdatedAt is always bumped to the
// current Unix time. If u carries no field changes, this is a no-op.
func (s *Store) Update(ctx context.Context, id string, u state.RunnerUpdate) error {
	exprs := []string{}
	names := map[string]string{}
	values := map[string]types.AttributeValue{}

	add := func(attrName, exprName string, v types.AttributeValue) {
		exprs = append(exprs, fmt.Sprintf("#%s = :%s", exprName, exprName))
		names["#"+exprName] = attrName
		values[":"+exprName] = v
	}

	if u.Status != nil {
		add("status", "s", &types.AttributeValueMemberS{Value: *u.Status})
	}
	if u.InstanceID != nil {
		add("instance_id", "i", &types.AttributeValueMemberS{Value: *u.InstanceID})
	}
	if u.GitHubRunnerID != nil {
		add("gh_runner_id", "g", &types.AttributeValueMemberN{Value: strconv.FormatInt(*u.GitHubRunnerID, 10)})
	}
	if u.ReEnqueueAttempts != nil {
		add("re_enqueue_attempts", "r", &types.AttributeValueMemberN{Value: strconv.Itoa(*u.ReEnqueueAttempts)})
	}
	if u.LastAttemptAt != nil {
		add("last_attempt_at", "l", &types.AttributeValueMemberN{Value: strconv.FormatInt(u.LastAttemptAt.Unix(), 10)})
	}

	if len(exprs) == 0 {
		// Caller didn't specify any field. Treat as no-op.
		return nil
	}

	// Always bump updated_at so the live record reflects the write.
	add("updated_at", "u", &types.AttributeValueMemberN{Value: strconv.FormatInt(time.Now().Unix(), 10)})

	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"runner_id": &types.AttributeValueMemberS{Value: id},
		},
		UpdateExpression:          aws.String("SET " + strings.Join(exprs, ", ")),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
	})
	if err != nil {
		return fmt.Errorf("dynamo: Update %s: %w", id, err)
	}
	return nil
}

// ListActiveRepos returns the deduped Repository values across runner records
// whose created_at (LaunchedAt) is at or after since. It uses a server-side
// FilterExpression so that only matching items are returned from DynamoDB.
// Results need not be sorted; callers that need stable ordering must sort.
func (s *Store) ListActiveRepos(ctx context.Context, since time.Time) ([]string, error) {
	input := &dynamodb.ScanInput{
		TableName:            aws.String(s.tableName),
		ProjectionExpression: aws.String("repository, created_at"),
		FilterExpression:     aws.String("created_at >= :since"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":since": &types.AttributeValueMemberN{Value: strconv.FormatInt(since.Unix(), 10)},
		},
	}
	out, err := s.client.Scan(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("scan active repos: %w", err)
	}
	seen := make(map[string]struct{})
	var repos []string
	for _, item := range out.Items {
		v, ok := item["repository"].(*types.AttributeValueMemberS)
		if !ok || v.Value == "" {
			continue
		}
		if _, dup := seen[v.Value]; dup {
			continue
		}
		seen[v.Value] = struct{}{}
		repos = append(repos, v.Value)
	}
	return repos, nil
}

// Delete removes a runner record by its ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("runner ID is required")
	}
	_, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"runner_id": &types.AttributeValueMemberS{Value: id},
		},
	})
	if err != nil {
		return fmt.Errorf("delete runner record: %w", err)
	}
	return nil
}
