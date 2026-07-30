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
			if f := cmd.Flags().Lookup("dsn"); f != nil && f.Changed {
				if err := config.ValidateDSNNoPassword(dsn); err != nil {
					return err
				}
			}
			publicKeyPath, err = config.ResolveSecret(cmd.Flags().Lookup("jwt-public-key"), "FACTVAULT_JWT_PUBLIC_KEY", "", true)
			if err != nil {
				return err
			}
			// C9: FACTVAULT_MCP_AUTH_TOKEN_FILE > FACTVAULT_MCP_AUTH_TOKEN > --auth-token flag.
			if !cmd.Flags().Lookup("auth-token").Changed {
				authToken, err = config.ResolveSecret(nil, "FACTVAULT_MCP_AUTH_TOKEN", "", false)
				if err != nil {
					return err
				}
			}
			if authToken == "" {
				return fmt.Errorf("MCP auth token required: set FACTVAULT_MCP_AUTH_TOKEN env (preferred) or --auth-token flag")
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
