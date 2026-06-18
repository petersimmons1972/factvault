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
	"github.com/petersimmons1972/factvault/internal/store"
)

// cosineSeedThreshold is the minimum cosine similarity score (1 - distance)
// for an entity to be returned by the cosine seed search path.
// Entities with score below this threshold are excluded from cosine results;
// when no entity meets the threshold, the search falls back to ILIKE.
const cosineSeedThreshold = 0.6

// Embedder abstracts the embed.Client so tests can inject a stub without
// requiring a live HTTP server.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Service provides tenant-aware retrieval APIs.
type Service struct {
	Pool        *pgxpool.Pool
	vectorStore store.VectorStore
	embedder    Embedder
}

// NewService constructs a Service with all required dependencies.
// embedder and vectorStore may be nil; if either is nil the cosine seed path
// is skipped and seedEntities falls back to ILIKE for all queries.
func NewService(pool *pgxpool.Pool, embedder Embedder, vectorStore store.VectorStore) Service {
	return Service{
		Pool:        pool,
		vectorStore: vectorStore,
		embedder:    embedder,
	}
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
		seeds, err := s.seedEntities(ctx, tx, tenantID, req.Query, 10)
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
		seeds, err := s.seedEntities(ctx, tx, tenantID, req.Query, 10)
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

// seedEntities finds seed entity IDs for the given query.
//
// Strategy (cosine-first with ILIKE fallback):
//  1. Empty query → ILIKE path (returns most recent entities, existing behavior).
//  2. Non-empty query with embedder + vectorStore available → embed the query,
//     call SearchNearest, filter by cosineSeedThreshold.
//     If cosine returns at least one result, return those IDs.
//  3. Fallback to ILIKE when:
//     - embedder or vectorStore is nil, OR
//     - embedder call fails (e.g. service unreachable), OR
//     - cosine returns zero results above threshold.
//
// The ILIKE fallback is always graceful — a down embedder must never 500 callers.
func (s Service) seedEntities(ctx context.Context, tx pgx.Tx, tenantID, query string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 10
	}

	// Cosine path: only attempted for non-empty queries with both deps wired.
	if query != "" && s.embedder != nil && s.vectorStore != nil {
		ids, err := s.cosineSeed(ctx, tenantID, query, limit)
		if err == nil && len(ids) > 0 {
			return ids, nil
		}
		// err != nil means embedder unreachable or SearchNearest failed — fall through to ILIKE.
		// len(ids) == 0 means nothing above threshold — fall through to ILIKE.
	}

	return ilikeSeed(ctx, tx, tenantID, query, limit)
}

// cosineSeed embeds the query and searches for nearest entities by cosine similarity.
// Returns entity IDs whose similarity score exceeds cosineSeedThreshold.
// Returns a non-nil error only when the embedder itself fails (network/HTTP error);
// callers treat any error here as a signal to fall back to ILIKE.
func (s Service) cosineSeed(ctx context.Context, tenantID, query string, limit int) ([]string, error) {
	var tenantUUID pgtype.UUID
	if err := tenantUUID.Scan(tenantID); err != nil {
		return nil, fmt.Errorf("retrieval: invalid tenant id for cosine seed: %w", err)
	}

	vecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("retrieval: embed query: %w", err)
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("retrieval: embedder returned empty vector")
	}

	results, err := s.vectorStore.SearchNearest(ctx, tenantUUID, vecs[0], limit)
	if err != nil {
		return nil, fmt.Errorf("retrieval: SearchNearest: %w", err)
	}

	ids := make([]string, 0, len(results))
	for _, r := range results {
		if r.Score >= cosineSeedThreshold {
			ids = append(ids, uuidToString(r.Entity.ID))
		}
	}
	return ids, nil
}

// ilikeSeed is the original ILIKE-based seed lookup. Unchanged behavior:
// empty query returns most-recent entities; non-empty query filters by label/description.
func ilikeSeed(ctx context.Context, tx pgx.Tx, tenantID, query string, limit int) ([]string, error) {
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

// uuidToString converts a pgtype.UUID to its string representation.
func uuidToString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	b := id.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
