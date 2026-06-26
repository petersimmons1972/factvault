package main

import (
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/auth"
	"github.com/petersimmons1972/factvault/internal/config"
	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/doctor"
	fvexamples "github.com/petersimmons1972/factvault/internal/examples"
)

func newInitCmd() *cobra.Command {
	var (
		dsn         string
		tenantID    string
		keyDir      string
		exampleName string
		skipExample bool
		skipMigrate bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "One-shot first-boot initialiser: migrate, keygen, health checks, and optional example load",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			// C1/C5: resolvers replace manual if-empty chains.
			dsn, err = config.ResolveSecret(cmd.Flags().Lookup("dsn"), "FACTVAULT_DATABASE_URL", "", true)
			if err != nil {
				return err
			}
			// C4: OOBE command — fall through to default UUID rather than error.
			tenantID, err = config.ResolveString(cmd.Flags().Lookup("tenant"), "FACTVAULT_DEV_TENANT_ID", "11111111-1111-1111-1111-111111111111", false)
			if err != nil {
				return err
			}
			// C5: FACTVAULT_AUTH_DIR wired.
			keyDir, err = config.ResolveString(cmd.Flags().Lookup("key-dir"), "FACTVAULT_AUTH_DIR", ".local", false)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()

			// C8: run migrations first (idempotent); --skip-migrate bypasses this.
			if !skipMigrate {
				if _, pErr := fmt.Fprintf(out, "==> Running database migrations\n"); pErr != nil {
					return pErr
				}
				if err := runMigrations(cmd.Context(), dsn); err != nil {
					return fmt.Errorf("migrate: %w", err)
				}
			}

			// --- Key generation ---
			privPath := filepath.Join(keyDir, "private.pem")
			pubPath := filepath.Join(keyDir, "public.pem")
			if fileNonEmpty(privPath) && fileNonEmpty(pubPath) {
				if _, err := fmt.Fprintf(out, "==> JWT keys already exist in %s — skipping keygen\n", keyDir); err != nil {
					return err
				}
			} else {
				if _, err := fmt.Fprintf(out, "==> Generating JWT keys in %s\n", keyDir); err != nil {
					return err
				}
				if err := initKeys(keyDir); err != nil {
					return fmt.Errorf("keygen: %w", err)
				}
				if _, err := fmt.Fprintf(out, "    private.pem: %s\n", privPath); err != nil {
					return err
				}
				if _, err := fmt.Fprintf(out, "    public.pem:  %s\n", pubPath); err != nil {
					return err
				}
			}

			// --- Doctor checks ---
			if _, err := fmt.Fprintf(out, "==> Running health checks\n"); err != nil {
				return err
			}
			// C2: FACTVAULT_LLM_BASE_URL canonical; FACTVAULT_LLM_URL deprecated alias.
			llmURL, isAlias, _ := config.ResolveStringWithAlias(nil, "FACTVAULT_LLM_BASE_URL", "FACTVAULT_LLM_URL", "", false)
			if isAlias {
				fmt.Fprintln(os.Stderr, "warning: FACTVAULT_LLM_URL is deprecated; use FACTVAULT_LLM_BASE_URL")
			}
			embedderURL, _ := config.ResolveString(nil, "FACTVAULT_EMBEDDER_URL", "http://localhost:8081", false)
			waybackURL, _ := config.ResolveString(nil, "FACTVAULT_WAYBACK_URL", "https://web.archive.org", false)
			drCfg := doctor.Config{
				DatabaseURL: dsn,
				LLMURL:      llmURL,
				EmbedderURL: embedderURL,
				WaybackURL:  waybackURL,
			}
			results := doctor.RunAll(cmd.Context(), drCfg)
			for _, r := range results {
				status := "FAIL"
				if r.OK {
					status = "OK"
				} else if !r.Required {
					status = "WARN"
				}
				if _, err := fmt.Fprintf(out, "    %-28s %s %s\n", r.Name, status, r.Detail); err != nil {
					return err
				}
			}
			if !doctor.RequiredOK(results) {
				return fmt.Errorf("one or more required health checks failed; fix them before proceeding")
			}

			// --- Example load ---
			if !skipExample && exampleName != "" {
				if _, err := fmt.Fprintf(out, "==> Loading example %q for tenant %s\n", exampleName, tenantID); err != nil {
					return err
				}
				ex, err := fvexamples.Load("examples", exampleName)
				if err != nil {
					return fmt.Errorf("load example: %w", err)
				}
				pool, err := db.NewPool(cmd.Context(), dsn)
				if err != nil {
					return fmt.Errorf("connect db: %w", err)
				}
				defer pool.Close()
				if err := ex.Insert(cmd.Context(), pool, tenantID); err != nil {
					return fmt.Errorf("insert example: %w", err)
				}
				if _, err := fmt.Fprintf(out, "    loaded %s: %d properties, %d seeds\n", ex.Name, len(ex.Properties), len(ex.Seeds)); err != nil {
					return err
				}
			}

			// --- Next steps summary ---
			if _, err := fmt.Fprintf(out, "\n==> Next steps\n"); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "    Start the API:\n"); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "      ./bin/factvault api --jwt-public-key %s\n", pubPath); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "    Obtain a dev auth token:\n"); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "      ./bin/factvault auth token --tenant %s --jwt-private-key %s\n", tenantID, privPath); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "    Run the worker pipeline:\n"); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "      ./bin/factvault worker dossier --tenant %s --dsn \"$FACTVAULT_DATABASE_URL\"\n", tenantID); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres DSN (or FACTVAULT_DATABASE_URL)")
	cmd.Flags().StringVar(&tenantID, "tenant", "", "Tenant UUID (or FACTVAULT_DEV_TENANT_ID; default 11111111-1111-1111-1111-111111111111)")
	// C5: FACTVAULT_AUTH_DIR wired; .local is the fallback default.
	cmd.Flags().StringVar(&keyDir, "key-dir", "", "Directory for JWT key files (or FACTVAULT_AUTH_DIR; default .local)")
	cmd.Flags().StringVar(&exampleName, "example", "ai-startup-tracking", "Example name to load (empty to skip)")
	cmd.Flags().BoolVar(&skipExample, "skip-example", false, "Skip example data loading")
	// C8: --skip-migrate lets operators who have already migrated bypass the step.
	cmd.Flags().BoolVar(&skipMigrate, "skip-migrate", false, "Skip running database migrations before keygen")
	return cmd
}

// initKeys generates an RSA key pair and writes private.pem (0600) and
// public.pem (0644) into keyDir. If both files already exist and are
// non-empty it is a no-op (idempotent).
func initKeys(keyDir string) error {
	privPath := filepath.Join(keyDir, "private.pem")
	pubPath := filepath.Join(keyDir, "public.pem")
	if fileNonEmpty(privPath) && fileNonEmpty(pubPath) {
		return nil
	}
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return err
	}
	privatePEM, publicPEM, err := auth.GenerateKeyPair()
	if err != nil {
		return err
	}
	// Validate PEM blocks before writing.
	if b, _ := pem.Decode(privatePEM); b == nil {
		return fmt.Errorf("generated private key is not valid PEM")
	}
	if b, _ := pem.Decode(publicPEM); b == nil {
		return fmt.Errorf("generated public key is not valid PEM")
	}
	if err := os.WriteFile(privPath, privatePEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(pubPath, publicPEM, 0o600); err != nil {
		return err
	}
	return nil
}

// fileNonEmpty returns true if path exists and has non-zero size.
func fileNonEmpty(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}
