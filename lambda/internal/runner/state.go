package runner

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

// DynamoDBAPI abstracts the DynamoDB operations for testing.
type DynamoDBAPI interface {
	PutItem(ctx context.Context, input *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	GetItem(ctx context.Context, input *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	UpdateItem(ctx context.Context, input *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	Scan(ctx context.Context, input *dynamodb.ScanInput, opts ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

// Store manages runner state in DynamoDB.
type Store struct {
	client    DynamoDBAPI
	tableName string
}

// NewStore creates a Store for the given DynamoDB table.
func NewStore(client DynamoDBAPI, tableName string) *Store {
	return &Store{
		client:    client,
		tableName: tableName,
	}
}

// Put writes a runner record to DynamoDB.
// Uses a condition expression to prevent overwriting existing records (idempotency).
func (s *Store) Put(ctx context.Context, record *Record) error {
	item, err := attributevalue.MarshalMap(record)
	if err != nil {
		return fmt.Errorf("marshal runner record: %w", err)
	}

	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(runner_id)"),
	})
	if err != nil {
		return fmt.Errorf("put runner record: %w", err)
	}
	return nil
}

// Get retrieves a runner record by repository and job ID.
func (s *Store) Get(ctx context.Context, repository string, jobID int64) (*Record, error) {
	return s.GetByID(ctx, runnerID(repository, jobID))
}

// GetByID retrieves a runner record by its composite primary key
// ("<repository>#<jobID>"). It is the single-argument variant used by
// adapters (e.g. StateAdapter) that hold a key produced elsewhere in the
// pipeline (the lifecycle handler does this) without needing to split it
// back into its components.
func (s *Store) GetByID(ctx context.Context, id string) (*Record, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"runner_id": &types.AttributeValueMemberS{Value: id},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get runner record: %w", err)
	}
	if out.Item == nil {
		return nil, state.ErrNotFound
	}
	var record Record
	if err := attributevalue.UnmarshalMap(out.Item, &record); err != nil {
		return nil, fmt.Errorf("unmarshal runner record: %w", err)
	}
	return &record, nil
}

// UpdateStatus updates the status of a runner record.
func (s *Store) UpdateStatus(ctx context.Context, repository string, jobID int64, status string) error {
	id := runnerID(repository, jobID)
	now := time.Now().Unix()

	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"runner_id": &types.AttributeValueMemberS{Value: id},
		},
		UpdateExpression: aws.String("SET #status = :status, updated_at = :now"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status": &types.AttributeValueMemberS{Value: status},
			":now":    &types.AttributeValueMemberN{Value: itoa(now)},
		},
	})
	if err != nil {
		return fmt.Errorf("update runner status: %w", err)
	}
	return nil
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
	// Always bump updated_at so the live record reflects the write.
	add("updated_at", "u", &types.AttributeValueMemberN{Value: strconv.FormatInt(time.Now().Unix(), 10)})

	if len(exprs) == 1 {
		// Only updated_at would change — caller didn't specify any field. Treat as no-op.
		return nil
	}

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

// ListByStatus returns all runner records with the given status.
func (s *Store) ListByStatus(ctx context.Context, status string) ([]*Record, error) {
	out, err := s.client.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(s.tableName),
		FilterExpression: aws.String("#status = :status"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status": &types.AttributeValueMemberS{Value: status},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("scan runners by status: %w", err)
	}
	var records []*Record
	for _, item := range out.Items {
		var record Record
		if err := attributevalue.UnmarshalMap(item, &record); err != nil {
			return nil, fmt.Errorf("unmarshal runner record: %w", err)
		}
		records = append(records, &record)
	}
	return records, nil
}
