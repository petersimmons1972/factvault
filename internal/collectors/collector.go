// Package collectors provides interfaces and implementations for ingesting
// content from external sources into the factvault pipeline.
package collectors

import (
	"context"
	"time"
)

// Item represents a single piece of content collected from an external source.
type Item struct {
	URL         string
	HTML        []byte
	Title       string
	Publisher   string
	PublishedAt *time.Time
	Topic       string
	Tags        []string
	// Meta holds arbitrary key-value metadata for this item. CollectOnce writes
	// this into the sources.meta JSONB column. SearchCollector sets
	// Meta: {"trust_tier": "web"} on every web-sourced item.
	Meta map[string]any
}

// Collector is the interface implemented by all source collectors.
type Collector interface {
	Name() string
	Collect(ctx context.Context) ([]Item, error)
}
