package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestClient_DeregisterRunner(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"204 deleted", http.StatusNoContent, false},
		{"404 already gone", http.StatusNotFound, false},
		{"500 server error", http.StatusInternalServerError, true},
		{"502 bad gateway", http.StatusBadGateway, true},
		{"403 forbidden", http.StatusForbidden, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got, want := r.Method, http.MethodDelete; got != want {
					t.Errorf("method: got %s want %s", got, want)
				}
				if got, want := r.URL.Path, "/repos/owner/repo/actions/runners/42"; got != want {
					t.Errorf("path: got %s want %s", got, want)
				}
				if got, want := r.Header.Get("Authorization"), "Bearer test-token"; got != want {
					t.Errorf("auth: got %q want %q", got, want)
				}
				if got, want := r.Header.Get("Accept"), "application/vnd.github+json"; got != want {
					t.Errorf("accept: got %q want %q", got, want)
				}
				if got, want := r.Header.Get("X-GitHub-Api-Version"), "2022-11-28"; got != want {
					t.Errorf("api-version: got %q want %q", got, want)
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			c := NewClientWithBase("test-token", srv.URL)
			c.httpClient = srv.Client()
			err := c.DeregisterRunner(context.Background(), "owner/repo", 42)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for status %d, got nil", tc.status)
				}
			} else if err != nil {
				t.Errorf("unexpected error for status %d: %v", tc.status, err)
			}
		})
	}
}

func TestClient_DeregisterRunner_ZeroID(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer srv.Close()

	c := NewClientWithBase("tok", srv.URL)
	c.httpClient = srv.Client()
	if err := c.DeregisterRunner(context.Background(), "owner/repo", 0); err != nil {
		t.Fatalf("zero ID should be no-op, got error: %v", err)
	}
	if called {
		t.Fatal("zero ID should not hit the network")
	}
}

func TestClient_DeregisterRunner_NegativeID(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer srv.Close()

	c := NewClientWithBase("tok", srv.URL)
	c.httpClient = srv.Client()
	if err := c.DeregisterRunner(context.Background(), "owner/repo", -1); err != nil {
		t.Fatalf("negative ID should be no-op, got error: %v", err)
	}
	if called {
		t.Fatal("negative ID should not hit the network")
	}
}

func TestListQueuedWorkflowJobs(t *testing.T) {
	t.Run("empty repo returns empty slice", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/repos/owner/repo/actions/runs" {
				_, _ = w.Write([]byte(`{"total_count":0,"workflow_runs":[]}`))
				return
			}
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}))
		defer srv.Close()

		c := NewClientWithBase("test-token", srv.URL)
		got, err := c.ListQueuedWorkflowJobs(context.Background(), "owner/repo")
		if err != nil {
			t.Fatalf("ListQueuedWorkflowJobs: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d jobs, want 0", len(got))
		}
	})

	t.Run("single queued run with two queued jobs", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/repos/owner/repo/actions/runs":
				_, _ = w.Write([]byte(`{"total_count":1,"workflow_runs":[{"id":111}]}`))
			case "/repos/owner/repo/actions/runs/111/jobs":
				_, _ = w.Write([]byte(`{"total_count":2,"jobs":[
				 {"id":1001,"run_id":111,"status":"queued","labels":["self-hosted","large"]},
				 {"id":1002,"run_id":111,"status":"queued","labels":["self-hosted","medium"]}
				]}`))
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
		}))
		defer srv.Close()

		c := NewClientWithBase("test-token", srv.URL)
		got, err := c.ListQueuedWorkflowJobs(context.Background(), "owner/repo")
		if err != nil {
			t.Fatalf("ListQueuedWorkflowJobs: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d jobs, want 2", len(got))
		}
		if got[0].JobID != 1001 || got[1].JobID != 1002 {
			t.Errorf("unexpected job IDs: %+v", got)
		}
	})

	t.Run("filters out non-queued jobs in same run", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/repos/owner/repo/actions/runs":
				_, _ = w.Write([]byte(`{"total_count":1,"workflow_runs":[{"id":111}]}`))
			case "/repos/owner/repo/actions/runs/111/jobs":
				_, _ = w.Write([]byte(`{"total_count":2,"jobs":[
				 {"id":1001,"run_id":111,"status":"queued","labels":["self-hosted","large"]},
				 {"id":1002,"run_id":111,"status":"in_progress","labels":["self-hosted","large"]}
				]}`))
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
		}))
		defer srv.Close()

		c := NewClientWithBase("test-token", srv.URL)
		got, err := c.ListQueuedWorkflowJobs(context.Background(), "owner/repo")
		if err != nil {
			t.Fatalf("ListQueuedWorkflowJobs: %v", err)
		}
		if len(got) != 1 || got[0].JobID != 1001 {
			t.Errorf("expected only job 1001, got %+v", got)
		}
	})

	t.Run("server error returns wrapped error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer srv.Close()

		c := NewClientWithBase("test-token", srv.URL)
		_, err := c.ListQueuedWorkflowJobs(context.Background(), "owner/repo")
		if err == nil {
			t.Fatal("expected error on 429, got nil")
		}
	})
}

func TestListInstallationRepositories(t *testing.T) {
	t.Run("empty installation returns empty slice", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/installation/repositories" {
				t.Errorf("unexpected path %q", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"total_count":0,"repositories":[]}`))
		}))
		defer srv.Close()
		c := NewClientWithBase("tok", srv.URL)
		got, err := c.ListInstallationRepositories(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 repos, got %d: %v", len(got), got)
		}
	})

	t.Run("single page returns all full_names", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"total_count":2,"repositories":[
				{"full_name":"devopsfactory-io/jit-runners"},
				{"full_name":"devopsfactory-io/neptune"}
			]}`))
		}))
		defer srv.Close()
		c := NewClientWithBase("tok", srv.URL)
		got, err := c.ListInstallationRepositories(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"devopsfactory-io/jit-runners", "devopsfactory-io/neptune"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("paginates by page query param until total_count reached", func(t *testing.T) {
		var calls int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			page := r.URL.Query().Get("page")
			w.WriteHeader(http.StatusOK)
			switch page {
			case "", "1":
				_, _ = w.Write([]byte(`{"total_count":3,"repositories":[
					{"full_name":"o/a"},{"full_name":"o/b"}
				]}`))
			case "2":
				_, _ = w.Write([]byte(`{"total_count":3,"repositories":[
					{"full_name":"o/c"}
				]}`))
			default:
				t.Errorf("unexpected page %q", page)
			}
		}))
		defer srv.Close()
		c := NewClientWithBase("tok", srv.URL)
		got, err := c.ListInstallationRepositories(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"o/a", "o/b", "o/c"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
		if calls != 2 {
			t.Errorf("expected 2 page fetches, got %d", calls)
		}
	})

	t.Run("non-200 returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		c := NewClientWithBase("tok", srv.URL)
		_, err := c.ListInstallationRepositories(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
