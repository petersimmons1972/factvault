package collectors

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/petersimmons1972/factvault/internal/config"
	"github.com/petersimmons1972/factvault/internal/netx"
)

const defaultMaxFeedBytes = 10 * 1024 * 1024

// RSSCollector fetches and parses an RSS or Atom feed into Items.
type RSSCollector struct {
	Spec       FeedSpec
	HTTPClient *http.Client
}

// Name returns the feed's configured name, or "rss" if none is set.
func (c RSSCollector) Name() string {
	if c.Spec.Name != "" {
		return c.Spec.Name
	}
	return "rss"
}

// Collect fetches the configured feed URL and returns parsed Items.
func (c RSSCollector) Collect(ctx context.Context) ([]Item, error) {
	if c.Spec.URL == "" {
		return nil, fmt.Errorf("rss collector: feed url required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Spec.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close on response body
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("rss collector: status %d", resp.StatusCode)
	}
	maxFeedBytes := maxFeedBytes()
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxFeedBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxFeedBytes {
		return nil, fmt.Errorf("rss collector: response body exceeds %d bytes", maxFeedBytes)
	}
	feed, err := gofeed.NewParser().ParseString(string(body))
	if err != nil {
		return nil, fmt.Errorf("rss collector: parse feed: %w", err)
	}
	items := make([]Item, 0, len(feed.Items))
	for _, entry := range feed.Items {
		if entry == nil || entry.Link == "" {
			continue
		}
		item := Item{
			URL:       entry.Link,
			HTML:      []byte(entry.Description),
			Title:     entry.Title,
			Publisher: feed.Title,
			Topic:     c.Spec.Topic,
			Tags:      append([]string(nil), c.Spec.Tags...),
		}
		if entry.Content != "" {
			item.HTML = []byte(entry.Content)
		}
		if len(item.HTML) == 0 {
			item.HTML = []byte(entry.Title)
		}
		if entry.PublishedParsed != nil {
			t := entry.PublishedParsed.UTC()
			item.PublishedAt = &t
		}
		items = append(items, item)
	}
	return items, nil
}

func (c RSSCollector) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return netx.NewSafeHTTPClient(20 * time.Second)
}

func maxFeedBytes() int {
	size, err := config.ResolveInt(nil, "FACTVAULT_MAX_RSS_BYTES", defaultMaxFeedBytes, false)
	if err != nil || size <= 0 {
		return defaultMaxFeedBytes
	}
	return size
}
