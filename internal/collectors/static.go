package collectors

import "context"

// StaticCollector is a Collector that returns a fixed list of Items; useful for testing.
type StaticCollector struct {
	CollectorName string
	Items         []Item
}

// Name returns the collector's configured name.
func (c StaticCollector) Name() string { return c.CollectorName }

// Collect returns the static item list regardless of context.
func (c StaticCollector) Collect(context.Context) ([]Item, error) {
	return c.Items, nil
}
