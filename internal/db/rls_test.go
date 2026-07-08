package db_test

import (
	"context"
	"strings"
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
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Logf("rollback (expected after commit): %v", err)
		}
	}()

	var got string
	if err := tx.QueryRow(ctx, "SELECT current_setting('app.tenant_id', true)").Scan(&got); err != nil {
		t.Fatalf("QueryRow GUC: %v", err)
	}
	if got != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("unexpected app.tenant_id: %q", got)
	}
	var currentUser string
	if err := tx.QueryRow(ctx, "SELECT current_user").Scan(&currentUser); err != nil {
		t.Fatalf("QueryRow current_user: %v", err)
	}
	if currentUser != "app_user" {
		t.Fatalf("expected role app_user, got %q", currentUser)
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
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Logf("rollback (expected after commit): %v", err)
		}
	}()

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

func TestTenantContext_ListPropertiesByTenantIncludesGlobalRows(t *testing.T) {
	pool := testdb.New(t)
	setup := context.Background()

	tenantA := mustUUID(t, "11111111-1111-1111-1111-111111111111")
	tenantB := mustUUID(t, "22222222-2222-2222-2222-222222222222")

	_, err := pool.Exec(setup, `
		INSERT INTO properties (id, tenant_id, slug, label, value_type)
		VALUES
			($1, NULL, 'alpha_global', 'Alpha global', 'string'),
			($2, $3, 'bravo_tenant', 'Bravo tenant', 'string'),
			($4, $5, 'charlie_other', 'Charlie other', 'string')
	`,
		"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1",
		"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbb2", tenantA,
		"cccccccc-cccc-cccc-cccc-ccccccccccc3", tenantB,
	)
	if err != nil {
		t.Fatalf("setup insert properties: %v", err)
	}

	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, tenantA)
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Logf("rollback (expected after commit): %v", err)
		}
	}()

	properties, err := db.New(tx).ListPropertiesByTenant(ctx, tenantA)
	if err != nil {
		t.Fatalf("ListPropertiesByTenant: %v", err)
	}

	if len(properties) != 2 {
		t.Fatalf("properties len=%d want 2", len(properties))
	}
	if properties[0].Slug != "alpha_global" || properties[0].TenantID.Valid {
		t.Fatalf("properties[0]=%+v want global alpha_global", properties[0])
	}
	if properties[1].Slug != "bravo_tenant" || !properties[1].TenantID.Valid || properties[1].TenantID != tenantA {
		t.Fatalf("properties[1]=%+v want tenant bravo_tenant", properties[1])
	}
}

func TestTenantContext_PropertiesRejectGlobalMutations(t *testing.T) {
	pool := testdb.New(t)
	setup := context.Background()

	tenantA := mustUUID(t, "11111111-1111-1111-1111-111111111111")
	globalID := "dddddddd-dddd-dddd-dddd-ddddddddddd4"

	if _, err := pool.Exec(setup, `
		INSERT INTO properties (id, tenant_id, slug, label, value_type)
		VALUES ($1, NULL, 'global_lock', 'Global lock', 'string')
	`, globalID); err != nil {
		t.Fatalf("setup insert global property: %v", err)
	}

	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, tenantA)
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Logf("rollback (expected after commit): %v", err)
		}
	}()

	_, err = tx.Exec(ctx, `
		INSERT INTO properties (tenant_id, slug, label, value_type)
		VALUES (NULL, 'tenant_cannot_create_global', 'Tenant cannot create global', 'string')
	`)
	if err == nil || !strings.Contains(err.Error(), "row-level security policy") {
		t.Fatalf("insert global property err=%v want row-level security policy error", err)
	}

	updateTag, err := tx.Exec(ctx, `
		UPDATE properties
		SET label = 'Updated global'
		WHERE id = $1
	`, globalID)
	if err != nil {
		t.Fatalf("update global property: %v", err)
	}
	if updateTag.RowsAffected() != 0 {
		t.Fatalf("updated %d global rows want 0", updateTag.RowsAffected())
	}

	deleteTag, err := tx.Exec(ctx, `DELETE FROM properties WHERE id = $1`, globalID)
	if err != nil {
		t.Fatalf("delete global property: %v", err)
	}
	if deleteTag.RowsAffected() != 0 {
		t.Fatalf("deleted %d global rows want 0", deleteTag.RowsAffected())
	}
}
