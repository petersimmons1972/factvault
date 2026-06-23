package config_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"
)

type feedConfig struct {
	Feeds []feedSpec `yaml:"feeds"`
}

type feedSpec struct {
	Name   string `yaml:"name"`
	Tenant string `yaml:"tenant"`
}

func TestFeedsYamlTenantMatchesDevTenant(t *testing.T) {
	configPath := mustPath(t, "feeds.yaml")
	composePath := mustPath(t, "..", "docker-compose.yml")
	readmePath := mustPath(t, "..", "README.md")

	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read feeds.yaml: %v", err)
	}

	var cfg feedConfig
	if err := yaml.Unmarshal(configData, &cfg); err != nil {
		t.Fatalf("parse feeds.yaml: %v", err)
	}
	if len(cfg.Feeds) == 0 {
		t.Fatal("feeds.yaml must contain at least one sample feed")
	}

	devTenantFromCompose := mustExtract(t, composePath, `FACTVAULT_DEV_TENANT_ID:-([0-9a-f-]{36})`)
	devTenantFromReadme := mustExtract(t, readmePath, `export FACTVAULT_DEV_TENANT_ID='([0-9a-f-]{36})'`)

	if devTenantFromCompose != devTenantFromReadme {
		t.Fatalf("dev tenant drift: docker-compose=%q README=%q", devTenantFromCompose, devTenantFromReadme)
	}

	for _, feed := range cfg.Feeds {
		if feed.Tenant != devTenantFromCompose {
			t.Fatalf("feed %q tenant=%q want %q", feed.Name, feed.Tenant, devTenantFromCompose)
		}
	}
}

func mustPath(t *testing.T, elems ...string) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	base := filepath.Dir(file)
	parts := append([]string{base}, elems...)
	return filepath.Join(parts...)
}

func mustExtract(t *testing.T, path, pattern string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}

	matches := regexp.MustCompile(pattern).FindStringSubmatch(string(data))
	if len(matches) != 2 {
		t.Fatalf("extract tenant from %s with %q", filepath.Base(path), pattern)
	}

	return matches[1]
}
