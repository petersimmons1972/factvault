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
}

// Collector is the interface implemented by all source collectors.
type Collector interface {
	Name() string
	Collect(ctx context.Context) ([]Item, error)
}
