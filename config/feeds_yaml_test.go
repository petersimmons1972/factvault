package config_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"
)

var (
	readmeDevTenantRE      = regexp.MustCompile(`FACTVAULT_DEV_TENANT_ID='([0-9a-f-]{36})'`)
	sourcePipelineTenantRE = regexp.MustCompile(`const tenantID = "([0-9a-f-]{36})"`)
)

func TestFeedsYamlTenantMatchesDevTenant(t *testing.T) {
	data, err := os.ReadFile("feeds.yaml")
	if err != nil {
		t.Fatalf("read feeds.yaml: %v", err)
	}
	var cfg struct {
		Feeds []struct {
			Name   string `yaml:"name"`
			Tenant string `yaml:"tenant"`
		} `yaml:"feeds"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse feeds.yaml: %v", err)
	}
	if len(cfg.Feeds) == 0 {
		t.Fatal("feeds.yaml contains no feeds")
	}

	readmeTenant := extractTenant(t, filepath.Join("..", "README.md"), readmeDevTenantRE)
	fixtureTenant := extractTenant(t, filepath.Join("..", "internal", "workers", "source_pipeline_test.go"), sourcePipelineTenantRE)
	if readmeTenant != fixtureTenant {
		t.Fatalf("README dev tenant %q does not match worker fixture tenant %q", readmeTenant, fixtureTenant)
	}

	for _, feed := range cfg.Feeds {
		if feed.Tenant != fixtureTenant {
			t.Fatalf("feed %q tenant=%q want %q", feed.Name, feed.Tenant, fixtureTenant)
		}
	}
}

func extractTenant(t *testing.T, path string, re *regexp.Regexp) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	matches := re.FindSubmatch(data)
	if len(matches) != 2 {
		t.Fatalf("extract tenant from %s: no match for %q", path, re.String())
	}

	return string(matches[1])
}
