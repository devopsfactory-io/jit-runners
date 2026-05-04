// Package firestore is the GCP Firestore implementation of the state.RunnerStore
// contract defined in internal/state. It mirrors the shape of
// internal/aws/dynamo.Store: a narrow firestoreAPI interface keeps the Store
// body decoupled from the real SDK so tests can use an in-memory fake without
// spinning up a live project.
package firestore

import (
	"context"
	"fmt"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	appstate "github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// QueryFilter is a single equality or range predicate applied to a collection query.
type QueryFilter struct {
	Field string
	Op    string // "==" or ">="
	Value any
}

// firestoreAPI is the narrow Firestore surface the Store needs.
// Only methods actually called by Store are listed, keeping the fake trivial.
type firestoreAPI interface {
	// Set creates or replaces the document at coll/id with data.
	Set(ctx context.Context, coll, id string, data map[string]any) error
	// Get returns the document data, or state.ErrNotFound if absent.
	Get(ctx context.Context, coll, id string) (map[string]any, error)
	// Update applies a partial update; only keys in data are written.
	Update(ctx context.Context, coll, id string, data map[string]any) error
	// Delete removes the document. No-op if absent.
	Delete(ctx context.Context, coll, id string) error
	// Query returns all documents in coll that match every filter.
	Query(ctx context.Context, coll string, filters []QueryFilter) ([]map[string]any, error)
}

// Store manages runner state in Firestore and satisfies state.RunnerStore.
type Store struct {
	api      firestoreAPI
	collName string
}

// Compile-time assertion that *Store satisfies state.RunnerStore.
var _ appstate.RunnerStore = (*Store)(nil)

// NewStore creates a Store backed by the real Firestore client.
// collectionName is the top-level collection that holds runner documents.
func NewStore(client *firestore.Client, collectionName string) *Store {
	return &Store{api: newClientAdapter(client), collName: collectionName}
}

// newStoreWithAPI is used in tests to inject a fake firestoreAPI.
func newStoreWithAPI(api firestoreAPI, collectionName string) *Store {
	return &Store{api: api, collName: collectionName}
}

// Put writes a runner record to Firestore. It overwrites any existing record
// with the same ID; idempotency is the caller's responsibility.
func (s *Store) Put(ctx context.Context, r appstate.Runner) error {
	if r.ID == "" {
		return fmt.Errorf("gcp/firestore: Put: runner ID is required")
	}
	doc := toDoc(r)
	if err := s.api.Set(ctx, s.collName, r.ID, doc); err != nil {
		return fmt.Errorf("gcp/firestore: Put: %w", err)
	}
	return nil
}

// Get retrieves a runner record by ID. Returns state.ErrNotFound when absent.
func (s *Store) Get(ctx context.Context, id string) (appstate.Runner, error) {
	if id == "" {
		return appstate.Runner{}, fmt.Errorf("gcp/firestore: Get: runner ID is required")
	}
	data, err := s.api.Get(ctx, s.collName, id)
	if err != nil {
		return appstate.Runner{}, fmt.Errorf("gcp/firestore: Get %s: %w", id, err)
	}
	return fromDoc(data), nil
}

// GetByInstanceID looks up a runner by its cloud instance ID using a
// Firestore field-equality query on the `instance_id` field.
// Returns state.ErrNotFound if no document matches. Used by the scaledown
// orphan sweep (issue #74).
//
// Firestore's default single-field auto-index handles `==` queries on
// `instance_id` without an explicit composite-index declaration — no
// schema change required.
func (s *Store) GetByInstanceID(ctx context.Context, instanceID string) (appstate.Runner, error) {
	if instanceID == "" {
		return appstate.Runner{}, appstate.ErrNotFound
	}
	docs, err := s.api.Query(ctx, s.collName, []QueryFilter{
		{Field: "instance_id", Op: "==", Value: instanceID},
	})
	if err != nil {
		return appstate.Runner{}, fmt.Errorf("gcp/firestore: GetByInstanceID %s: %w", instanceID, err)
	}
	if len(docs) == 0 {
		return appstate.Runner{}, appstate.ErrNotFound
	}
	return fromDoc(docs[0]), nil
}

// List returns runner records matching the filter. StatusEq narrows by status;
// OlderThan filters client-side records whose LaunchedAt is older than now-OlderThan.
func (s *Store) List(ctx context.Context, f appstate.Filter) ([]appstate.Runner, error) {
	var filters []QueryFilter
	if f.StatusEq != "" {
		filters = append(filters, QueryFilter{Field: "status", Op: "==", Value: f.StatusEq})
	}
	docs, err := s.api.Query(ctx, s.collName, filters)
	if err != nil {
		return nil, fmt.Errorf("gcp/firestore: List: %w", err)
	}
	var cutoff time.Time
	if f.OlderThan > 0 {
		cutoff = time.Now().Add(-f.OlderThan)
	}
	runners := make([]appstate.Runner, 0, len(docs))
	for _, d := range docs {
		r := fromDoc(d)
		if !cutoff.IsZero() && !r.LaunchedAt.Before(cutoff) {
			continue
		}
		runners = append(runners, r)
	}
	return runners, nil
}

// Update applies a partial update to an existing runner record. Only non-nil
// fields in u are written. updated_at is always bumped to the current time.
// If u carries no field changes the operation is a no-op.
func (s *Store) Update(ctx context.Context, id string, u appstate.RunnerUpdate) error {
	data := make(map[string]any)

	if u.Status != nil {
		data["status"] = *u.Status
	}
	if u.InstanceID != nil {
		data["instance_id"] = *u.InstanceID
	}
	if u.GitHubRunnerID != nil {
		data["gh_runner_id"] = *u.GitHubRunnerID
	}
	if u.ReEnqueueAttempts != nil {
		data["re_enqueue_attempts"] = *u.ReEnqueueAttempts
	}
	if u.LastAttemptAt != nil {
		data["last_attempt_at"] = *u.LastAttemptAt
	}

	if len(data) == 0 {
		return nil
	}

	data["updated_at"] = time.Now()

	if err := s.api.Update(ctx, s.collName, id, data); err != nil {
		return fmt.Errorf("gcp/firestore: Update %s: %w", id, err)
	}
	return nil
}

// Delete removes a runner record by ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("gcp/firestore: Delete: runner ID is required")
	}
	if err := s.api.Delete(ctx, s.collName, id); err != nil {
		return fmt.Errorf("gcp/firestore: Delete %s: %w", id, err)
	}
	return nil
}

// ListActiveRepos returns the deduped Repository values across runner records
// whose created_at is at or after since. Uses a server-side >= filter backed
// by Firestore's default single-field auto-index on created_at (spec D8).
// Results are sorted for stable output.
func (s *Store) ListActiveRepos(ctx context.Context, since time.Time) ([]string, error) {
	filters := []QueryFilter{
		{Field: "created_at", Op: ">=", Value: since},
	}
	docs, err := s.api.Query(ctx, s.collName, filters)
	if err != nil {
		return nil, fmt.Errorf("gcp/firestore: ListActiveRepos: %w", err)
	}
	seen := make(map[string]struct{})
	var repos []string
	for _, d := range docs {
		repo, ok := d["repository"].(string)
		if !ok || repo == "" {
			continue
		}
		if _, dup := seen[repo]; dup {
			continue
		}
		seen[repo] = struct{}{}
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	return repos, nil
}

// toDoc converts a state.Runner to the Firestore document map using snake_case
// keys. created_at is set to LaunchedAt if non-zero, otherwise time.Now()
// (mirrors the AWS dynamo store's toDB behaviour).
func toDoc(r appstate.Runner) map[string]any {
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

	doc := map[string]any{
		"runner_id":           r.ID,
		"instance_id":         r.InstanceID,
		"repository":          r.Repository,
		"labels":              r.Labels,
		"status":              r.Status,
		"launched_at":         r.LaunchedAt,
		"created_at":          createdAt,
		"updated_at":          updatedAt,
		"ttl":                 ttl,
		"gh_runner_id":        r.GitHubRunnerID,
		"job_id":              r.JobID,
		"workflow_run_id":     r.WorkflowRunID,
		"re_enqueue_attempts": r.ReEnqueueAttempts,
	}
	if !r.LastAttemptAt.IsZero() {
		doc["last_attempt_at"] = r.LastAttemptAt
	}
	return doc
}

// fromDoc converts a Firestore document map back to a state.Runner.
func fromDoc(d map[string]any) appstate.Runner {
	r := appstate.Runner{
		ID:                stringField(d, "runner_id"),
		InstanceID:        stringField(d, "instance_id"),
		Repository:        stringField(d, "repository"),
		Status:            stringField(d, "status"),
		Labels:            stringSliceField(d, "labels"),
		LaunchedAt:        timeField(d, "launched_at"),
		UpdatedAt:         timeField(d, "updated_at"),
		TTL:               timeField(d, "ttl"),
		GitHubRunnerID:    int64Field(d, "gh_runner_id"),
		JobID:             int64Field(d, "job_id"),
		WorkflowRunID:     int64Field(d, "workflow_run_id"),
		ReEnqueueAttempts: intField(d, "re_enqueue_attempts"),
		LastAttemptAt:     timeField(d, "last_attempt_at"),
	}
	// If launched_at was not stored (legacy doc), fall back to created_at.
	if r.LaunchedAt.IsZero() {
		r.LaunchedAt = timeField(d, "created_at")
	}
	return r
}

func stringField(d map[string]any, key string) string {
	v, ok := d[key].(string)
	if !ok {
		return ""
	}
	return v
}

func stringSliceField(d map[string]any, key string) []string {
	v, ok := d[key]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, elem := range t {
			if s, ok := elem.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func timeField(d map[string]any, key string) time.Time {
	v, ok := d[key]
	if !ok {
		return time.Time{}
	}
	if t, ok := v.(time.Time); ok {
		return t
	}
	return time.Time{}
}

func int64Field(d map[string]any, key string) int64 {
	v, ok := d[key]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	}
	return 0
}

func intField(d map[string]any, key string) int {
	v, ok := d[key]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	}
	return 0
}

// clientAdapter wraps *firestore.Client to satisfy firestoreAPI.
type clientAdapter struct {
	client *firestore.Client
}

func newClientAdapter(c *firestore.Client) *clientAdapter {
	return &clientAdapter{client: c}
}

func (a *clientAdapter) Set(ctx context.Context, coll, id string, data map[string]any) error {
	_, err := a.client.Collection(coll).Doc(id).Set(ctx, data)
	return err
}

func (a *clientAdapter) Get(ctx context.Context, coll, id string) (map[string]any, error) {
	snap, err := a.client.Collection(coll).Doc(id).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return nil, appstate.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return snap.Data(), nil
}

func (a *clientAdapter) Update(ctx context.Context, coll, id string, data map[string]any) error {
	updates := make([]firestore.Update, 0, len(data))
	for k, v := range data {
		updates = append(updates, firestore.Update{Path: k, Value: v})
	}
	_, err := a.client.Collection(coll).Doc(id).Update(ctx, updates)
	return err
}

func (a *clientAdapter) Delete(ctx context.Context, coll, id string) error {
	_, err := a.client.Collection(coll).Doc(id).Delete(ctx)
	return err
}

func (a *clientAdapter) Query(ctx context.Context, coll string, filters []QueryFilter) ([]map[string]any, error) {
	q := a.client.Collection(coll).Query
	for _, f := range filters {
		q = q.Where(f.Field, f.Op, f.Value)
	}
	iter := q.Documents(ctx)
	defer iter.Stop()

	var results []map[string]any
	for {
		snap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		results = append(results, snap.Data())
	}
	return results, nil
}
