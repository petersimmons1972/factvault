package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/config"
	"github.com/petersimmons1972/factvault/internal/doctor"
)

func newDoctorCmd() *cobra.Command {
	var cfg doctor.Config
	var requiredOnly bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run first-boot health checks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			// C1: flag.Changed > env > default (empty = optional).
			cfg.DatabaseURL, err = config.ResolveSecret(cmd.Flags().Lookup("dsn"), "FACTVAULT_DATABASE_URL", "", false)
			if err != nil {
				return err
			}
			// C2: FACTVAULT_LLM_BASE_URL canonical; FACTVAULT_LLM_URL deprecated alias.
			llmURL, isAlias, err := config.ResolveStringWithAlias(cmd.Flags().Lookup("llm-url"), "FACTVAULT_LLM_BASE_URL", "FACTVAULT_LLM_URL", "", false)
			if err != nil {
				return err
			}
			if isAlias {
				fmt.Fprintln(os.Stderr, "warning: FACTVAULT_LLM_URL is deprecated; use FACTVAULT_LLM_BASE_URL")
			}
			cfg.LLMURL = llmURL
			// C7: default embedder URL is :8081 (host-accessible port).
			cfg.EmbedderURL, err = config.ResolveString(cmd.Flags().Lookup("embedder-url"), "FACTVAULT_EMBEDDER_URL", "http://localhost:8081", false)
			if err != nil {
				return err
			}
			cfg.WaybackURL, err = config.ResolveString(cmd.Flags().Lookup("wayback-url"), "FACTVAULT_WAYBACK_URL", "https://web.archive.org", false)
			if err != nil {
				return err
			}

			results := doctor.RunAll(cmd.Context(), cfg)
			for _, result := range results {
				var status string
				switch {
				case result.OK:
					status = "OK"
				case requiredOnly && !result.Required:
					// Optional check failed but --required-only is set: downgrade to WARN.
					status = "WARN"
				default:
					status = "FAIL"
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%-28s %s %s\n", result.Name, status, result.Detail); err != nil {
					return err
				}
				// Suppress remedy lines for optional failures when --required-only is set.
				if !result.OK && result.Remedy != "" && (!requiredOnly || result.Required) {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "  remedy: %s\n", result.Remedy); err != nil {
						return err
					}
				}
			}
			if requiredOnly {
				if !doctor.RequiredOK(results) {
					return fmt.Errorf("one or more required doctor checks failed")
				}
				return nil
			}
			if !doctor.AllOK(results) {
				return fmt.Errorf("one or more doctor checks failed")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cfg.DatabaseURL, "dsn", "", "Postgres DSN (or FACTVAULT_DATABASE_URL)")
	cmd.Flags().StringVar(&cfg.LLMURL, "llm-url", "", "LLM base URL (or FACTVAULT_LLM_BASE_URL)")
	cmd.Flags().StringVar(&cfg.EmbedderURL, "embedder-url", "", "embedder base URL (or FACTVAULT_EMBEDDER_URL)")
	cmd.Flags().StringVar(&cfg.WaybackURL, "wayback-url", "", "Wayback base URL (or FACTVAULT_WAYBACK_URL)")
	cmd.Flags().BoolVar(&requiredOnly, "required-only", false, "Exit 0 if only optional checks (LLM, embedder, Wayback) fail; show WARN instead of FAIL for them")
	return cmd
}
