package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/api"
	"github.com/petersimmons1972/factvault/internal/auth"
	"github.com/petersimmons1972/factvault/internal/db"
)

func newAPICmd() *cobra.Command {
	var dsn, addr, publicKeyPath string
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Run the factvault HTTP API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
			data, err := os.ReadFile(filepath.Clean(publicKeyPath))
			if err != nil {
				return err
			}
			pub, err := auth.ParsePublicKeyPEM(data)
			if err != nil {
				return err
			}
			pool, err := db.NewPool(cmd.Context(), dsn)
			if err != nil {
				return err
			}
			defer pool.Close()
			return http.ListenAndServe(addr, api.New(pool, pub).Router())
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres DSN (or FACTVAULT_DATABASE_URL)")
	cmd.Flags().StringVar(&addr, "addr", ":8080", "HTTP listen address")
	cmd.Flags().StringVar(&publicKeyPath, "jwt-public-key", "", "JWT public key PEM path")
	return cmd
}
