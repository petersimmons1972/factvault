package collectors

import (
	"context"
	"time"
)

type Item struct {
	URL         string
	HTML        []byte
	Title       string
	Publisher   string
	PublishedAt *time.Time
	Topic       string
	Tags        []string
}

type Collector interface {
	Name() string
	Collect(ctx context.Context) ([]Item, error)
}
