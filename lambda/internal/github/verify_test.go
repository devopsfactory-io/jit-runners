package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifyWebhookSignature(t *testing.T) {
	secret := "test-secret-123"
	body := []byte(`{"action":"queued"}`)

	validSig := computeSignature(body, secret)

	tests := []struct {
		name      string
		body      []byte
		signature string
		secret    string
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "valid signature",
			body:      body,
			signature: "sha256=" + validSig,
			secret:    secret,
		},
		{
			name:      "valid signature uppercase prefix",
			body:      body,
			signature: "SHA256=" + validSig,
			secret:    secret,
		},
		{
			name:      "missing signature header",
			body:      body,
			signature: "",
			secret:    secret,
			wantErr:   true,
			errMsg:    "missing X-Hub-Signature-256",
		},
		{
			name:      "empty secret",
			body:      body,
			signature: "sha256=" + validSig,
			secret:    "",
			wantErr:   true,
			errMsg:    "webhook secret is empty",
		},
		{
			name:      "wrong prefix",
			body:      body,
			signature: "sha1=abc123",
			secret:    secret,
			wantErr:   true,
			errMsg:    "signature must start with sha256=",
		},
		{
			name:      "signature mismatch",
			body:      body,
			signature: "sha256=0000000000000000000000000000000000000000000000000000000000000000",
			secret:    secret,
			wantErr:   true,
			errMsg:    "signature mismatch",
		},
		{
			name:      "wrong secret",
			body:      body,
			signature: "sha256=" + validSig,
			secret:    "wrong-secret",
			wantErr:   true,
			errMsg:    "signature mismatch",
		},
		{
			name:      "different body",
			body:      []byte(`{"action":"completed"}`),
			signature: "sha256=" + validSig,
			secret:    secret,
			wantErr:   true,
			errMsg:    "signature mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyWebhookSignature(tt.body, tt.signature, tt.secret)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if err.Error() != tt.errMsg {
					t.Errorf("error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func computeSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
