package runner

import "time"

// Status constants for runner records.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// Record is the DynamoDB state record for an active runner.
type Record struct {
	RunnerID   string   `dynamodbav:"runner_id"`
	InstanceID string   `dynamodbav:"instance_id"`
	JobID      int64    `dynamodbav:"job_id"`
	RunID      int64    `dynamodbav:"run_id"`
	Repository string   `dynamodbav:"repository"`
	Labels     []string `dynamodbav:"labels"`
	Status     string   `dynamodbav:"status"`
	CreatedAt  int64    `dynamodbav:"created_at"`
	UpdatedAt  int64    `dynamodbav:"updated_at"`
	TTL        int64    `dynamodbav:"ttl"`
}

// NewRecord creates a runner record with sensible defaults.
func NewRecord(repository string, jobID, runID int64, instanceID string, labels []string) *Record {
	now := time.Now().Unix()
	return &Record{
		RunnerID:   runnerID(repository, jobID),
		InstanceID: instanceID,
		JobID:      jobID,
		RunID:      runID,
		Repository: repository,
		Labels:     labels,
		Status:     StatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
		TTL:        now + 86400, // 24h TTL
	}
}

func runnerID(repository string, jobID int64) string {
	return repository + "#" + itoa(jobID)
}

func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
