package collectors

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRSSBodyLimitEnforced(t *testing.T) {
	oversizedDescription := strings.Repeat("a", 10*1024*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		if _, err := fmt.Fprintf(w, `<?xml version="1.0"?><rss><channel><title>Feed</title><item><title>First</title><link>https://example.com/1</link><description>%s</description></item></channel></rss>`, oversizedDescription); err != nil {
			http.Error(w, "write failed", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	_, err := (RSSCollector{Spec: FeedSpec{URL: srv.URL}, HTTPClient: srv.Client()}).Collect(context.Background())
	if err == nil {
		t.Fatal("expected oversized RSS body to fail")
	}
}
