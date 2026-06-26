package research

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSearchCollector_DefaultClientIsSafe(t *testing.T) {
	t.Parallel()
	sc := &SearchCollector{}
	client := sc.httpClient()
	tr, ok := client.Transport.(*http.Transport)
	if !ok || tr == nil {
		t.Fatal("expected *http.Transport")
	}
	ctx := context.Background()
	_, err := tr.DialContext(ctx, "tcp", "localhost:80")
	if err == nil {
		t.Fatal("expected blocked error for localhost, got nil")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected blocked in error, got: %v", err)
	}
}
