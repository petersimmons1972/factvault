package collectors

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type FeedConfig struct {
	Feeds []FeedSpec `yaml:"feeds"`
}

type FeedSpec struct {
	URL      string   `yaml:"url"`
	TenantID string   `yaml:"tenant"`
	Topic    string   `yaml:"topic"`
	Tags     []string `yaml:"tags"`
	Interval string   `yaml:"interval"`
	Name     string   `yaml:"name"`
}

func LoadFeedConfig(path string) (FeedConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FeedConfig{}, err
	}
	var cfg FeedConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return FeedConfig{}, fmt.Errorf("parse feeds config: %w", err)
	}
	return cfg, nil
}

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
