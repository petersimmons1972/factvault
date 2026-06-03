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
		feedsPath      string
		once           bool
		interval       time.Duration
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
	cmd.PersistentFlags().StringVar(&feedsPath, "feeds", "config/feeds.yaml", "RSS feed config file")
	cmd.PersistentFlags().BoolVar(&once, "once", false, "Run one RSS polling cycle and exit")
	cmd.PersistentFlags().DurationVar(&interval, "interval", 15*time.Minute, "Default RSS polling interval")

	addRun := func(name string, fn func(context.Context, *workers.SourcePipeline) error) {
		cmd.AddCommand(&cobra.Command{
			Use:   name,
			Short: "Run " + name + " worker once",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
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
		RunE: func(cmd *cobra.Command, _ []string) error {
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
		Use:   "rss",
		Short: "Poll RSS/Atom feeds and ingest source items",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dsn == "" {
				dsn = os.Getenv("FACTVAULT_DATABASE_URL")
			}
			if dsn == "" {
				return fmt.Errorf("database DSN required: set --dsn or FACTVAULT_DATABASE_URL")
			}
			pool, err := db.NewPool(cmd.Context(), dsn)
			if err != nil {
				return err
			}
			defer pool.Close()

			cfg, err := collectors.LoadFeedConfig(feedsPath)
			if err != nil {
				return err
			}
			if len(cfg.Feeds) == 0 {
				return fmt.Errorf("feeds config has no feeds")
			}

			p := &workers.SourcePipeline{DB: pool}
			schedules := buildRSSSchedules(cfg.Feeds, interval)
			runForFeeds := func(ctx context.Context, feedIdx []int) error {
				for _, i := range feedIdx {
					feed := cfg.Feeds[i]
					collector := collectors.RSSCollector{Spec: feed}
					if err := p.CollectOnce(ctx, feed.TenantID, collector); err != nil {
						return err
					}
				}
				return nil
			}

			if once {
				return runForFeeds(cmd.Context(), allScheduleIndexes(schedules))
			}

			lastPolled := map[int]time.Time{}
			for {
				now := time.Now().UTC()
				due := dueRSSFeedIndexes(schedules, lastPolled, now)
				if len(due) > 0 {
					if err := runForFeeds(cmd.Context(), due); err != nil {
						return err
					}
					for _, i := range due {
						lastPolled[i] = now
					}
				}
				wait := nextRSSPollWait(schedules, lastPolled, now)
				if wait <= 0 {
					wait = time.Second
				}
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(wait):
				}
			}
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "dossier",
		Short: "Precompute dossier bundles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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

type rssSchedule struct {
	feedIndex int
	interval  time.Duration
}

func buildRSSSchedules(feeds []collectors.FeedSpec, defaultInterval time.Duration) []rssSchedule {
	out := make([]rssSchedule, 0, len(feeds))
	for i, feed := range feeds {
		if strings.TrimSpace(feed.TenantID) == "" {
			continue
		}
		out = append(out, rssSchedule{feedIndex: i, interval: feed.PollInterval(defaultInterval)})
	}
	return out
}

func allScheduleIndexes(schedules []rssSchedule) []int {
	idx := make([]int, 0, len(schedules))
	for _, s := range schedules {
		idx = append(idx, s.feedIndex)
	}
	return idx
}

func dueRSSFeedIndexes(schedules []rssSchedule, lastPolled map[int]time.Time, now time.Time) []int {
	due := make([]int, 0, len(schedules))
	for _, s := range schedules {
		last, ok := lastPolled[s.feedIndex]
		if !ok || now.Sub(last) >= s.interval {
			due = append(due, s.feedIndex)
		}
	}
	return due
}

func nextRSSPollWait(schedules []rssSchedule, lastPolled map[int]time.Time, now time.Time) time.Duration {
	if len(schedules) == 0 {
		return time.Second
	}
	var minWait time.Duration = -1
	for _, s := range schedules {
		last, ok := lastPolled[s.feedIndex]
		if !ok {
			return 0
		}
		wait := max(s.interval-now.Sub(last), 0)
		if minWait < 0 || wait < minWait {
			minWait = wait
		}
	}
	return minWait
}
