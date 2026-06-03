package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/spf13/cobra"
)

func newMigrateCmd() *cobra.Command {
	var dsn string

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run goose database migrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMigrations(cmd.Context(), dsn)
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres DSN (overrides FACTVAULT_DATABASE_URL)")
	return cmd
}

func runMigrations(ctx context.Context, dsn string) error {
	if dsn == "" {
		dsn = os.Getenv("FACTVAULT_DATABASE_URL")
	}
	if dsn == "" {
		return fmt.Errorf("database DSN required: set --dsn flag or FACTVAULT_DATABASE_URL")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "close db: %v\n", err)
		}
	}()

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	if err := goose.RunContext(ctx, "up", db, "migrations"); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
