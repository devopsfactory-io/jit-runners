package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const githubAPI = "https://api.github.com"

// Client calls GitHub API with an installation access token.
type Client struct {
	httpClient *http.Client
	token      string
	baseURL    string
}

// NewClient creates a client that uses the given installation access token.
func NewClient(token string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		token:      token,
		baseURL:    githubAPI,
	}
}

// NewClientWithBase creates a client with a custom base URL (for testing).
func NewClientWithBase(token, baseURL string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		token:      token,
		baseURL:    baseURL,
	}
}

// InstallationToken obtains an installation access token using the GitHub App JWT.
func InstallationToken(ctx context.Context, appID, privateKeyPEM string, installationID int64) (string, error) {
	return installationTokenWithBase(ctx, appID, privateKeyPEM, installationID, githubAPI)
}

func installationTokenWithBase(ctx context.Context, appID, privateKeyPEM string, installationID int64, baseURL string) (string, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(privateKeyPEM))
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}
	appIDNum, err := strconv.ParseInt(appID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("parse app ID %q: %w", appID, err)
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"iss": appIDNum,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	jwtStr, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/app/installations/%d/access_tokens", baseURL, installationID),
		nil,
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // G704: URL from GitHub API constant
	if err != nil {
		return "", fmt.Errorf("request installation token: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("installation token: status %d", resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Token, nil
}

// JITRunnerConfig holds the response from the JIT runner config generation API.
// The shape mirrors the GitHub REST response:
//
//	{ "runner": { "id": 99, "name": "..." }, "encoded_jit_config": "..." }
type JITRunnerConfig struct {
	Runner     JITRunner `json:"runner"`
	EncodedJIT string    `json:"encoded_jit_config"`
}

// JITRunner is the nested runner identity returned alongside the encoded JIT
// config. The ID is required to deregister the runner from GitHub if scaleup
// later fails to launch the EC2 instance, or for end-of-job cleanup.
type JITRunner struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// QueuedJob is a workflow_job in queued state, returned by
// ListQueuedWorkflowJobs. Used by the rebalancer Lambda to compute demand
// and by cmd/scaleup for the demand-aware decision.
type QueuedJob struct {
	JobID  int64    `json:"id"`
	RunID  int64    `json:"run_id"`
	Status string   `json:"status"`
	Labels []string `json:"labels"`
}

type listRunsResponse struct {
	WorkflowRuns []struct {
		ID int64 `json:"id"`
	} `json:"workflow_runs"`
}

type listJobsResponse struct {
	Jobs []QueuedJob `json:"jobs"`
}

// GenerateJITConfig requests a just-in-time runner configuration from GitHub.
// This creates a single-use runner that auto-deregisters after one job.
func (c *Client) GenerateJITConfig(ctx context.Context, ownerRepo string, name string, labels []string) (*JITRunnerConfig, error) {
	body := struct {
		Name          string   `json:"name"`
		RunnerGroupID int      `json:"runner_group_id"`
		Labels        []string `json:"labels"`
	}{
		Name:          name,
		RunnerGroupID: 1, // default runner group
		Labels:        labels,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal JIT config request: %w", err)
	}
	url := fmt.Sprintf("%s/repos/%s/actions/runners/generate-jitconfig", c.baseURL, ownerRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req) //nolint:gosec // G704: URL from GitHub API constant
	if err != nil {
		return nil, fmt.Errorf("request JIT config: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	if resp.StatusCode != http.StatusCreated {
		const maxBody = 500
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		if err != nil {
			return nil, fmt.Errorf("generate JIT config: status %d (failed to read body: %w)", resp.StatusCode, err)
		}
		if len(b) > 0 {
			return nil, fmt.Errorf("generate JIT config: status %d: %s", resp.StatusCode, bytes.TrimSpace(b))
		}
		return nil, fmt.Errorf("generate JIT config: status %d", resp.StatusCode)
	}
	var cfg JITRunnerConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode JIT config: %w", err)
	}
	return &cfg, nil
}

// DeregisterRunner deletes a self-hosted runner registration on GitHub.
// Returns nil for both 204 (deleted) and 404 (already gone) so callers can
// invoke this unconditionally on cleanup paths. A zero runnerID is a no-op.
func (c *Client) DeregisterRunner(ctx context.Context, ownerRepo string, runnerID int64) error {
	if runnerID <= 0 {
		return nil // nothing to deregister
	}
	url := fmt.Sprintf("%s/repos/%s/actions/runners/%d", c.baseURL, ownerRepo, runnerID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("github: DeregisterRunner build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req) //nolint:gosec // G704: URL from GitHub API constant
	if err != nil {
		return fmt.Errorf("github: DeregisterRunner %d: %w", runnerID, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("github: DeregisterRunner %d: status %d", runnerID, resp.StatusCode)
}

// ListQueuedWorkflowJobs returns all workflow_jobs currently in `queued`
// status across the repo. Implementation: list workflow runs filtered to
// status=queued, then per-run list jobs and filter to status=queued.
// Pagination is requested at per_page=100 (workflows seldom exceed 100
// queued jobs at once; if needed, callers iterate via the periodic
// rebalancer cycle which re-queries each minute).
func (c *Client) ListQueuedWorkflowJobs(ctx context.Context, ownerRepo string) ([]QueuedJob, error) {
	runs, err := c.listQueuedRuns(ctx, ownerRepo)
	if err != nil {
		return nil, err
	}
	var queued []QueuedJob
	for _, run := range runs {
		jobs, err := c.listJobsForRun(ctx, ownerRepo, run.ID)
		if err != nil {
			return nil, err
		}
		for _, j := range jobs {
			if j.Status == "queued" {
				queued = append(queued, j)
			}
		}
	}
	return queued, nil
}

func (c *Client) listQueuedRuns(ctx context.Context, ownerRepo string) ([]struct {
	ID int64 `json:"id"`
}, error) {
	url := fmt.Sprintf("%s/repos/%s/actions/runs?status=queued&per_page=100", c.baseURL, ownerRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.httpClient.Do(req) //nolint:gosec // G704
	if err != nil {
		return nil, fmt.Errorf("github: list queued runs: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: list queued runs: status %d", resp.StatusCode)
	}
	var body listRunsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("github: list queued runs decode: %w", err)
	}
	return body.WorkflowRuns, nil
}

func (c *Client) listJobsForRun(ctx context.Context, ownerRepo string, runID int64) ([]QueuedJob, error) {
	url := fmt.Sprintf("%s/repos/%s/actions/runs/%d/jobs?per_page=100", c.baseURL, ownerRepo, runID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.httpClient.Do(req) //nolint:gosec // G704
	if err != nil {
		return nil, fmt.Errorf("github: list jobs for run %d: %w", runID, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: list jobs for run %d: status %d", runID, resp.StatusCode)
	}
	var body listJobsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("github: list jobs for run %d decode: %w", runID, err)
	}
	return body.Jobs, nil
}
