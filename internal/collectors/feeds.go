package collectors

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// FeedConfig is the top-level structure parsed from a feeds YAML configuration file.
type FeedConfig struct {
	Feeds []FeedSpec `yaml:"feeds"`
}

// FeedSpec describes a single RSS/Atom feed source and its polling parameters.
type FeedSpec struct {
	URL      string   `yaml:"url"`
	TenantID string   `yaml:"tenant"`
	Topic    string   `yaml:"topic"`
	Tags     []string `yaml:"tags"`
	Interval string   `yaml:"interval"`
	Name     string   `yaml:"name"`
}

// LoadFeedConfig reads and parses a YAML feed configuration from path.
// The caller is responsible for validating that path comes from a trusted source.
func LoadFeedConfig(path string) (FeedConfig, error) { //nolint:gosec // path comes from operator config, not user input
	data, err := os.ReadFile(path) //nolint:gosec // controlled by operator config
	if err != nil {
		return FeedConfig{}, err
	}
	var cfg FeedConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return FeedConfig{}, fmt.Errorf("parse feeds config: %w", err)
	}
	return cfg, nil
}

// PollInterval returns the feed's configured polling interval, or defaultInterval if
// the feed has no interval configured or the value is unparseable.
func (f FeedSpec) PollInterval(defaultInterval time.Duration) time.Duration {
	if f.Interval == "" {
		return defaultInterval
	}
	d, err := time.ParseDuration(f.Interval)
	if err != nil || d <= 0 {
		return defaultInterval
	}
	return d
}
