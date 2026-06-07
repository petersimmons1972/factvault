// Package retrieval provides semantic search and entity retrieval over the fact store.
package retrieval

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/petersimmons1972/factvault/internal/assembler"
	"github.com/petersimmons1972/factvault/internal/db"
)

// Service provides tenant-aware retrieval APIs.
type Service struct {
	Pool *pgxpool.Pool
}

// DossierResponse is the HTTP response payload for a single-entity dossier request.
type DossierResponse struct {
	Bundle   *assembler.Bundle `json:"bundle"`
	CachedAt *string           `json:"cached_at,omitempty"`
}

// StoryRequest captures search and depth controls for a story build.
type StoryRequest struct {
	Query         string  `json:"query"`
	Depth         int     `json:"depth,omitempty"`
	MaxFacts      *int    `json:"max_facts,omitempty"`
	MinConfidence float64 `json:"min_confidence,omitempty"`
}

// StoryResponse returns a built bundle for a story request.
type StoryResponse struct {
	Bundle *assembler.Bundle `json:"bundle"`
}

// FactsQueryRequest captures a full-text style query and confidence filters.
type FactsQueryRequest struct {
	Query         string  `json:"query"`
	MaxFacts      *int    `json:"max_facts,omitempty"`
	MinConfidence float64 `json:"min_confidence,omitempty"`
}

// FactsQueryResponse returns bundles for a facts-like query.
type FactsQueryResponse struct {
	Bundle *assembler.Bundle `json:"bundle"`
}

// Dossier returns the root bundle for one entity.
func (s Service) Dossier(ctx context.Context, tenantID, entityID string) (*DossierResponse, error) {
	return withTenantTx(ctx, s.Pool, tenantID, func(ctx context.Context, tx pgx.Tx) (*DossierResponse, error) {
		bundle, err := assembler.Assemble(ctx, tx, []string{entityID}, 0, tenantID)
		if err != nil {
			return nil, err
		}
		return &DossierResponse{Bundle: bundle}, nil
	})
}

// Story builds a story bundle from query-matched seeds.
func (s Service) Story(ctx context.Context, tenantID string, req StoryRequest) (*StoryResponse, error) {
	depth := req.Depth
	if depth == 0 {
		depth = 2
	}
	if depth < 1 || depth > 3 {
		return nil, assembler.ErrInvalidDepth
	}
	return withTenantTx(ctx, s.Pool, tenantID, func(ctx context.Context, tx pgx.Tx) (*StoryResponse, error) {
		seeds, err := seedEntities(ctx, tx, tenantID, req.Query, 10)
		if err != nil {
			return nil, err
		}
		if len(seeds) == 0 {
			return nil, assembler.ErrEntityNotFound
		}
		bundle, err := assembler.Assemble(ctx, tx, seeds, depth, tenantID)
		if err != nil {
			return nil, err
		}
		return &StoryResponse{Bundle: bundle}, nil
	})
}

// FactsQuery builds bundles for a general facts search query.
func (s Service) FactsQuery(ctx context.Context, tenantID string, req FactsQueryRequest) (*FactsQueryResponse, error) {
	return withTenantTx(ctx, s.Pool, tenantID, func(ctx context.Context, tx pgx.Tx) (*FactsQueryResponse, error) {
		seeds, err := seedEntities(ctx, tx, tenantID, req.Query, 10)
		if err != nil {
			return nil, err
		}
		if len(seeds) == 0 {
			return nil, assembler.ErrEntityNotFound
		}
		bundle, err := assembler.Assemble(ctx, tx, seeds, 0, tenantID)
		if err != nil {
			return nil, err
		}
		return &FactsQueryResponse{Bundle: bundle}, nil
	})
}

func withTenantTx[T any](ctx context.Context, pool *pgxpool.Pool, tenantID string, fn func(context.Context, pgx.Tx) (T, error)) (T, error) {
	var zero T
	if pool == nil {
		return zero, fmt.Errorf("retrieval: nil db pool")
	}
	var tenant pgtype.UUID
	if err := tenant.Scan(tenantID); err != nil {
		return zero, fmt.Errorf("invalid tenant id: %w", err)
	}
	txCtx := db.WithPool(ctx, pool)
	txCtx, tx, err := db.TenantContext(txCtx, tenant)
	if err != nil {
		return zero, err
	}
	defer func() {
		if err := tx.Rollback(txCtx); err != nil {
			fmt.Fprintf(os.Stderr, "rollback after commit: %v\n", err)
		}
	}()
	return fn(txCtx, tx)
}

func seedEntities(ctx context.Context, tx pgx.Tx, tenantID, query string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text
		FROM entities
		WHERE tenant_id = $1::uuid AND ($2 = '' OR label ILIKE '%' || $2 || '%' OR COALESCE(description, '') ILIKE '%' || $2 || '%')
		ORDER BY created_at DESC
		LIMIT $3
	`, tenantID, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
