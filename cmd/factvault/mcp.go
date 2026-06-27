package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/auth"
	"github.com/petersimmons1972/factvault/internal/config"
	"github.com/petersimmons1972/factvault/internal/db"
	mcpserver "github.com/petersimmons1972/factvault/internal/mcp"
)

func newMCPCmd() *cobra.Command {
	var dsn string
	var publicKeyPath, authToken string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the factvault MCP server over stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			// C1: flag.Changed > env > required error.
			dsn, err = config.ResolveSecret(cmd.Flags().Lookup("dsn"), "FACTVAULT_DATABASE_URL", "", true)
			if err != nil {
				return err
			}
			// Reject inline passwords in the DSN flag — they appear in /proc/<pid>/cmdline.
			if f := cmd.Flags().Lookup("dsn"); f != nil && f.Changed {
				if err := config.ValidateDSNNoPassword(dsn); err != nil {
					return err
				}
			}
			if publicKeyPath == "" {
				publicKeyPath = os.Getenv("FACTVAULT_JWT_PUBLIC_KEY")
			}
			if publicKeyPath == "" {
				return fmt.Errorf("JWT public key required: set --jwt-public-key or FACTVAULT_JWT_PUBLIC_KEY")
			}
			// auth-token: flag value already stored in authToken via StringVar;
			// fall back to env when the flag was not explicitly set.
			if !cmd.Flags().Lookup("auth-token").Changed {
				authToken = os.Getenv("FACTVAULT_MCP_AUTH_TOKEN")
			}
			keyData, err := os.ReadFile(filepath.Clean(publicKeyPath))
			if err != nil {
				return err
			}
			publicKey, err := auth.ParsePublicKeyPEM(keyData)
			if err != nil {
				return err
			}
			pool, err := db.NewPool(cmd.Context(), dsn)
			if err != nil {
				return err
			}
			defer pool.Close()
			return mcpserver.New(pool, publicKey, authToken, os.Getenv("FACTVAULT_EMBEDDER_URL")).RunStdio(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres DSN (or FACTVAULT_DATABASE_URL)")
	cmd.Flags().StringVar(&publicKeyPath, "jwt-public-key", "", "JWT public key PEM path")
	cmd.Flags().StringVar(&authToken, "auth-token", "", "Optional default Bearer token for MCP clients without explicit authorization")
	if err := cmd.Flags().MarkDeprecated("auth-token", "use FACTVAULT_MCP_AUTH_TOKEN environment variable instead"); err != nil {
		panic(err)
	}
	return cmd
}
