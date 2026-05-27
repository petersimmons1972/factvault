package workers

import (
	"testing"

	"github.com/petersimmons1972/factvault/internal/extractors"
)

func TestBuildLLMExtractor_NoConfigDisablesLLM(t *testing.T) {
	t.Parallel()

	extractor, provider, err := BuildLLMExtractor(LLMRuntimeConfig{})
	if err != nil {
		t.Fatalf("BuildLLMExtractor() error = %v", err)
	}
	if extractor != nil {
		t.Fatal("expected nil extractor when llm config is empty")
	}
	if provider != "local" {
		t.Fatalf("provider = %q, want local", provider)
	}
}

func TestBuildLLMExtractor_LocalRequiresModel(t *testing.T) {
	t.Parallel()

	_, _, err := BuildLLMExtractor(LLMRuntimeConfig{
		Provider: "local",
		BaseURL:  "http://localhost:11434/v1",
	})
	if err == nil {
		t.Fatal("expected error for missing model")
	}
}

func TestBuildLLMExtractor_LocalBuildsClient(t *testing.T) {
	t.Parallel()

	extractor, provider, err := BuildLLMExtractor(LLMRuntimeConfig{
		Provider: "local",
		Model:    "llama3.1:8b",
	})
	if err != nil {
		t.Fatalf("BuildLLMExtractor() error = %v", err)
	}
	if provider != "local" {
		t.Fatalf("provider = %q, want local", provider)
	}
	client, ok := extractor.(*extractors.LLMClient)
	if !ok {
		t.Fatalf("extractor type = %T, want *extractors.LLMClient", extractor)
	}
	if client.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("BaseURL = %q, want default local base url", client.BaseURL)
	}
}

func TestBuildLLMExtractor_OpenAIRequiresAPIKey(t *testing.T) {
	t.Parallel()

	_, _, err := BuildLLMExtractor(LLMRuntimeConfig{
		Provider: "openai",
		Model:    "gpt-4o-mini",
	})
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestBuildLLMExtractor_AnthropicFailsFast(t *testing.T) {
	t.Parallel()

	_, provider, err := BuildLLMExtractor(LLMRuntimeConfig{
		Provider: "anthropic",
		Model:    "claude-sonnet",
	})
	if provider != "anthropic" {
		t.Fatalf("provider = %q, want anthropic", provider)
	}
	if err == nil {
		t.Fatal("expected unsupported provider error")
	}
}
