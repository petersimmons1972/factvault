package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/auth"
	"github.com/petersimmons1972/factvault/internal/db"
	mcpserver "github.com/petersimmons1972/factvault/internal/mcp"
)

func newMCPCmd() *cobra.Command {
	var dsn string
	var publicKeyPath, authToken string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the factvault MCP server over stdio",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dsn == "" {
				dsn = os.Getenv("FACTVAULT_DATABASE_URL")
			}
			if dsn == "" {
				return fmt.Errorf("database DSN required: set --dsn or FACTVAULT_DATABASE_URL")
			}
			if publicKeyPath == "" {
				publicKeyPath = os.Getenv("FACTVAULT_JWT_PUBLIC_KEY")
			}
			if publicKeyPath == "" {
				return fmt.Errorf("JWT public key required: set --jwt-public-key or FACTVAULT_JWT_PUBLIC_KEY")
			}
			if authToken == "" {
				authToken = os.Getenv("FACTVAULT_MCP_AUTH_TOKEN")
			}
			keyData, err := os.ReadFile(publicKeyPath)
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
			return mcpserver.New(pool, publicKey, authToken).RunStdio(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres DSN (or FACTVAULT_DATABASE_URL)")
	cmd.Flags().StringVar(&publicKeyPath, "jwt-public-key", "", "JWT public key PEM path")
	cmd.Flags().StringVar(&authToken, "auth-token", "", "Optional default Bearer token for MCP clients without explicit authorization")
	return cmd
}
