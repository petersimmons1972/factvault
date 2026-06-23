package main

import (
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

	cfg := resolveLLMRuntimeConfig("openai", "flag-model", "https://flag.example/v1", "flag-key")
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

	cfg := resolveLLMRuntimeConfig("openai", "", "", "")
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

func TestResolveLLMRuntimeConfig_UsesLegacyLLMURLEnv(t *testing.T) {
	t.Setenv("FACTVAULT_LLM_BASE_URL", "")
	t.Setenv("FACTVAULT_LLM_URL", "http://localhost:11434/v1")

	cfg := resolveLLMRuntimeConfig("local", "llama3.1", "", "")
	if cfg.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("BaseURL = %q, want legacy FACTVAULT_LLM_URL", cfg.BaseURL)
	}
}

func TestBuildLLMExtractor_UnsupportedProviderFromCLIConfig(t *testing.T) {
	cfg := resolveLLMRuntimeConfig("anthropic", "claude-3-5-sonnet", "", "")
	_, _, err := workers.BuildLLMExtractor(cfg)
	if err == nil {
		t.Fatal("expected unsupported provider error")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("error = %q, want unsupported message", err.Error())
	}
}

func TestFirstNonEmpty(t *testing.T) {
	got := firstNonEmpty("", "  ", "value")
	if got != "value" {
		t.Fatalf("firstNonEmpty() = %q, want value", got)
	}
}

func TestWorkerCollectRegistersSearXNGURLFlag(t *testing.T) {
	collectCmd := mustSubcommand(t, newWorkerCmd(), "collect")
	flag := collectCmd.Flags().Lookup("searxng-url")
	if flag == nil {
		t.Fatal("expected --searxng-url flag to be registered")
	}
	if flag.DefValue != "" {
		t.Fatalf("default flag value = %q, want empty string", flag.DefValue)
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
