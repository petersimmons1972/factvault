package main

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/petersimmons1972/factvault/internal/collectors"
	"github.com/petersimmons1972/factvault/internal/config"
)

// TestFeedsYamlTenantMatchesDevTenant asserts that every sample feed in
// config/feeds.yaml uses the dev-tenant UUID that matches the registry default
// for FACTVAULT_DEV_TENANT_ID. This prevents silent wrong-tenant ingestion when
// the sample config diverges from the dev default (issue #211).
func TestFeedsYamlTenantMatchesDevTenant(t *testing.T) {
	// Locate config/feeds.yaml relative to the repo root.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	feedsPath := filepath.Join(repoRoot, "config", "feeds.yaml")

	cfg, err := collectors.LoadFeedConfig(feedsPath)
	if err != nil {
		t.Fatalf("LoadFeedConfig(%q): %v", feedsPath, err)
	}
	if len(cfg.Feeds) == 0 {
		t.Fatal("feeds.yaml has no feeds; add at least one sample feed")
	}

	// Locate the dev-tenant default from the registry (single source of truth).
	var devTenantDefault string
	for _, e := range config.Registry {
		if e.EnvVar == "FACTVAULT_DEV_TENANT_ID" {
			devTenantDefault = e.Default
			break
		}
	}
	if devTenantDefault == "" {
		t.Fatal("FACTVAULT_DEV_TENANT_ID not found in config.Registry; registry may be out of date")
	}

	for _, feed := range cfg.Feeds {
		if feed.TenantID != devTenantDefault {
			t.Errorf("feed %q: tenant=%q want=%q (registry dev default for FACTVAULT_DEV_TENANT_ID)\n"+
				"hint: update config/feeds.yaml to use the dev-tenant UUID so sample and dev configs stay in sync",
				feed.Name, feed.TenantID, devTenantDefault)
		}
	}
}
