package github

import (
	"context"
	"net/http"
	"net/http/httptest"
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
