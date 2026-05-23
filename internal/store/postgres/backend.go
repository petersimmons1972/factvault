// Package postgres implements store interfaces using pgx and Postgres.
package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/store"
)

var errInvalidLimit = errors.New("limit must be greater than zero")

// Store is the Postgres implementation of the backend-neutral store interfaces.
type Store struct {
	queries *db.Queries
	db      db.DBTX
}

// New returns a Postgres-backed Store using a pgx pool.
func New(pool *pgxpool.Pool) *Store {
	return NewWithDBTX(pool)
}

// NewWithDBTX returns a Postgres-backed Store over a pool, connection, or transaction.
func NewWithDBTX(conn db.DBTX) *Store {
	return &Store{
		queries: db.New(conn),
		db:      conn,
	}
}

// WithTx returns a Store bound to tx.
func (s *Store) WithTx(tx pgx.Tx) *Store {
	return NewWithDBTX(tx)
}

// GetEntity delegates to the generated sqlc query.
func (s *Store) GetEntity(ctx context.Context, id pgtype.UUID) (db.Entity, error) {
	return s.queries.GetEntity(ctx, id)
}

// ListEntitiesByTenant delegates to the generated sqlc query.
func (s *Store) ListEntitiesByTenant(ctx context.Context, tenantID pgtype.UUID) ([]db.Entity, error) {
	return s.queries.ListEntitiesByTenant(ctx, tenantID)
}

// ListPropertiesByTenant delegates to the generated sqlc query.
func (s *Store) ListPropertiesByTenant(ctx context.Context, tenantID pgtype.UUID) ([]db.Property, error) {
	return s.queries.ListPropertiesByTenant(ctx, tenantID)
}

// ListQualifiersByStatement delegates to the generated sqlc query.
func (s *Store) ListQualifiersByStatement(ctx context.Context, statementID pgtype.UUID) ([]db.Qualifier, error) {
	return s.queries.ListQualifiersByStatement(ctx, statementID)
}

// ListSourcesByTenant delegates to the generated sqlc query.
func (s *Store) ListSourcesByTenant(ctx context.Context, tenantID pgtype.UUID) ([]db.Source, error) {
	return s.queries.ListSourcesByTenant(ctx, tenantID)
}

// ListStatementsBySubject delegates to the generated sqlc query.
func (s *Store) ListStatementsBySubject(ctx context.Context, subjectID pgtype.UUID) ([]db.Statement, error) {
	return s.queries.ListStatementsBySubject(ctx, subjectID)
}

// SearchNearest returns the nearest tenant-scoped entities by cosine similarity.
func (s *Store) SearchNearest(ctx context.Context, tenantID pgtype.UUID, embedding []float32, k int) ([]store.EntityWithScore, error) {
	if k <= 0 {
		return nil, errInvalidLimit
	}

	rows, err := s.db.Query(ctx, `
SELECT id, tenant_id, ext_id, label, type_uri, description, embedding, meta, created_at, updated_at,
       1 - (embedding <=> $2) AS score
FROM entities
WHERE tenant_id = $1
  AND embedding IS NOT NULL
ORDER BY embedding <=> $2
LIMIT $3
`, tenantID, pgvector.NewVector(embedding), k)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []store.EntityWithScore
	for rows.Next() {
		var result store.EntityWithScore
		if err := rows.Scan(
			&result.Entity.ID,
			&result.Entity.TenantID,
			&result.Entity.ExtID,
			&result.Entity.Label,
			&result.Entity.TypeUri,
			&result.Entity.Description,
			&result.Entity.Embedding,
			&result.Entity.Meta,
			&result.Entity.CreatedAt,
			&result.Entity.UpdatedAt,
			&result.Score,
		); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
