package main

import (
	"strings"
	"testing"

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
