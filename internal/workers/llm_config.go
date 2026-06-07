package workers

import (
	"fmt"
	"strings"

	"github.com/petersimmons1972/factvault/internal/extractors"
)

// LLMRuntimeConfig captures runtime settings for LLM extraction.
type LLMRuntimeConfig struct {
	Provider string
	Model    string
	BaseURL  string
	APIKey   string
}

// BuildLLMExtractor constructs an LLM extractor implementation from config.
func BuildLLMExtractor(cfg LLMRuntimeConfig) (LLMExtractor, string, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	model := strings.TrimSpace(cfg.Model)
	baseURL := strings.TrimSpace(cfg.BaseURL)
	apiKey := strings.TrimSpace(cfg.APIKey)

	hasConfig := provider != "" || model != "" || baseURL != "" || apiKey != ""
	if !hasConfig {
		return nil, "local", nil
	}
	if provider == "" {
		provider = "local"
	}

	switch provider {
	case "local", "ollama":
		if baseURL == "" {
			baseURL = "http://localhost:11434/v1"
		}
		if model == "" {
			return nil, provider, fmt.Errorf("llm model required for provider %q: set --llm-model or FACTVAULT_LLM_MODEL", provider)
		}
		return &extractors.LLMClient{
			BaseURL: baseURL,
			APIKey:  apiKey,
			Model:   model,
		}, provider, nil
	case "openai":
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		if model == "" {
			return nil, provider, fmt.Errorf("llm model required for provider %q: set --llm-model or FACTVAULT_LLM_MODEL", provider)
		}
		if apiKey == "" {
			return nil, provider, fmt.Errorf("llm api key required for provider %q: set --llm-api-key or FACTVAULT_LLM_API_KEY", provider)
		}
		return &extractors.LLMClient{
			BaseURL: baseURL,
			APIKey:  apiKey,
			Model:   model,
		}, provider, nil
	case "anthropic":
		return nil, provider, fmt.Errorf("llm provider %q is not supported yet", provider)
	default:
		return nil, provider, fmt.Errorf("unsupported llm provider %q (supported: local, openai)", provider)
	}
}
