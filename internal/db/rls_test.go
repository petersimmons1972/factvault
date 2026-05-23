package db_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/testdb"
)

func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(s); err != nil {
		t.Fatalf("scan UUID: %v", err)
	}
	return id
}

func TestTenantContext_GUCIsSet(t *testing.T) {
	pool := testdb.New(t)
	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, mustUUID(t, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"))
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}
	defer tx.Rollback(ctx)

	var got string
	if err := tx.QueryRow(ctx, "SELECT current_setting('app.tenant_id', true)").Scan(&got); err != nil {
		t.Fatalf("QueryRow GUC: %v", err)
	}
	if got != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("unexpected app.tenant_id: %q", got)
	}
}

func TestTenantContext_RLSFiltersRows(t *testing.T) {
	pool := testdb.New(t)
	setup := context.Background()

	_, err := pool.Exec(
		setup, "INSERT INTO entities (tenant_id, label) VALUES ($1, 'Alpha Corp'), ($2, 'Beta Corp')",
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
	)
	if err != nil {
		t.Fatalf("setup insert: %v", err)
	}

	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, mustUUID(t, "11111111-1111-1111-1111-111111111111"))
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE app_user"); err != nil {
		t.Fatalf("SET LOCAL ROLE app_user: %v", err)
	}

	rows, err := tx.Query(ctx, "SELECT label FROM entities ORDER BY label")
	if err != nil {
		t.Fatalf("query entities: %v", err)
	}
	defer rows.Close()

	var labels []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			t.Fatalf("scan: %v", err)
		}
		labels = append(labels, label)
	}
	if len(labels) != 1 || labels[0] != "Alpha Corp" {
		t.Fatalf("unexpected labels: %v", labels)
	}
}
