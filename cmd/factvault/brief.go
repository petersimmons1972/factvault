package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/assembler"
	"github.com/petersimmons1972/factvault/internal/briefs"
	"github.com/petersimmons1972/factvault/internal/config"
	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/retrieval"
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
		Short: "Generate and persist a deterministic evidence brief from tenant-scoped data",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pool, tenant, err := openBriefPool(cmd, *dsn, *tenantID)
			if err != nil {
				return err
			}
			defer pool.Close()
			if inputPath != "" {
				return errors.New("--input is no longer supported; brief bundles are assembled server-side")
			}
			req := briefs.GenerateRequest{SourceKind: briefs.SourceKind(sourceKind)}
			if entityID != "" {
				req.EntityID = &entityID
			}
			if query != "" {
				req.Query = &query
			}
			service := briefs.Service{
				Pool: pool,
				BundleLoader: briefs.BundleLoaderFunc(func(ctx context.Context, tenantID string, req briefs.GenerateRequest) (*assembler.Bundle, error) {
					return loadBriefBundleForCLI(ctx, pool, tenantID, req)
				}),
			}
			rec, err := service.GenerateAndStore(cmd.Context(), tenant, req)
			if err != nil {
				return err
			}
			return writeJSONOutput(cmd, rec)
		},
	}
	cmd.Flags().StringVar(&inputPath, "input", "", "Deprecated: briefs are assembled server-side")
	cmd.Flags().StringVar(&sourceKind, "source-kind", string(briefs.SourceKindDossier), "brief source kind: dossier or story")
	cmd.Flags().StringVar(&entityID, "entity-id", "", "Entity UUID for dossier-derived brief")
	cmd.Flags().StringVar(&query, "query", "", "Query text for story-derived brief")
	if err := cmd.Flags().MarkDeprecated("input", "bundle JSON is no longer accepted; briefs are assembled server-side"); err != nil {
		panic(err)
	}
	return cmd
}

func newBriefListCmd(dsn, tenantID *string) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List persisted evidence briefs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pool, tenant, err := openBriefPool(cmd, *dsn, *tenantID)
			if err != nil {
				return err
			}
			defer pool.Close()
			records, err := (briefs.Service{Pool: pool}).List(cmd.Context(), tenant, briefs.ListOptions{Limit: limit})
			if err != nil {
				return err
			}
			return writeJSONOutput(cmd, records)
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
			return writeJSONOutput(cmd, record)
		},
	}
	return cmd
}

func loadBriefBundleForCLI(ctx context.Context, pool *pgxpool.Pool, tenantID string, req briefs.GenerateRequest) (*assembler.Bundle, error) {
	svc := retrieval.NewService(pool, nil, nil)
	switch req.SourceKind {
	case briefs.SourceKindDossier:
		if req.EntityID == nil {
			return nil, fmt.Errorf("%w: dossier briefs require entity_id", briefs.ErrInvalidGenerateRequest)
		}
		resp, err := svc.Dossier(ctx, tenantID, *req.EntityID)
		if err != nil {
			return nil, err
		}
		return resp.Bundle, nil
	case briefs.SourceKindStory:
		if req.Query == nil {
			return nil, fmt.Errorf("%w: story briefs require query", briefs.ErrInvalidGenerateRequest)
		}
		resp, err := svc.Story(ctx, tenantID, retrieval.StoryRequest{Query: *req.Query})
		if err != nil {
			return nil, err
		}
		return resp.Bundle, nil
	default:
		return nil, fmt.Errorf("%w: unsupported source_kind %q", briefs.ErrInvalidGenerateRequest, req.SourceKind)
	}
}

func openBriefPool(cmd *cobra.Command, _, _ string) (*pgxpool.Pool, string, error) {
	// C1/C4: resolvers replace manual if-empty chains.
	// Note: dsn and tenantID args are ignored; values come from flags/env via resolvers.
	dsn, err := config.ResolveSecret(cmd.Flags().Lookup("dsn"), "FACTVAULT_DATABASE_URL", "", true)
	if err != nil {
		return nil, "", err
	}
	if f := cmd.Flags().Lookup("dsn"); f != nil && f.Changed {
		if err := config.ValidateDSNNoPassword(dsn); err != nil {
			return nil, "", err
		}
	}
	// C4: --tenant > FACTVAULT_DEV_TENANT_ID > ERROR.
	tenantID, err := config.ResolveString(cmd.Flags().Lookup("tenant"), "FACTVAULT_DEV_TENANT_ID", "", true)
	if err != nil {
		return nil, "", err
	}
	pool, err := db.NewPool(cmd.Context(), dsn)
	if err != nil {
		return nil, "", err
	}
	return pool, tenantID, nil
}

func writeJSONOutput(cmd *cobra.Command, value any) error {
	return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
}
