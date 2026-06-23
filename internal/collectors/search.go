package collectors

import (
	"context"
	"os"
	"strings"
)

const (
	// SearXNGURLEnvVar is the env var used to configure the search collector base URL.
	SearXNGURLEnvVar = "FACTVAULT_SEARXNG_URL"
)

// SearchCollector carries SearXNG runtime configuration for the search collector.
type SearchCollector struct {
	baseURL string
}

// NewSearchCollector builds a SearchCollector from the configured SearXNG base URL.
func NewSearchCollector(searxngURL string) SearchCollector {
	return SearchCollector{baseURL: normalizeSearchBaseURL(searxngURL)}
}

// Name returns the collector identifier.
func (c SearchCollector) Name() string { return "search" }

// Collect is a placeholder until search query ingestion is implemented.
func (c SearchCollector) Collect(context.Context) ([]Item, error) {
	return nil, nil
}

// SearchURL returns the SearXNG search endpoint URL.
func (c SearchCollector) SearchURL() string {
	if c.baseURL == "" {
		return ""
	}
	return c.baseURL + "/search"
}

// ResolveSearchCollectorURL returns the configured SearXNG URL for the search collector.
func ResolveSearchCollectorURL(flagValue string) string {
	return firstNonEmptyTrimmed(flagValue, os.Getenv(SearXNGURLEnvVar))
}

func normalizeSearchBaseURL(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
