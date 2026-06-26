package main

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/api"
	"github.com/petersimmons1972/factvault/internal/auth"
	"github.com/petersimmons1972/factvault/internal/config"
	"github.com/petersimmons1972/factvault/internal/db"
)

func newAPICmd() *cobra.Command {
	var dsn, addr, publicKeyPath string
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Run the factvault HTTP API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			// C1: typed resolver replaces manual if-empty chains.
			dsn, err = config.ResolveSecret(cmd.Flags().Lookup("dsn"), "FACTVAULT_DATABASE_URL", "", true)
			if err != nil {
				return err
			}
			addr, err = config.ResolveString(cmd.Flags().Lookup("addr"), "FACTVAULT_API_ADDR", ":8080", false)
			if err != nil {
				return err
			}
			publicKeyPath, err = config.ResolveSecret(cmd.Flags().Lookup("jwt-public-key"), "FACTVAULT_JWT_PUBLIC_KEY", "", true)
			if err != nil {
				return err
			}
			// C7: embedder URL — default :8081 for host access, in-network uses :8080.
			embedderURL, err := config.ResolveString(nil, "FACTVAULT_EMBEDDER_URL", "http://localhost:8081", false)
			if err != nil {
				return err
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
			httpServer := http.Server{
				Addr:              addr,
				Handler:           api.New(pool, pub, embedderURL).Router(),
				ReadTimeout:       5 * time.Second,
				ReadHeaderTimeout: 5 * time.Second,
				WriteTimeout:      30 * time.Second,
				IdleTimeout:       60 * time.Second,
			}
			return httpServer.ListenAndServe()
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres DSN (or FACTVAULT_DATABASE_URL)")
	cmd.Flags().StringVar(&addr, "addr", ":8080", "HTTP listen address (or FACTVAULT_API_ADDR)")
	cmd.Flags().StringVar(&publicKeyPath, "jwt-public-key", "", "JWT public key PEM path (or FACTVAULT_JWT_PUBLIC_KEY)")
	return cmd
}
