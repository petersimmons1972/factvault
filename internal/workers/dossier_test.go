package workers

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/petersimmons1972/factvault/internal/testdb"
)

func TestDossierWorkerPrecomputesDossier(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()
	tenantID := uuid.NewString()
	entityID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO entities (id, tenant_id, label) VALUES ($1, $2, 'Acme')`, entityID, tenantID); err != nil {
		t.Fatalf("insert entity: %v", err)
	}
	count, err := (DossierWorker{DB: pool}).RunOnce(ctx, DossierOptions{TenantID: tenantID, EntityID: entityID})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if count != 1 {
		t.Fatalf("count=%d want 1", count)
	}
	var got string
	if err := pool.QueryRow(ctx, `SELECT bundle->>'entity_id' FROM dossiers WHERE tenant_id=$1 AND entity_id=$2`, tenantID, entityID).Scan(&got); err != nil {
		t.Fatalf("select dossier: %v", err)
	}
	if got != entityID {
		t.Fatalf("entity_id=%q want %q", got, entityID)
	}
}
