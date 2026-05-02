package ssm

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// fakeSSM is a minimal stand-in for *ssm.Client that records calls and
// returns scripted responses keyed by parameter name.
type fakeSSM struct {
	calls    atomic.Int32
	values   map[string]string // name -> value to return
	notFound map[string]bool   // name -> simulate ParameterNotFound
	apiErr   error             // returned for every call when set
}

func (f *fakeSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.calls.Add(1)
	if f.apiErr != nil {
		return nil, f.apiErr
	}
	name := aws.ToString(in.Name)
	if f.notFound[name] {
		return nil, &types.ParameterNotFound{}
	}
	v, ok := f.values[name]
	if !ok {
		return nil, &types.ParameterNotFound{}
	}
	return &ssm.GetParameterOutput{
		Parameter: &types.Parameter{
			Name:  aws.String(name),
			Value: aws.String(v),
		},
	}, nil
}

func TestLoader_GetCachesValue(t *testing.T) {
	f := &fakeSSM{values: map[string]string{"/jit-runners/runner-log-level": "debug"}}
	l := New(f, 100*time.Millisecond)
	ctx := context.Background()

	got, err := l.Get(ctx, "/jit-runners/runner-log-level", "info")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "debug" {
		t.Errorf("first Get: got %q, want debug", got)
	}

	got, _ = l.Get(ctx, "/jit-runners/runner-log-level", "info")
	if got != "debug" {
		t.Errorf("cached Get: got %q, want debug", got)
	}
	if c := f.calls.Load(); c != 1 {
		t.Errorf("API calls = %d, want 1 (cache miss only)", c)
	}
}

func TestLoader_GetRefetchesAfterTTL(t *testing.T) {
	f := &fakeSSM{values: map[string]string{"/jit-runners/runner-log-level": "debug"}}
	l := New(f, 50*time.Millisecond)
	ctx := context.Background()

	if _, err := l.Get(ctx, "/jit-runners/runner-log-level", "info"); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	f.values["/jit-runners/runner-log-level"] = "info"
	got, err := l.Get(ctx, "/jit-runners/runner-log-level", "info")
	if err != nil {
		t.Fatalf("post-TTL Get: %v", err)
	}
	if got != "info" {
		t.Errorf("post-TTL Get: got %q, want info", got)
	}
	if c := f.calls.Load(); c != 2 {
		t.Errorf("API calls = %d, want 2 (initial + refetch)", c)
	}
}

func TestLoader_GetMissingFallsBackToDefault(t *testing.T) {
	f := &fakeSSM{notFound: map[string]bool{"/missing": true}}
	l := New(f, time.Minute)
	ctx := context.Background()

	got, err := l.Get(ctx, "/missing", "fallback-value")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "fallback-value" {
		t.Errorf("got %q, want fallback-value", got)
	}
}

func TestLoader_GetAPIErrorFallsBackWhenNoCache(t *testing.T) {
	f := &fakeSSM{apiErr: errors.New("throttle")}
	l := New(f, time.Minute)

	got, err := l.Get(context.Background(), "/whatever", "default")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "default" {
		t.Errorf("got %q, want default", got)
	}
}

func TestLoader_GetAPIErrorReturnsLastCachedValue(t *testing.T) {
	f := &fakeSSM{values: map[string]string{"/x": "first"}}
	l := New(f, 50*time.Millisecond)
	ctx := context.Background()

	if _, err := l.Get(ctx, "/x", "default"); err != nil {
		t.Fatalf("warm-up Get: %v", err)
	}

	time.Sleep(80 * time.Millisecond)
	f.apiErr = errors.New("throttle")

	got, err := l.Get(ctx, "/x", "default")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "first" {
		t.Errorf("got %q, want first (last cached value)", got)
	}
}
