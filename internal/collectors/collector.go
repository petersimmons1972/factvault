package collectors

import "context"

type Item struct {
	URL  string
	HTML []byte
}

type Collector interface {
	Name() string
	Collect(ctx context.Context) ([]Item, error)
}
