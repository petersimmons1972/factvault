package collectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRSSCollector_CollectParsesItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel><title>Feed A</title><item><title>First</title><link>https://example.com/1</link><description>one</description><pubDate>Mon, 02 Jan 2006 15:04:05 MST</pubDate></item></channel></rss>`))
	}))
	defer srv.Close()

	c := RSSCollector{Spec: FeedSpec{URL: srv.URL, Topic: "intel", Tags: []string{"a", "b"}}, HTTPClient: srv.Client()}
	items, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items)=%d want 1", len(items))
	}
	if items[0].URL != "https://example.com/1" || items[0].Title != "First" || items[0].Publisher != "Feed A" {
		t.Fatalf("unexpected item: %+v", items[0])
	}
	if items[0].Topic != "intel" || len(items[0].Tags) != 2 {
		t.Fatalf("topic/tags not propagated: %+v", items[0])
	}
}

func TestRSSCollector_CollectRejectsInvalidFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not xml"))
	}))
	defer srv.Close()

	_, err := (RSSCollector{Spec: FeedSpec{URL: srv.URL}, HTTPClient: srv.Client()}).Collect(context.Background())
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadFeedConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "feeds.yaml")
	if err := os.WriteFile(path, []byte("feeds:\n  - url: https://example.com/rss\n    tenant: t\n    topic: x\n    tags: [a,b]\n    interval: 10m\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg, err := LoadFeedConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Feeds) != 1 || cfg.Feeds[0].Topic != "x" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}
