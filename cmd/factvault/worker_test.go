package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/petersimmons1972/factvault/internal/collectors"
	"github.com/petersimmons1972/factvault/internal/workers"
)

func TestResolveLLMRuntimeConfig_PrefersFlagsOverEnv(t *testing.T) {
	t.Setenv("FACTVAULT_LLM_MODEL", "env-model")
	t.Setenv("FACTVAULT_LLM_BASE_URL", "https://env.example/v1")
	t.Setenv("FACTVAULT_LLM_URL", "https://legacy.example/v1")
	t.Setenv("FACTVAULT_LLM_API_KEY", "env-key")

	cfg, err := resolveLLMRuntimeConfig("openai", "flag-model", "https://flag.example/v1", "flag-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Model != "flag-model" {
		t.Fatalf("Model = %q, want flag-model", cfg.Model)
	}
	if cfg.BaseURL != "https://flag.example/v1" {
		t.Fatalf("BaseURL = %q, want https://flag.example/v1", cfg.BaseURL)
	}
	if cfg.APIKey != "flag-key" {
		t.Fatalf("APIKey = %q, want flag-key", cfg.APIKey)
	}
}

func TestResolveLLMRuntimeConfig_FallsBackToEnv(t *testing.T) {
	t.Setenv("FACTVAULT_LLM_MODEL", "env-model")
	t.Setenv("FACTVAULT_LLM_BASE_URL", "https://env.example/v1")
	t.Setenv("FACTVAULT_LLM_API_KEY", "env-key")

	cfg, err := resolveLLMRuntimeConfig("openai", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Model != "env-model" {
		t.Fatalf("Model = %q, want env-model", cfg.Model)
	}
	if cfg.BaseURL != "https://env.example/v1" {
		t.Fatalf("BaseURL = %q, want https://env.example/v1", cfg.BaseURL)
	}
	if cfg.APIKey != "env-key" {
		t.Fatalf("APIKey = %q, want env-key", cfg.APIKey)
	}
}

// TestResolveLLMRuntimeConfig_UsesLegacyLLMURLEnv verifies the C2 alias path.
// The primary FACTVAULT_LLM_BASE_URL must be absent (not set, not empty-string)
// so the resolver falls through to the deprecated FACTVAULT_LLM_URL alias.
func TestResolveLLMRuntimeConfig_UsesLegacyLLMURLEnv(t *testing.T) {
	// C1: os.LookupEnv("FACTVAULT_LLM_BASE_URL") == ("", false) lets the alias win.
	// t.Setenv("", "") would make it ("", true), suppressing the alias — wrong.
	os.Unsetenv("FACTVAULT_LLM_BASE_URL") //nolint:errcheck
	t.Setenv("FACTVAULT_LLM_URL", "http://localhost:11434/v1")

	cfg, err := resolveLLMRuntimeConfig("local", "llama3.1", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("BaseURL = %q, want legacy FACTVAULT_LLM_URL", cfg.BaseURL)
	}
}

func TestBuildLLMExtractor_UnsupportedProviderFromCLIConfig(t *testing.T) {
	cfg, err := resolveLLMRuntimeConfig("anthropic", "claude-3-5-sonnet", "", "")
	if err != nil {
		t.Fatalf("unexpected error from resolve: %v", err)
	}
	_, _, err = workers.BuildLLMExtractor(cfg)
	if err == nil {
		t.Fatal("expected unsupported provider error")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("error = %q, want unsupported message", err.Error())
	}
}

func TestRSSScheduleHelpers(t *testing.T) {
	feeds := []collectors.FeedSpec{
		{TenantID: "t1", Interval: "10m"},
		{TenantID: "", Interval: "5m"},
		{TenantID: "t2", Interval: "30m"},
	}
	schedules := buildRSSSchedules(feeds, 15*time.Minute)
	if len(schedules) != 2 {
		t.Fatalf("len(schedules)=%d want 2", len(schedules))
	}
	idx := allScheduleIndexes(schedules)
	if len(idx) != 2 || idx[0] != 0 || idx[1] != 2 {
		t.Fatalf("indexes=%v", idx)
	}

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	due := dueRSSFeedIndexes(schedules, map[int]time.Time{}, now)
	if len(due) != 2 {
		t.Fatalf("initial due=%v want both feeds", due)
	}

	last := map[int]time.Time{0: now, 2: now}
	if got := nextRSSPollWait(schedules, last, now); got != 10*time.Minute {
		t.Fatalf("next wait=%s want 10m", got)
	}
	due = dueRSSFeedIndexes(schedules, last, now.Add(10*time.Minute))
	if len(due) != 1 || due[0] != 0 {
		t.Fatalf("due at 10m=%v want only feed 0", due)
	}
	due = dueRSSFeedIndexes(schedules, last, now.Add(30*time.Minute))
	if len(due) != 2 {
		t.Fatalf("due at 30m=%v want both", due)
	}
}

func TestRSSOnceUsesAllScheduledFeeds(t *testing.T) {
	schedules := []rssSchedule{{feedIndex: 2, interval: time.Minute}, {feedIndex: 7, interval: 2 * time.Minute}}
	idx := allScheduleIndexes(schedules)
	if len(idx) != 2 || idx[0] != 2 || idx[1] != 7 {
		t.Fatalf("once indexes=%v", idx)
	}
}

// TestWorkerCmdHasEmbedSubcommand verifies the embed subcommand is registered.
func TestWorkerCmdHasEmbedSubcommand(t *testing.T) {
	cmd := newWorkerCmd()
	var found bool
	for _, sub := range cmd.Commands() {
		if sub.Use == "embed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("worker command does not have an 'embed' subcommand")
	}
}

// TestEffectiveRSSTenantPriorityChain verifies that effectiveRSSTenant implements the
// C4 resolution order: --tenant flag > feed.TenantID > FACTVAULT_DEV_TENANT_ID (issue #217).
func TestEffectiveRSSTenantPriorityChain(t *testing.T) {
	const flagVal = "flag-tenant-uuid"
	const feedVal = "feed-tenant-uuid"
	const devVal  = "dev-tenant-uuid"

	// --tenant flag beats feed TenantID and dev-tenant.
	if got, warn := effectiveRSSTenant(true, flagVal, feedVal, devVal); got != flagVal || warn {
		t.Errorf("flag override: got=%q warn=%v, want=%q warn=false", got, warn, flagVal)
	}

	// --tenant flag beats an empty feed TenantID and dev-tenant.
	if got, warn := effectiveRSSTenant(true, flagVal, "", devVal); got != flagVal || warn {
		t.Errorf("flag override (empty feed): got=%q warn=%v, want=%q warn=false", got, warn, flagVal)
	}

	// feed.TenantID beats dev-tenant when --tenant is not set.
	if got, warn := effectiveRSSTenant(false, "", feedVal, devVal); got != feedVal || warn {
		t.Errorf("feed tenant: got=%q warn=%v, want=%q warn=false", got, warn, feedVal)
	}

	// dev-tenant fallback when neither flag nor feed TenantID is set; warn=true signals caller.
	if got, warn := effectiveRSSTenant(false, "", "", devVal); got != devVal || !warn {
		t.Errorf("dev fallback: got=%q warn=%v, want=%q warn=true", got, warn, devVal)
	}

	// No tenant available → empty string, warn=false (caller converts to error).
	if got, warn := effectiveRSSTenant(false, "", "", ""); got != "" || warn {
		t.Errorf("no tenant: got=%q warn=%v, want empty warn=false", got, warn)
	}
}

// TestEmbedSubcommandHasEmbedderURLFlag verifies the --embedder-url flag is present.
func TestEmbedSubcommandHasEmbedderURLFlag(t *testing.T) {
	cmd := newWorkerCmd()
	for _, sub := range cmd.Commands() {
		if sub.Use == "embed" {
			f := sub.Flags().Lookup("embedder-url")
			if f == nil {
				t.Fatal("embed subcommand missing --embedder-url flag")
			}
			return
		}
	}
	t.Fatal("embed subcommand not found")
}
