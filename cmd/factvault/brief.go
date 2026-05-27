package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/assembler"
	"github.com/petersimmons1972/factvault/internal/briefs"
	"github.com/petersimmons1972/factvault/internal/db"
)

func newBriefCmd() *cobra.Command {
	var dsn, tenantID string
	cmd := &cobra.Command{Use: "brief", Short: "Generate and read evidence briefs"}
	cmd.PersistentFlags().StringVar(&dsn, "dsn", "", "Postgres DSN (or FACTVAULT_DATABASE_URL)")
	cmd.PersistentFlags().StringVar(&tenantID, "tenant", "", "Tenant UUID")

	cmd.AddCommand(newBriefGenerateCmd(&dsn, &tenantID))
	cmd.AddCommand(newBriefListCmd(&dsn, &tenantID))
	cmd.AddCommand(newBriefGetCmd(&dsn, &tenantID))
	return cmd
}

func newBriefGenerateCmd(dsn, tenantID *string) *cobra.Command {
	var inputPath, sourceKind, entityID, query string
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate and persist a deterministic evidence brief",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, tenant, err := openBriefPool(cmd, *dsn, *tenantID)
			if err != nil {
				return err
			}
			defer pool.Close()
			bundle, err := readBundle(inputPath)
			if err != nil {
				return err
			}
			service := briefs.Service{Pool: pool}
			req := briefs.GenerateRequest{SourceKind: briefs.SourceKind(sourceKind), Bundle: bundle}
			if entityID != "" {
				req.EntityID = &entityID
			}
			if query != "" {
				req.Query = &query
			}
			rec, err := service.GenerateAndStore(cmd.Context(), tenant, req)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(rec)
		},
	}
	cmd.Flags().StringVar(&inputPath, "input", "", "Path to dossier/story bundle JSON (default stdin)")
	cmd.Flags().StringVar(&sourceKind, "source-kind", string(briefs.SourceKindDossier), "brief source kind: dossier or story")
	cmd.Flags().StringVar(&entityID, "entity-id", "", "Entity UUID for dossier-derived brief")
	cmd.Flags().StringVar(&query, "query", "", "Query text for story-derived brief")
	return cmd
}

func newBriefListCmd(dsn, tenantID *string) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List persisted evidence briefs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, tenant, err := openBriefPool(cmd, *dsn, *tenantID)
			if err != nil {
				return err
			}
			defer pool.Close()
			records, err := (briefs.Service{Pool: pool}).List(cmd.Context(), tenant, briefs.ListOptions{Limit: limit})
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(records)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 100, "Max records to return")
	return cmd
}

func newBriefGetCmd(dsn, tenantID *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <brief-id>",
		Short: "Get a persisted evidence brief",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, tenant, err := openBriefPool(cmd, *dsn, *tenantID)
			if err != nil {
				return err
			}
			defer pool.Close()
			record, err := (briefs.Service{Pool: pool}).Get(cmd.Context(), tenant, args[0])
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(record)
		},
	}
	return cmd
}

func openBriefPool(cmd *cobra.Command, dsn, tenantID string) (*pgxpool.Pool, string, error) {
	if dsn == "" {
		dsn = os.Getenv("FACTVAULT_DATABASE_URL")
	}
	if dsn == "" {
		return nil, "", fmt.Errorf("database DSN required: set --dsn or FACTVAULT_DATABASE_URL")
	}
	if tenantID == "" {
		return nil, "", fmt.Errorf("tenant required: set --tenant")
	}
	pool, err := db.NewPool(cmd.Context(), dsn)
	if err != nil {
		return nil, "", err
	}
	return pool, tenantID, nil
}

func readBundle(inputPath string) (*assembler.Bundle, error) {
	var data []byte
	var err error
	if inputPath == "" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(inputPath)
	}
	if err != nil {
		return nil, err
	}
	var bundle assembler.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return nil, fmt.Errorf("parse bundle json: %w", err)
	}
	return &bundle, nil
}
