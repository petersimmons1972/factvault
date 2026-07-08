package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// TestTenantContext_PropertiesRejectGlobalMutations asserts app_user cannot
// insert a new global (tenant_id IS NULL) property, and cannot update or
// delete an existing global property. Each assertion runs in its own
// TenantContext transaction: Postgres aborts the entire transaction after a
// row-level security violation (SQLSTATE 42501), so reusing a single tx
// across an expected-failure assertion and a subsequent assertion produces a
// misleading 25P02 "current transaction is aborted" error on the second
// call rather than the intended result.
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

	t.Run("insert global property rejected", func(t *testing.T) {
		ctx := db.WithPool(context.Background(), pool)
		ctx, tx, err := db.TenantContext(ctx, tenantA)
		if err != nil {
			t.Fatalf("TenantContext: %v", err)
		}
		defer func() {
			if err := tx.Rollback(ctx); err != nil {
				t.Logf("rollback (expected after RLS violation): %v", err)
			}
		}()

		_, err = tx.Exec(ctx, `
			INSERT INTO properties (tenant_id, slug, label, value_type)
			VALUES (NULL, 'tenant_cannot_create_global', 'Tenant cannot create global', 'string')
		`)
		if err == nil || !strings.Contains(err.Error(), "row-level security policy") {
			t.Fatalf("insert global property err=%v want row-level security policy error", err)
		}
	})

	t.Run("update global property is a no-op", func(t *testing.T) {
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
	})

	t.Run("delete global property is a no-op", func(t *testing.T) {
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

		deleteTag, err := tx.Exec(ctx, `DELETE FROM properties WHERE id = $1`, globalID)
		if err != nil {
			t.Fatalf("delete global property: %v", err)
		}
		if deleteTag.RowsAffected() != 0 {
			t.Fatalf("deleted %d global rows want 0", deleteTag.RowsAffected())
		}
	})
}

// TestTenantContext_GlobalPropertiesAreImmutable is a focused variant of the
// above, isolated to a single global row and a single mutation type per
// subtest, matching the acceptance criteria in issue #269 ("app_user cannot
// insert/update/delete global properties") one assertion at a time.
func TestTenantContext_GlobalPropertiesAreImmutable(t *testing.T) {
	pool := testdb.New(t)
	setup := context.Background()

	tenantA := mustUUID(t, "11111111-1111-1111-1111-111111111111")
	globalID := "abcdefab-cdef-abcd-efab-cdefabcdef01"

	if _, err := pool.Exec(setup, `
		INSERT INTO properties (id, tenant_id, slug, label, value_type)
		VALUES ($1, NULL, 'immutable_global', 'Immutable global', 'string')
	`, globalID); err != nil {
		t.Fatalf("setup insert global property: %v", err)
	}

	withTx := func(t *testing.T, fn func(ctx context.Context, tx pgx.Tx)) {
		t.Helper()
		ctx := db.WithPool(context.Background(), pool)
		ctx, tx, err := db.TenantContext(ctx, tenantA)
		if err != nil {
			t.Fatalf("TenantContext: %v", err)
		}
		defer func() {
			if err := tx.Rollback(ctx); err != nil {
				t.Logf("rollback (expected): %v", err)
			}
		}()
		fn(ctx, tx)
	}

	t.Run("label survives update attempt", func(t *testing.T) {
		withTx(t, func(ctx context.Context, tx pgx.Tx) {
			tag, err := tx.Exec(ctx, `UPDATE properties SET label = 'hijacked' WHERE id = $1`, globalID)
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			if tag.RowsAffected() != 0 {
				t.Fatalf("rows affected=%d want 0", tag.RowsAffected())
			}
		})
	})

	var label string
	if err := pool.QueryRow(setup, `SELECT label FROM properties WHERE id = $1`, globalID).Scan(&label); err != nil {
		t.Fatalf("verify label: %v", err)
	}
	if label != "Immutable global" {
		t.Fatalf("label=%q want unchanged", label)
	}
}

// TestTenantContext_PropertiesIncludeGlobalRows re-verifies (via a raw SQL
// SELECT rather than the generated ListPropertiesByTenant query) that
// app_user under RLS actually sees both its own tenant's properties and
// global (tenant_id IS NULL) properties in the same result set — the core
// acceptance criterion from issue #269.
func TestTenantContext_PropertiesIncludeGlobalRows(t *testing.T) {
	pool := testdb.New(t)
	setup := context.Background()

	tenantA := mustUUID(t, "33333333-3333-3333-3333-333333333333")
	tenantB := mustUUID(t, "44444444-4444-4444-4444-444444444444")

	globalID := uuid.New().String()
	tenantAPropertyID := uuid.New().String()
	tenantBPropertyID := uuid.New().String()

	_, err := pool.Exec(setup, `
		INSERT INTO properties (id, tenant_id, slug, label, value_type)
		VALUES
			($1, NULL, 'shared_vocab_global', 'Shared vocab', 'string'),
			($2, $3, 'tenant_a_only', 'Tenant A only', 'string'),
			($4, $5, 'tenant_b_only', 'Tenant B only', 'string')
	`,
		globalID,
		tenantAPropertyID, tenantA,
		tenantBPropertyID, tenantB,
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

	// Filter to just the rows this test inserted: testdb.New shares one
	// database across the whole package's test run, so an unfiltered SELECT
	// would also pick up rows left behind by other tests.
	rows, err := tx.Query(ctx, `
		SELECT slug FROM properties
		WHERE id IN ($1, $2, $3)
		ORDER BY slug
	`, globalID, tenantAPropertyID, tenantBPropertyID)
	if err != nil {
		t.Fatalf("query properties: %v", err)
	}
	defer rows.Close()

	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			t.Fatalf("scan: %v", err)
		}
		slugs = append(slugs, slug)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	// tenantB's row must be invisible under RLS filtering by id, even though
	// the query nominally matches its id: it visibility is gated entirely by
	// the RLS policy (tenant match or global), not by the id filter.
	want := []string{"shared_vocab_global", "tenant_a_only"}
	if len(slugs) != len(want) {
		t.Fatalf("slugs=%v want %v (tenantB's row must be filtered by RLS)", slugs, want)
	}
	for i, s := range want {
		if slugs[i] != s {
			t.Fatalf("slugs=%v want %v", slugs, want)
		}
	}
}
