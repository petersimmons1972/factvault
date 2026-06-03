package main

import (
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/auth"
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
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "One-shot first-boot initialiser: keygen, health checks, and optional example load",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Resolve DSN.
			if dsn == "" {
				dsn = os.Getenv("FACTVAULT_DATABASE_URL")
			}
			if dsn == "" {
				return fmt.Errorf("database DSN required: set --dsn or FACTVAULT_DATABASE_URL")
			}
			// Resolve tenant.
			if tenantID == "" {
				tenantID = os.Getenv("FACTVAULT_DEV_TENANT_ID")
			}
			if tenantID == "" {
				tenantID = "11111111-1111-1111-1111-111111111111"
			}

			out := cmd.OutOrStdout()

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
			cfg := doctor.Config{
				DatabaseURL: dsn,
				LLMURL:      firstNonEmpty(os.Getenv("FACTVAULT_LLM_BASE_URL"), os.Getenv("FACTVAULT_LLM_URL")),
				EmbedderURL: os.Getenv("FACTVAULT_EMBEDDER_URL"),
				WaybackURL:  os.Getenv("FACTVAULT_WAYBACK_URL"),
			}
			results := doctor.RunAll(cmd.Context(), cfg)
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
	cmd.Flags().StringVar(&keyDir, "key-dir", ".local", "Directory for JWT key files")
	cmd.Flags().StringVar(&exampleName, "example", "ai-startup-tracking", "Example name to load (empty to skip)")
	cmd.Flags().BoolVar(&skipExample, "skip-example", false, "Skip example data loading")
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
	if err := os.WriteFile(pubPath, publicPEM, 0o644); err != nil {
		return err
	}
	return nil
}

// fileNonEmpty returns true if path exists and has non-zero size.
func fileNonEmpty(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}
