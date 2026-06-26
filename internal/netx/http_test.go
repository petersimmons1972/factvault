package netx

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
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

func TestValidateHTTPURLAllowPrivate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{name: "localhost allowed", rawURL: "http://localhost:8080", wantErr: false},
		{name: "private ip allowed", rawURL: "http://10.0.0.1/", wantErr: false},
		{name: "loopback allowed", rawURL: "http://127.0.0.1/", wantErr: false},
		{name: "unsupported scheme still blocked", rawURL: "file:///etc/passwd", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateHTTPURL(context.Background(), tc.rawURL, true)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q", tc.rawURL)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.rawURL, err)
			}
		})
	}
}

// TestNewSafeHTTPClient_HostnameResolvingToLoopbackIsBlocked verifies E-01:
// dial-time hostname resolution checks resolved IPs against the blocklist,
// preventing DNS-rebind attacks where a hostname resolves to a private address.
func TestNewSafeHTTPClient_HostnameResolvingToLoopbackIsBlocked(t *testing.T) {
	t.Parallel()
	client := NewSafeHTTPClient(5 * time.Second)
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	ctx := context.Background()
	// "localhost" resolves to 127.0.0.1 (loopback) — must be blocked at dial time.
	_, err := tr.DialContext(ctx, "tcp", "localhost:80")
	if err == nil {
		t.Fatal("expected blocked error for localhost resolving to loopback, got nil")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected 'blocked' in error, got: %v", err)
	}
}

// TestNewSafeHTTPClient_EnvProxyIsIgnored verifies E-02:
// the safe client ignores HTTP_PROXY/HTTPS_PROXY environment variables.
// Not parallel: t.Setenv is incompatible with t.Parallel.
func TestNewSafeHTTPClient_EnvProxyIsIgnored(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	client := NewSafeHTTPClient(5 * time.Second)
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	if tr.Proxy != nil {
		t.Fatal("expected Proxy to be nil on safe client, got non-nil")
	}
}
