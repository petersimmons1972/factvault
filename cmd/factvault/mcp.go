package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/db"
	mcpserver "github.com/petersimmons1972/factvault/internal/mcp"
)

func newMCPCmd() *cobra.Command {
	var dsn string
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
			pool, err := db.NewPool(cmd.Context(), dsn)
			if err != nil {
				return err
			}
			defer pool.Close()
			return mcpserver.New(pool, nil).RunStdio(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres DSN (or FACTVAULT_DATABASE_URL)")
	return cmd
}
