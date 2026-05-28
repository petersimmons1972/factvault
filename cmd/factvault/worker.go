package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/collectors"
	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/vocabulary"
	"github.com/petersimmons1972/factvault/internal/workers"
)

func newWorkerCmd() *cobra.Command {
	var (
		dsn            string
		tenantID       string
		limit          int
		ageDays        int
		vocabularyMode string
		llmProvider    string
		llmModel       string
		llmBaseURL     string
		llmAPIKey      string
		confirmCost    bool
		costThreshold  int
	)

	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Run source-pipeline workers",
	}
	cmd.PersistentFlags().StringVar(&dsn, "dsn", "", "Postgres DSN (or FACTVAULT_DATABASE_URL)")
	cmd.PersistentFlags().StringVar(&tenantID, "tenant", "", "Tenant UUID")
	cmd.PersistentFlags().IntVar(&limit, "limit", 100, "Maximum rows per run")
	cmd.PersistentFlags().IntVar(&ageDays, "age-days", 7, "Verify threshold in days")
	cmd.PersistentFlags().StringVar(&vocabularyMode, "vocabulary-mode", string(vocabulary.ModeStrict), "Property vocabulary mode: strict or permissive")
	cmd.PersistentFlags().StringVar(&llmProvider, "llm-provider", "", "LLM provider for extraction: local or openai")
	cmd.PersistentFlags().StringVar(&llmModel, "llm-model", "", "LLM model for extraction")
	cmd.PersistentFlags().StringVar(&llmBaseURL, "llm-base-url", "", "LLM base URL")
	cmd.PersistentFlags().StringVar(&llmAPIKey, "llm-api-key", "", "LLM API key")
	cmd.PersistentFlags().BoolVar(&confirmCost, "confirm-cost", false, "Confirm frontier-model extraction batches above the guardrail threshold")
	cmd.PersistentFlags().IntVar(&costThreshold, "llm-cost-guardrail-threshold", 1000, "Frontier-model extraction batch guardrail threshold")

	addRun := func(name string, fn func(context.Context, *workers.SourcePipeline) error) {
		cmd.AddCommand(&cobra.Command{
			Use:   name,
			Short: "Run " + name + " worker once",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if dsn == "" {
					dsn = os.Getenv("FACTVAULT_DATABASE_URL")
				}
				if dsn == "" {
					return fmt.Errorf("database DSN required: set --dsn or FACTVAULT_DATABASE_URL")
				}
				if tenantID == "" {
					return fmt.Errorf("tenant required: set --tenant")
				}
				pool, err := db.NewPool(cmd.Context(), dsn)
				if err != nil {
					return err
				}
				defer pool.Close()
				p := &workers.SourcePipeline{DB: pool}
				return fn(cmd.Context(), p)
			},
		})
	}

	addRun("collect", func(ctx context.Context, p *workers.SourcePipeline) error {
		// TODO(#94): seed URL is not yet configurable; collector uses a static stub.
		seed := collectors.StaticCollector{
			CollectorName: "seed",
			Items: []collectors.Item{
				{URL: "https://example.com/factvault-seed", HTML: []byte("<html><body>seed</body></html>")},
			},
		}
		return p.CollectOnce(ctx, tenantID, seed)
	})
	addRun("archive", func(ctx context.Context, p *workers.SourcePipeline) error {
		return p.ArchiveOnce(ctx, tenantID, limit)
	})
	addRun("extract", func(ctx context.Context, p *workers.SourcePipeline) error {
		extractor, provider, err := workers.BuildLLMExtractor(resolveLLMRuntimeConfig(llmProvider, llmModel, llmBaseURL, llmAPIKey))
		if err != nil {
			return err
		}
		factPipeline := &workers.FactPipeline{
			DB:                     p.DB,
			VocabularyMode:         vocabulary.Mode(vocabularyMode),
			LLM:                    extractor,
			LLMProvider:            provider,
			ConfirmCost:            confirmCost,
			CostGuardrailThreshold: costThreshold,
		}
		return factPipeline.ExtractOnce(ctx, tenantID, limit)
	})
	addRun("verify", func(ctx context.Context, p *workers.SourcePipeline) error {
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		return p.VerifyOnce(ctx, tenantID, ageDays, limit)
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "corroborate",
		Short: "Run corroborate worker once",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dsn == "" {
				dsn = os.Getenv("FACTVAULT_DATABASE_URL")
			}
			if dsn == "" {
				return fmt.Errorf("database DSN required: set --dsn or FACTVAULT_DATABASE_URL")
			}
			if tenantID == "" {
				return fmt.Errorf("tenant required: set --tenant")
			}
			pool, err := db.NewPool(cmd.Context(), dsn)
			if err != nil {
				return err
			}
			defer pool.Close()
			return (&workers.Corroborator{DB: pool}).CorroborateOnce(cmd.Context(), tenantID)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "dossier",
		Short: "Precompute dossier bundles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dsn == "" {
				dsn = os.Getenv("FACTVAULT_DATABASE_URL")
			}
			if dsn == "" {
				return fmt.Errorf("database DSN required: set --dsn or FACTVAULT_DATABASE_URL")
			}
			if tenantID == "" {
				return fmt.Errorf("tenant required: set --tenant")
			}
			pool, err := db.NewPool(cmd.Context(), dsn)
			if err != nil {
				return err
			}
			defer pool.Close()
			_, err = (workers.DossierWorker{DB: pool}).RunOnce(cmd.Context(), workers.DossierOptions{TenantID: tenantID, Limit: limit})
			return err
		},
	})
	return cmd
}

func resolveLLMRuntimeConfig(provider, model, baseURL, apiKey string) workers.LLMRuntimeConfig {
	return workers.LLMRuntimeConfig{
		Provider: provider,
		Model:    firstNonEmpty(model, os.Getenv("FACTVAULT_LLM_MODEL")),
		BaseURL:  firstNonEmpty(baseURL, os.Getenv("FACTVAULT_LLM_BASE_URL"), os.Getenv("FACTVAULT_LLM_URL")),
		APIKey:   firstNonEmpty(apiKey, os.Getenv("FACTVAULT_LLM_API_KEY")),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
