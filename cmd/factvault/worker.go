package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/collectors"
	"github.com/petersimmons1972/factvault/internal/config"
	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/embed"
	"github.com/petersimmons1972/factvault/internal/research"
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
	cmd.PersistentFlags().StringVar(&tenantID, "tenant", "", "Tenant UUID (or FACTVAULT_DEV_TENANT_ID)")
	cmd.PersistentFlags().IntVar(&limit, "limit", 100, "Maximum rows per run (or FACTVAULT_WORKER_LIMIT)")
	cmd.PersistentFlags().IntVar(&ageDays, "age-days", 7, "Verify threshold in days (or FACTVAULT_VERIFY_AGE_DAYS)")
	cmd.PersistentFlags().StringVar(&vocabularyMode, "vocabulary-mode", string(vocabulary.ModeStrict), "Property vocabulary mode: strict or permissive")
	cmd.PersistentFlags().StringVar(&llmProvider, "llm-provider", "", "LLM provider for extraction: local or openai")
	cmd.PersistentFlags().StringVar(&llmModel, "llm-model", "", "LLM model for extraction (or FACTVAULT_LLM_MODEL)")
	cmd.PersistentFlags().StringVar(&llmBaseURL, "llm-base-url", "", "LLM base URL (or FACTVAULT_LLM_BASE_URL)")
	cmd.PersistentFlags().BoolVar(&confirmCost, "confirm-cost", false, "Confirm frontier-model extraction batches above the guardrail threshold")
	cmd.PersistentFlags().IntVar(&costThreshold, "llm-cost-guardrail-threshold", 1000, "Frontier-model extraction batch guardrail threshold in paid extractions per run")
	cmd.PersistentFlags().StringVar(&feedsPath, "feeds", "config/feeds.yaml", "RSS feed config file (or FACTVAULT_FEEDS_PATH)")
	cmd.PersistentFlags().BoolVar(&once, "once", false, "Run one RSS polling cycle and exit")
	cmd.PersistentFlags().DurationVar(&interval, "interval", 15*time.Minute, "Default RSS polling interval")

	// addRun registers a simple "run once" subcommand that opens a DB pool and
	// runs fn. C1/C4/C5: resolvers replace manual if-empty chains.
	addRun := func(name string, fn func(context.Context, *workers.SourcePipeline) error) {
		cmd.AddCommand(&cobra.Command{
			Use:   name,
			Short: "Run " + name + " worker once",
			Args:  cobra.NoArgs,
			RunE: func(subcmd *cobra.Command, _ []string) error {
				var err error
				// C1/C5: flag.Changed > env > required error.
				dsn, err = config.ResolveSecret(subcmd.Flags().Lookup("dsn"), "FACTVAULT_DATABASE_URL", "", true)
				if err != nil {
					return err
				}
				if f := subcmd.Flags().Lookup("dsn"); f != nil && f.Changed {
					if err := config.ValidateDSNNoPassword(dsn); err != nil {
						return err
					}
				}
				// C4: --tenant > FACTVAULT_DEV_TENANT_ID > ERROR; warn when env provides the value.
				tenantID, err = config.ResolveString(subcmd.Flags().Lookup("tenant"), "FACTVAULT_DEV_TENANT_ID", "", true)
				if err != nil {
					return err
				}
				if tf := subcmd.Flags().Lookup("tenant"); tf == nil || !tf.Changed {
					fmt.Fprintln(os.Stderr, "warning: using dev tenant from env (FACTVAULT_DEV_TENANT_ID); pass --tenant for production use")
				}
				// C5: FACTVAULT_WORKER_LIMIT wired.
				limit, err = config.ResolveInt(subcmd.Flags().Lookup("limit"), "FACTVAULT_WORKER_LIMIT", 100, false)
				if err != nil {
					return err
				}
				// C5: FACTVAULT_VERIFY_AGE_DAYS wired.
				ageDays, err = config.ResolveInt(subcmd.Flags().Lookup("age-days"), "FACTVAULT_VERIFY_AGE_DAYS", 7, false)
				if err != nil {
					return err
				}
				pool, err := db.NewPool(subcmd.Context(), dsn)
				if err != nil {
					return err
				}
				defer pool.Close()
				p := &workers.SourcePipeline{DB: pool}
				return fn(subcmd.Context(), p)
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
		rtCfg, err := resolveLLMRuntimeConfig(llmProvider, llmModel, llmBaseURL)
		if err != nil {
			return err
		}
		extractor, provider, err := workers.BuildLLMExtractor(rtCfg)
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
		RunE: func(subcmd *cobra.Command, _ []string) error {
			resolvedDSN, err := config.ResolveSecret(subcmd.Flags().Lookup("dsn"), "FACTVAULT_DATABASE_URL", "", true)
			if err != nil {
				return err
			}
			if f := subcmd.Flags().Lookup("dsn"); f != nil && f.Changed {
				if err := config.ValidateDSNNoPassword(resolvedDSN); err != nil {
					return err
				}
			}
			resolvedTenant, err := config.ResolveString(subcmd.Flags().Lookup("tenant"), "FACTVAULT_DEV_TENANT_ID", "", true)
			if err != nil {
				return err
			}
			if tf := subcmd.Flags().Lookup("tenant"); tf == nil || !tf.Changed {
				fmt.Fprintln(os.Stderr, "warning: using dev tenant from env (FACTVAULT_DEV_TENANT_ID); pass --tenant for production use")
			}
			pool, err := db.NewPool(subcmd.Context(), resolvedDSN)
			if err != nil {
				return err
			}
			defer pool.Close()
			return (&workers.Corroborator{DB: pool}).CorroborateOnce(subcmd.Context(), resolvedTenant)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "rss",
		Short: "Poll RSS/Atom feeds and ingest source items",
		Args:  cobra.NoArgs,
		RunE: func(subcmd *cobra.Command, _ []string) error {
			resolvedDSN, err := config.ResolveSecret(subcmd.Flags().Lookup("dsn"), "FACTVAULT_DATABASE_URL", "", true)
			if err != nil {
				return err
			}
			// C5: FACTVAULT_FEEDS_PATH wired.
			resolvedFeeds, err := config.ResolveString(subcmd.Flags().Lookup("feeds"), "FACTVAULT_FEEDS_PATH", "config/feeds.yaml", false)
			if err != nil {
				return err
			}
			pool, err := db.NewPool(subcmd.Context(), resolvedDSN)
			if err != nil {
				return err
			}
			defer pool.Close()

			cfg, err := collectors.LoadFeedConfig(resolvedFeeds)
			if err != nil {
				return err
			}
			if len(cfg.Feeds) == 0 {
				return fmt.Errorf("feeds config has no feeds")
			}

			// C4: resolve --tenant flag for optional global override.
			// Priority per feed: --tenant (if set) > feed.TenantID > FACTVAULT_DEV_TENANT_ID.
			tenantFlag := subcmd.Flags().Lookup("tenant")
			flagTenantChanged := tenantFlag != nil && tenantFlag.Changed
			var flagTenantValue string
			if flagTenantChanged {
				flagTenantValue = tenantFlag.Value.String()
			}
			// Pre-resolve dev-tenant fallback for feeds that lack their own TenantID.
			devTenantValue := os.Getenv("FACTVAULT_DEV_TENANT_ID")

			p := &workers.SourcePipeline{DB: pool}
			schedules := buildRSSSchedules(cfg.Feeds, interval)
			runForFeeds := func(ctx context.Context, feedIdx []int) error {
				for _, i := range feedIdx {
					feed := cfg.Feeds[i]
					tenant, usedDev := effectiveRSSTenant(flagTenantChanged, flagTenantValue, feed.TenantID, devTenantValue)
					if tenant == "" {
						return fmt.Errorf("feed %q: no tenant configured; pass --tenant or set FACTVAULT_DEV_TENANT_ID", feed.Name)
					}
					if usedDev {
						fmt.Fprintf(os.Stderr, "warning: feed %q using dev tenant from env (FACTVAULT_DEV_TENANT_ID); pass --tenant for production use\n", feed.Name)
					}
					collector := collectors.RSSCollector{Spec: feed}
					if err := p.CollectOnce(ctx, tenant, collector); err != nil {
						return err
					}
				}
				return nil
			}

			if once {
				return runForFeeds(subcmd.Context(), allScheduleIndexes(schedules))
			}

			lastPolled := map[int]time.Time{}
			for {
				now := time.Now().UTC()
				due := dueRSSFeedIndexes(schedules, lastPolled, now)
				if len(due) > 0 {
					if err := runForFeeds(subcmd.Context(), due); err != nil {
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
				case <-subcmd.Context().Done():
					return subcmd.Context().Err()
				case <-time.After(wait):
				}
			}
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "dossier",
		Short: "Precompute dossier bundles",
		Args:  cobra.NoArgs,
		RunE: func(subcmd *cobra.Command, _ []string) error {
			resolvedDSN, err := config.ResolveSecret(subcmd.Flags().Lookup("dsn"), "FACTVAULT_DATABASE_URL", "", true)
			if err != nil {
				return err
			}
			resolvedTenant, err := config.ResolveString(subcmd.Flags().Lookup("tenant"), "FACTVAULT_DEV_TENANT_ID", "", true)
			if err != nil {
				return err
			}
			if tf := subcmd.Flags().Lookup("tenant"); tf == nil || !tf.Changed {
				fmt.Fprintln(os.Stderr, "warning: using dev tenant from env (FACTVAULT_DEV_TENANT_ID); pass --tenant for production use")
			}
			resolvedLimit, err := config.ResolveInt(subcmd.Flags().Lookup("limit"), "FACTVAULT_WORKER_LIMIT", 100, false)
			if err != nil {
				return err
			}
			pool, err := db.NewPool(subcmd.Context(), resolvedDSN)
			if err != nil {
				return err
			}
			defer pool.Close()
			_, err = (workers.DossierWorker{DB: pool}).RunOnce(subcmd.Context(), workers.DossierOptions{TenantID: resolvedTenant, Limit: resolvedLimit})
			return err
		},
	})

	// research subcommand: generate queries -> fetch candidate URLs -> CollectOnce.
	researchCmd := &cobra.Command{
		Use:   "research <entity>",
		Short: "Actively research an entity: generate queries -> web search -> collect URLs",
		Args:  cobra.ExactArgs(1),
		RunE: func(subcmd *cobra.Command, args []string) error {
			resolvedDSN, err := config.ResolveSecret(subcmd.Flags().Lookup("dsn"), "FACTVAULT_DATABASE_URL", "", true)
			if err != nil {
				return err
			}
			resolvedTenant, err := config.ResolveString(subcmd.Flags().Lookup("tenant"), "FACTVAULT_DEV_TENANT_ID", "", true)
			if err != nil {
				return err
			}
			if tf := subcmd.Flags().Lookup("tenant"); tf == nil || !tf.Changed {
				fmt.Fprintln(os.Stderr, "warning: using dev tenant from env (FACTVAULT_DEV_TENANT_ID); pass --tenant for production use")
			}

			perspectives, err := subcmd.Flags().GetInt("perspectives")
			if err != nil {
				return err
			}
			questionsPerPerspective, err := subcmd.Flags().GetInt("questions-per")
			if err != nil {
				return err
			}
			resultsPerQuery, err := subcmd.Flags().GetInt("results-per-query")
			if err != nil {
				return err
			}
			maxFetches, err := subcmd.Flags().GetInt("max-fetches")
			if err != nil {
				return err
			}
			entityType, err := subcmd.Flags().GetString("entity-type")
			if err != nil {
				return err
			}

			// C5: FACTVAULT_SEARXNG_URL wired.
			searxngURL, err := config.ResolveString(subcmd.Flags().Lookup("searxng-url"), "FACTVAULT_SEARXNG_URL", "https://searxng.example.com", false)
			if err != nil {
				return err
			}

			rtCfg, err := resolveLLMRuntimeConfig("", llmModel, llmBaseURL)
			if err != nil {
				return err
			}
			llmCfg := research.LLMConfig{
				BaseURL: rtCfg.BaseURL,
				APIKey:  rtCfg.APIKey,
				Model:   rtCfg.Model,
			}

			cfg := research.Config{
				Perspectives:            perspectives,
				QuestionsPerPerspective: questionsPerPerspective,
				ResultsPerQuery:         resultsPerQuery,
				MaxTotalFetches:         maxFetches,
			}
			entity := research.Entity{
				Label: args[0],
				Type:  entityType,
			}

			maxPossible := cfg.Perspectives * cfg.QuestionsPerPerspective * cfg.ResultsPerQuery
			if _, wErr := fmt.Fprintf(subcmd.OutOrStdout(),
				"research: entity=%q type=%q perspectives=%d questions-per=%d results-per-query=%d max-fetches=%d (ceiling: %d searches, %d fetches)\n",
				entity.Label, entity.Type, cfg.Perspectives, cfg.QuestionsPerPerspective, cfg.ResultsPerQuery, cfg.MaxTotalFetches,
				cfg.Perspectives*cfg.QuestionsPerPerspective, min(maxPossible, cfg.MaxTotalFetches)); wErr != nil {
				return fmt.Errorf("write output: %w", wErr)
			}

			pool, err := db.NewPool(subcmd.Context(), resolvedDSN)
			if err != nil {
				return err
			}
			defer pool.Close()

			queries, err := research.GenerateQueries(subcmd.Context(), entity, cfg, nil, llmCfg)
			if err != nil {
				return fmt.Errorf("generate queries: %w", err)
			}
			if _, wErr := fmt.Fprintf(subcmd.OutOrStdout(), "research: generated %d unique queries\n", len(queries)); wErr != nil {
				return fmt.Errorf("write output: %w", wErr)
			}

			collector := &research.SearchCollector{
				SearchURL:       searxngURL,
				Queries:         queries,
				ResultsPerQuery: cfg.ResultsPerQuery,
				MaxTotalFetches: cfg.MaxTotalFetches,
			}

			p := &workers.SourcePipeline{DB: pool}
			if err := p.CollectOnce(subcmd.Context(), resolvedTenant, collector); err != nil {
				return fmt.Errorf("collect: %w", err)
			}
			if _, wErr := fmt.Fprintf(subcmd.OutOrStdout(), "research: collection complete (trust_tier=web, status=collected)\n"); wErr != nil {
				return fmt.Errorf("write output: %w", wErr)
			}
			return nil
		},
	}
	researchCmd.Flags().Int("perspectives", 5, "Number of research perspectives")
	researchCmd.Flags().Int("questions-per", 4, "Questions per perspective")
	researchCmd.Flags().Int("results-per-query", 5, "Search results per query")
	researchCmd.Flags().Int("max-fetches", 40, "Hard ceiling on page fetches")
	researchCmd.Flags().String("entity-type", "", "Entity type (e.g. Person, City, Company)")
	// C5: FACTVAULT_SEARXNG_URL wired via this flag.
	researchCmd.Flags().String("searxng-url", "", "SearXNG base URL (or FACTVAULT_SEARXNG_URL)")
	cmd.AddCommand(researchCmd)

	embedCmd := &cobra.Command{
		Use:   "embed",
		Short: "Populate NULL embedding columns for entities, statements, and sources",
		Args:  cobra.NoArgs,
		RunE: func(subcmd *cobra.Command, _ []string) error {
			resolvedDSN, err := config.ResolveSecret(subcmd.Flags().Lookup("dsn"), "FACTVAULT_DATABASE_URL", "", true)
			if err != nil {
				return err
			}
			resolvedTenant, err := config.ResolveString(subcmd.Flags().Lookup("tenant"), "FACTVAULT_DEV_TENANT_ID", "", true)
			if err != nil {
				return err
			}
			if tf := subcmd.Flags().Lookup("tenant"); tf == nil || !tf.Changed {
				fmt.Fprintln(os.Stderr, "warning: using dev tenant from env (FACTVAULT_DEV_TENANT_ID); pass --tenant for production use")
			}
			resolvedLimit, err := config.ResolveInt(subcmd.Flags().Lookup("limit"), "FACTVAULT_WORKER_LIMIT", 100, false)
			if err != nil {
				return err
			}
			// C7: default embedder URL is :8081 (host-accessible port).
			embedderURL, err := config.ResolveString(subcmd.Flags().Lookup("embedder-url"), "FACTVAULT_EMBEDDER_URL", "http://localhost:8081", false)
			if err != nil {
				return err
			}

			pool, err := db.NewPool(subcmd.Context(), resolvedDSN)
			if err != nil {
				return err
			}
			defer pool.Close()

			w := &workers.EmbedWorker{
				DB:     pool,
				Client: embed.NewClient(embedderURL, nil),
			}
			result, err := w.RunOnce(subcmd.Context(), resolvedTenant, workers.EmbedOptions{Limit: resolvedLimit})
			if err != nil {
				return err
			}
			if _, wErr := fmt.Fprintf(subcmd.OutOrStdout(), "embed: populated=%d skipped=%d\n", result.Populated, result.Skipped); wErr != nil {
				return fmt.Errorf("write output: %w", wErr)
			}
			return nil
		},
	}
	embedCmd.Flags().String("embedder-url", "", "Embedder service URL (or FACTVAULT_EMBEDDER_URL)")
	cmd.AddCommand(embedCmd)

	return cmd
}

// resolveLLMRuntimeConfig resolves LLM config following C1/C2/C9.
// Non-empty flag arg means the caller's flag was set explicitly; empty arg falls
// through to env vars with C1 resolver semantics.
// C2: FACTVAULT_LLM_BASE_URL is canonical; FACTVAULT_LLM_URL is a deprecated alias.
// C9: FACTVAULT_LLM_API_KEY_FILE takes precedence over FACTVAULT_LLM_API_KEY (via ResolveSecret).
// The --llm-api-key CLI flag is intentionally removed; secrets must not appear in process args.
func resolveLLMRuntimeConfig(provider, model, baseURL string) (workers.LLMRuntimeConfig, error) {
	// Model: flag arg > FACTVAULT_LLM_MODEL > default.
	if model == "" {
		var err error
		model, _, err = config.ResolveStringWithAlias(nil, "FACTVAULT_LLM_MODEL", "", "llama3.1:8b", false)
		if err != nil {
			return workers.LLMRuntimeConfig{}, err
		}
	}

	// BaseURL: flag arg > FACTVAULT_LLM_BASE_URL > FACTVAULT_LLM_URL (alias, C2) > default.
	if baseURL == "" {
		var isAlias bool
		var err error
		baseURL, isAlias, err = config.ResolveStringWithAlias(nil, "FACTVAULT_LLM_BASE_URL", "FACTVAULT_LLM_URL", "http://localhost:11434/v1", false)
		if err != nil {
			return workers.LLMRuntimeConfig{}, err
		}
		if isAlias {
			// C2: deprecated alias in use -- warn on stderr.
			fmt.Fprintln(os.Stderr, "warning: FACTVAULT_LLM_URL is deprecated; use FACTVAULT_LLM_BASE_URL")
		}
	}

	// APIKey: FACTVAULT_LLM_API_KEY_FILE > FACTVAULT_LLM_API_KEY > empty (C9 via ResolveSecret).
	apiKey, err := config.ResolveSecret(nil, "FACTVAULT_LLM_API_KEY", "", false)
	if err != nil {
		return workers.LLMRuntimeConfig{}, err
	}

	return workers.LLMRuntimeConfig{
		Provider: provider,
		Model:    model,
		BaseURL:  baseURL,
		APIKey:   apiKey,
	}, nil
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

// effectiveRSSTenant returns the tenant to use for a single RSS feed following C4:
//
//	--tenant flag (flagChanged/flagValue) > feed.TenantID > devTenant (from env).
//
// devTenant is the pre-resolved FACTVAULT_DEV_TENANT_ID value (empty string if unset).
// The second return value is true when the dev-tenant fallback was used, signalling
// that the caller should emit a warn-on-dev message (C4 semantics).
func effectiveRSSTenant(flagChanged bool, flagValue, feedTenantID, devTenant string) (string, bool) {
	if flagChanged {
		return flagValue, false
	}
	if feedTenantID != "" {
		return feedTenantID, false
	}
	// Neither flag nor per-feed tenant is set; fall back to FACTVAULT_DEV_TENANT_ID.
	return devTenant, devTenant != ""
}
