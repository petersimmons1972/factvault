// Package store defines backend-neutral persistence interfaces.
package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
)

// Store is the backend-neutral domain query surface.
type Store interface {
	db.Querier
}

// EntityWithScore is a vector search result with backend-specific distance normalized to a score.
type EntityWithScore struct {
	Entity db.Entity
	Score  float64
}

// VectorStore abstracts ANN lookups over entity embeddings.
type VectorStore interface {
	SearchNearest(ctx context.Context, tenantID pgtype.UUID, embedding []float32, k int) ([]EntityWithScore, error)
}
