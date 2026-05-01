package secretsmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type fakeSM struct {
	out *secretsmanager.GetSecretValueOutput
	err error
}

func (f *fakeSM) GetSecretValue(_ context.Context, _ *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	return f.out, f.err
}

func TestLoader_Load(t *testing.T) {
	tests := []struct {
		name    string
		out     *secretsmanager.GetSecretValueOutput
		err     error
		want    []byte
		wantErr error
	}{
		{
			name: "secret as string",
			out:  &secretsmanager.GetSecretValueOutput{SecretString: aws.String("hello")},
			want: []byte("hello"),
		},
		{
			name: "secret as binary",
			out:  &secretsmanager.GetSecretValueOutput{SecretBinary: []byte{0x01, 0x02, 0x03}},
			want: []byte{0x01, 0x02, 0x03},
		},
		{
			name:    "empty secret returns sentinel",
			out:     &secretsmanager.GetSecretValueOutput{},
			wantErr: ErrEmptySecret,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := New(&fakeSM{out: tt.out, err: tt.err})
			got, err := l.Load(context.Background(), "arn:aws:secret/test")
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != string(tt.want) {
				t.Errorf("got = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoader_EmptyName(t *testing.T) {
	l := New(&fakeSM{})
	if _, err := l.Load(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty name")
	}
}
