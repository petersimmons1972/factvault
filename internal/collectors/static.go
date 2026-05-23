package collectors

import "context"

type StaticCollector struct {
	CollectorName string
	Items         []Item
}

func (c StaticCollector) Name() string { return c.CollectorName }

func (c StaticCollector) Collect(context.Context) ([]Item, error) {
	return c.Items, nil
}
