package secretmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/googleapis/gax-go/v2"

	smpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
)

type fakeSM struct {
	respPayload []byte
	respErr     error
	gotName     string
}

func (f *fakeSM) AccessSecretVersion(_ context.Context, req *smpb.AccessSecretVersionRequest, _ ...gax.CallOption) (*smpb.AccessSecretVersionResponse, error) {
	f.gotName = req.GetName()
	if f.respErr != nil {
		return nil, f.respErr
	}
	return &smpb.AccessSecretVersionResponse{
		Payload: &smpb.SecretPayload{Data: f.respPayload},
	}, nil
}

func TestLoader_Load_ReturnsSecretBytes(t *testing.T) {
	fake := &fakeSM{respPayload: []byte("super-secret")}
	loader := New(fake)
	got, err := loader.Load(context.Background(), "projects/p/secrets/s/versions/latest")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(got) != "super-secret" {
		t.Errorf("payload mismatch: %q", got)
	}
	if fake.gotName != "projects/p/secrets/s/versions/latest" {
		t.Errorf("name mismatch: %q", fake.gotName)
	}
}

func TestLoader_Load_EmptyNameReturnsError(t *testing.T) {
	loader := New(&fakeSM{})
	_, err := loader.Load(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestLoader_Load_PropagatesAPIError(t *testing.T) {
	fake := &fakeSM{respErr: errors.New("boom")}
	loader := New(fake)
	_, err := loader.Load(context.Background(), "projects/p/secrets/s/versions/latest")
	if err == nil {
		t.Fatal("expected propagated error")
	}
}
