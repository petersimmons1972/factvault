package postgres

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	pgvector "github.com/pgvector/pgvector-go"

	"github.com/petersimmons1972/factvault/internal/store"
	"github.com/petersimmons1972/factvault/internal/testdb"
)

func TestPostgresStoreImplementsInterfaces(_ *testing.T) {
	var _ store.Store = (*Store)(nil)
	var _ store.VectorStore = (*Store)(nil)
}

func TestPostgresStoreWrapsSQLCQueries(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	tenantID := uuidFromString("00000000-0000-0000-0000-000000000091")

	if _, err := pool.Exec(ctx, `INSERT INTO entities (tenant_id, ext_id, label, embedding) VALUES ($1, 'store-test-acme', 'Acme', $2)`, tenantID, pgvector.NewVector(vectorWith(1, 0))); err != nil {
		t.Fatalf("insert entity: %v", err)
	}

	entities, err := New(pool).ListEntitiesByTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("ListEntitiesByTenant: %v", err)
	}
	if len(entities) != 1 || entities[0].Label != "Acme" {
		t.Fatalf("expected wrapped sqlc query to return Acme, got %#v", entities)
	}
}

func TestPostgresStoreSearchNearest(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	tenantID := uuidFromString("00000000-0000-0000-0000-000000000092")
	otherTenantID := uuidFromString("00000000-0000-0000-0000-000000000093")

	near := vectorWith(1, 0)
	far := vectorWith(0, 1)
	if _, err := pool.Exec(ctx, `INSERT INTO entities (tenant_id, ext_id, label, embedding) VALUES ($1, 'store-test-near', 'Near', $2), ($1, 'store-test-far', 'Far', $3), ($4, 'store-test-other', 'Other Tenant', $2)`, tenantID, pgvector.NewVector(near), pgvector.NewVector(far), otherTenantID); err != nil {
		t.Fatalf("insert entities: %v", err)
	}

	got, err := New(pool).SearchNearest(ctx, tenantID, near, 1)
	if err != nil {
		t.Fatalf("SearchNearest: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one result, got %d", len(got))
	}
	if got[0].Entity.Label != "Near" {
		t.Fatalf("expected nearest tenant entity, got %q", got[0].Entity.Label)
	}
	if got[0].Score < 0.99 {
		t.Fatalf("expected near-identical score, got %f", got[0].Score)
	}
}

func uuidFromString(s string) pgtype.UUID {
	var id pgtype.UUID
	if err := id.Scan(s); err != nil {
		panic(err)
	}
	return id
}

func vectorWith(first, second float32) []float32 {
	v := make([]float32, 1024)
	v[0] = first
	v[1] = second
	return v
}
