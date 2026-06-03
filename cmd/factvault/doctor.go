package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/doctor"
)

func newDoctorCmd() *cobra.Command {
	var cfg doctor.Config
	var requiredOnly bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run first-boot health checks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.DatabaseURL == "" {
				cfg.DatabaseURL = os.Getenv("FACTVAULT_DATABASE_URL")
			}
			if cfg.LLMURL == "" {
				cfg.LLMURL = os.Getenv("FACTVAULT_LLM_URL")
			}
			if cfg.EmbedderURL == "" {
				cfg.EmbedderURL = os.Getenv("FACTVAULT_EMBEDDER_URL")
			}
			if cfg.WaybackURL == "" {
				cfg.WaybackURL = os.Getenv("FACTVAULT_WAYBACK_URL")
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
				fmt.Fprintf(cmd.OutOrStdout(), "%-28s %s %s\n", result.Name, status, result.Detail)
				// Suppress remedy lines for optional failures when --required-only is set.
				if !result.OK && result.Remedy != "" && (!requiredOnly || result.Required) {
					fmt.Fprintf(cmd.OutOrStdout(), "  remedy: %s\n", result.Remedy)
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
	cmd.Flags().StringVar(&cfg.LLMURL, "llm-url", "", "LLM base URL")
	cmd.Flags().StringVar(&cfg.EmbedderURL, "embedder-url", "", "embedder base URL")
	cmd.Flags().StringVar(&cfg.WaybackURL, "wayback-url", "", "Wayback base URL")
	cmd.Flags().BoolVar(&requiredOnly, "required-only", false, "Exit 0 if only optional checks (LLM, embedder, Wayback) fail; show WARN instead of FAIL for them")
	return cmd
}
