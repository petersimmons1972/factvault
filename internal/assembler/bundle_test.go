package assembler

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/petersimmons1972/factvault/internal/testdb"
)

func TestAssembleDossier(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()

	// Create a test tenant and entity
	tenantID := uuid.NewString()
	entityID := uuid.NewString()

	// Begin transaction with tenant context
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	// Set tenant context for RLS
	_, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID)
	if err != nil {
		t.Fatalf("failed to set tenant context: %v", err)
	}

	// Insert test entity
	_, err = tx.Exec(ctx, `
		INSERT INTO entities (id, tenant_id, ext_id, label, type_uri, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, now(), now())
	`, entityID, tenantID, "test-entity", "Test Entity", "https://schema.org/Thing")
	if err != nil {
		t.Fatalf("failed to insert entity: %v", err)
	}

	// Test: Assemble dossier for the entity (depth=0)
	bundle, err := Assemble(ctx, tx, []string{entityID}, 0, tenantID)
	if err != nil {
		t.Errorf("Assemble failed: %v", err)
	}

	if bundle == nil {
		t.Errorf("expected non-nil bundle")
	} else {
		if bundle.EntityID != entityID {
			t.Errorf("expected EntityID %s, got %s", entityID, bundle.EntityID)
		}
		if bundle.TenantID != tenantID {
			t.Errorf("expected TenantID %s, got %s", tenantID, bundle.TenantID)
		}
		if len(bundle.Entities) != 1 {
			t.Errorf("expected 1 entity, got %d", len(bundle.Entities))
		}
		if len(bundle.Entities) > 0 && bundle.Entities[0].ID != entityID {
			t.Errorf("expected entity ID %s, got %s", entityID, bundle.Entities[0].ID)
		}
	}
}

func TestAssembleEntityNotFound(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()

	tenantID := uuid.NewString()
	nonexistentEntityID := uuid.NewString()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID)
	if err != nil {
		t.Fatalf("failed to set tenant context: %v", err)
	}

	bundle, err := Assemble(ctx, tx, []string{nonexistentEntityID}, 0, tenantID)

	if err == nil {
		t.Errorf("expected error for non-existent entity, got nil")
	}
	if bundle != nil {
		t.Errorf("expected nil bundle on error, got %v", bundle)
	}
}

func TestAssembleInvalidDepth(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()

	tenantID := uuid.NewString()
	entityID := uuid.NewString()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID)
	if err != nil {
		t.Fatalf("failed to set tenant context: %v", err)
	}

	tests := []struct {
		depth int
		valid bool
	}{
		{-1, false},
		{0, true},
		{1, true},
		{2, true},
		{3, true},
		{4, false},
	}

	for _, tt := range tests {
		_, err := Assemble(ctx, tx, []string{entityID}, tt.depth, tenantID)
		if tt.valid {
			if errors.Is(err, ErrInvalidDepth) {
				t.Errorf("depth %d should be valid, got ErrInvalidDepth", tt.depth)
			}
		} else {
			if !errors.Is(err, ErrInvalidDepth) {
				t.Errorf("depth %d should be invalid, expected ErrInvalidDepth, got %v", tt.depth, err)
			}
		}
	}
}

func TestAssembleEmptyEntityList(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()

	tenantID := uuid.NewString()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID)
	if err != nil {
		t.Fatalf("failed to set tenant context: %v", err)
	}

	_, err = Assemble(ctx, tx, []string{}, 0, tenantID)

	if !errors.Is(err, ErrInvalidEntityCount) {
		t.Errorf("expected ErrInvalidEntityCount, got %v", err)
	}
}
