package netx

import (
	"context"
	"testing"
)

func TestValidatePublicHTTPURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{name: "https public", rawURL: "https://example.com/a", wantErr: false},
		{name: "http public", rawURL: "http://example.com/a", wantErr: false},
		{name: "file scheme", rawURL: "file:///etc/passwd", wantErr: true},
		{name: "localhost", rawURL: "http://localhost:8080", wantErr: true},
		{name: "loopback", rawURL: "http://127.0.0.1/", wantErr: true},
		{name: "link local", rawURL: "http://169.254.169.254/latest/meta-data", wantErr: true},
		{name: "private", rawURL: "http://10.0.0.1/", wantErr: true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidatePublicHTTPURL(context.Background(), tc.rawURL)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q", tc.rawURL)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.rawURL, err)
			}
		})
	}
}
