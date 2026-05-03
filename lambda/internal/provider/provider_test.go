package provider

import (
	"context"
	"strings"
	"testing"
)

func TestNew_DefaultsToAWS(t *testing.T) {
	// We can't fully exercise newAWS without AWS creds, so we only assert
	// that empty-name routes to newAWS and surfaces newAWS's error path
	// (i.e. doesn't return "unknown provider"). Setting an obviously bad
	// region forces newAWS to fail at LoadDefaultConfig; what matters is
	// that the error message comes from the AWS path, not the dispatch.
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	_, err := New(context.Background(), "")
	if err != nil && strings.Contains(err.Error(), "unknown CLOUD_PROVIDER") {
		t.Errorf("empty name should not return unknown-provider error, got %v", err)
	}
}

func TestNew_GCPRequiresProject(t *testing.T) {
	t.Setenv("GCP_PROJECT", "")
	_, err := New(context.Background(), "gcp")
	if err == nil {
		t.Fatal("expected error when GCP_PROJECT unset")
	}
	if !strings.Contains(err.Error(), "GCP_PROJECT") {
		t.Errorf("expected error to mention GCP_PROJECT, got %v", err)
	}
}

func TestNew_UnknownProviderReturnsError(t *testing.T) {
	_, err := New(context.Background(), "azure")
	if err == nil {
		t.Fatal("expected unknown-provider error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown CLOUD_PROVIDER") {
		t.Errorf("got %v, want unknown-provider error", err)
	}
}

func TestBundle_Close_NilSafe(t *testing.T) {
	var b *Bundle
	if err := b.Close(); err != nil {
		t.Errorf("nil bundle Close should be no-op, got %v", err)
	}
	b2 := &Bundle{}
	if err := b2.Close(); err != nil {
		t.Errorf("bundle without CloseFn should be no-op, got %v", err)
	}
}
