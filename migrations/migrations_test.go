package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	neturl "net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/petersimmons1972/factvault/internal/testdb"
)

// startMigrationsDB spins up a throwaway Postgres container and returns a ready
// *sql.DB handle. Cleanup (purge + close) is registered on t.
func startMigrationsDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	adminDSN := pool.Config().ConnString()
	dbName := fmt.Sprintf("factvault_migrations_%d", time.Now().UnixNano())
	if _, err := pool.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", dbName)); err != nil {
		t.Fatalf("create database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `
			SELECT pg_terminate_backend(pid)
			FROM pg_stat_activity
			WHERE datname = $1 AND pid <> pg_backend_pid()
		`, dbName); err != nil {
			t.Logf("terminate %s connections: %v", dbName, err)
		}
		if _, err := pool.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbName)); err != nil {
			t.Logf("drop database %s: %v", dbName, err)
		}
	})

	dsn, err := databaseDSN(adminDSN, dbName)
	if err != nil {
		t.Fatalf("databaseDSN: %v", err)
	}

	var db *sql.DB
	if db, err = sql.Open("pgx", dsn); err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Logf("db.Close: %v", err)
		}
	})

	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose.SetDialect: %v", err)
	}
	return db
}

func databaseDSN(adminDSN, dbName string) (string, error) {
	parsed, err := neturl.Parse(adminDSN)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + dbName
	return parsed.String(), nil
}

// sourcesMetaColumnExists reports whether the sources.meta column is present.
func sourcesMetaColumnExists(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(
		context.Background(),
		"SELECT EXISTS (SELECT FROM information_schema.columns WHERE table_name = 'sources' AND column_name = 'meta')",
	).Scan(&exists)
	if err != nil {
		t.Fatalf("checking sources.meta column: %v", err)
	}
	return exists
}

func TestMigrationsRunClean(t *testing.T) {
	db := startMigrationsDB(t)

	if err := goose.RunContext(context.Background(), "up", db, "."); err != nil {
		t.Fatalf("goose up: %v", err)
	}

	tables := []string{
		"entities", "properties", "statements", "qualifiers", "relations",
		"sources", "statement_sources", "source_verifications",
		"proposed_properties", "dossiers",
	}
	for _, tbl := range tables {
		var exists bool
		err := db.QueryRowContext(context.Background(), "SELECT EXISTS (SELECT FROM pg_tables WHERE tablename = $1)", tbl).Scan(&exists)
		if err != nil {
			t.Fatalf("checking table %s: %v", tbl, err)
		}
		if !exists {
			t.Errorf("table %s missing", tbl)
		}
	}

	indices := []string{
		"idx_entities_embedding", "idx_statements_embedding", "idx_relations_embedding", "idx_sources_embedding",
	}
	for _, idx := range indices {
		var exists bool
		err := db.QueryRowContext(context.Background(), "SELECT EXISTS (SELECT FROM pg_indexes WHERE indexname = $1)", idx).Scan(&exists)
		if err != nil {
			t.Fatalf("checking index %s: %v", idx, err)
		}
		if !exists {
			t.Errorf("index %s missing", idx)
		}
	}

	// The migration creates app_user as NOLOGIN (no password) so that the GRANTs
	// in the schema succeed. In production environments the role is created WITH
	// LOGIN and a real password by the init layer BEFORE migrations run; the
	// IF NOT EXISTS guard in the migration is a no-op in that case.
	var roleExists bool
	if err := db.QueryRowContext(context.Background(), "SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = 'app_user')").Scan(&roleExists); err != nil {
		t.Fatalf("checking role: %v", err)
	}
	if !roleExists {
		t.Error("app_user role missing")
	}

	// Verify the migration does NOT embed a hardcoded password.
	// pg_authid.rolpassword is NULL for roles created without a password.
	var hasPassword bool
	if err := db.QueryRowContext(
		context.Background(),
		"SELECT rolpassword IS NOT NULL FROM pg_authid WHERE rolname = 'app_user'",
	).Scan(&hasPassword); err != nil {
		t.Fatalf("checking app_user password: %v", err)
	}
	if hasPassword {
		t.Error("app_user must not have a password set by the migration (hardcoded credentials are prohibited)")
	}

	var viewExists bool
	if err := db.QueryRowContext(context.Background(), "SELECT EXISTS (SELECT FROM pg_views WHERE viewname = 'v_conflicts')").Scan(&viewExists); err != nil {
		t.Fatalf("checking view: %v", err)
	}
	if !viewExists {
		t.Error("v_conflicts view missing")
	}
}

// TestMigration00005_RollsBack guards the down stanza of 00005_sources_meta.sql:
// up-to-latest → down-to-4 (reverts 00007, 00006, then 00005) → assert column
// gone → up (re-applies later migrations) → assert column back. Without this,
// the rollback path is only ever exercised by hand.
//
// Note: "down" rolls back exactly ONE step (the current latest). We use "down-to 4"
// so that all migrations above version 4 (currently 00005, 00006, and 00007) are
// rolled back, ensuring 00005's down stanza is always exercised regardless of how
// many later migrations exist.
func TestMigration00005_RollsBack(t *testing.T) {
	db := startMigrationsDB(t)

	// up to latest: meta column must be present.
	if err := goose.RunContext(context.Background(), "up", db, "."); err != nil {
		t.Fatalf("goose up: %v", err)
	}
	if !sourcesMetaColumnExists(t, db) {
		t.Fatal("sources.meta column missing after goose up")
	}

	// down to version 4: reverts all migrations above 00004, including 00005.
	// Meta column must be gone.
	if err := goose.RunContext(context.Background(), "down-to", db, ".", "4"); err != nil {
		t.Fatalf("goose down-to 4: %v", err)
	}
	if sourcesMetaColumnExists(t, db) {
		t.Fatal("sources.meta column still present after rolling back 00005")
	}

	// re-up: all migrations re-apply cleanly and the column returns.
	if err := goose.RunContext(context.Background(), "up", db, "."); err != nil {
		t.Fatalf("goose up (re-apply): %v", err)
	}
	if !sourcesMetaColumnExists(t, db) {
		t.Fatal("sources.meta column missing after re-applying 00005")
	}
}

func TestTenantScopedForeignKeysRejectCrossTenantReferences(t *testing.T) {
	db := startMigrationsDB(t)
	ctx := context.Background()

	if err := goose.RunContext(ctx, "up", db, "."); err != nil {
		t.Fatalf("goose up: %v", err)
	}

	tenantA := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	tenantB := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	entityA1 := "10000000-0000-0000-0000-000000000001"
	entityA2 := "10000000-0000-0000-0000-000000000002"
	entityB1 := "20000000-0000-0000-0000-000000000001"
	sourceA := "30000000-0000-0000-0000-000000000001"
	sourceB := "40000000-0000-0000-0000-000000000001"
	globalProperty := "50000000-0000-0000-0000-000000000001"
	tenantStringPropertyA := "50000000-0000-0000-0000-000000000002"
	tenantStringPropertyB := "50000000-0000-0000-0000-000000000003"
	tenantEntityPropertyA := "50000000-0000-0000-0000-000000000004"
	statementA := "60000000-0000-0000-0000-000000000001"
	statementB := "60000000-0000-0000-0000-000000000002"

	fixtures := []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO entities (id, tenant_id, label) VALUES
				($1, $2, 'Tenant A Subject'),
				($3, $2, 'Tenant A Value'),
				($4, $5, 'Tenant B Entity')`,
			args: []any{entityA1, tenantA, entityA2, entityB1, tenantB},
		},
		{
			query: `INSERT INTO properties (id, tenant_id, slug, label, value_type) VALUES
				($1, NULL, 'global_note', 'Global note', 'string'),
				($2, $4, 'tenant_a_note', 'Tenant A note', 'string'),
				($3, $5, 'tenant_b_note', 'Tenant B note', 'string'),
				($6, $4, 'tenant_a_ref', 'Tenant A ref', 'entity_ref')`,
			args: []any{
				globalProperty,
				tenantStringPropertyA,
				tenantStringPropertyB,
				tenantA,
				tenantB,
				tenantEntityPropertyA,
			},
		},
		{
			query: `INSERT INTO sources (id, tenant_id, url, content_hash, status) VALUES
				($1, $2, 'https://example.com/a', 'hash-a', 'archived'),
				($3, $4, 'https://example.com/b', 'hash-b', 'archived')`,
			args: []any{sourceA, tenantA, sourceB, tenantB},
		},
		{
			query: `INSERT INTO statements (id, tenant_id, subject_id, property_id, val_text, confidence)
				VALUES ($1, $2, $3, $4, 'same-tenant', 0.900)`,
			args: []any{statementA, tenantA, entityA1, tenantStringPropertyA},
		},
		{
			query: `INSERT INTO statements (id, tenant_id, subject_id, property_id, val_text, confidence)
				VALUES ($1, $2, $3, $4, 'other-tenant', 0.800)`,
			args: []any{statementB, tenantB, entityB1, tenantStringPropertyB},
		},
	}
	for _, fixture := range fixtures {
		if _, err := db.ExecContext(ctx, fixture.query, fixture.args...); err != nil {
			t.Fatalf("fixture insert failed for %q: %v", fixture.query, err)
		}
	}

	t.Run("same tenant statement accepts tenant property", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `
			INSERT INTO statements (id, tenant_id, subject_id, property_id, val_text, confidence)
			VALUES ($1, $2, $3, $4, 'tenant-local', 0.750)
		`, "60000000-0000-0000-0000-000000000003", tenantA, entityA2, tenantStringPropertyA)
		if err != nil {
			t.Fatalf("same-tenant statement insert: %v", err)
		}
	})

	t.Run("same tenant statement accepts global property", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `
			INSERT INTO statements (id, tenant_id, subject_id, property_id, val_text, confidence)
			VALUES ($1, $2, $3, $4, 'global-ok', 0.750)
		`, "60000000-0000-0000-0000-000000000004", tenantA, entityA2, globalProperty)
		if err != nil {
			t.Fatalf("global-property statement insert: %v", err)
		}
	})

	t.Run("cross tenant statement subject rejected", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `
			INSERT INTO statements (id, tenant_id, subject_id, property_id, val_text, confidence)
			VALUES ($1, $2, $3, $4, 'bad-subject', 0.500)
		`, "60000000-0000-0000-0000-000000000011", tenantA, entityB1, tenantStringPropertyA)
		expectPGCode(t, err, "23503")
	})

	t.Run("cross tenant statement property rejected", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `
			INSERT INTO statements (id, tenant_id, subject_id, property_id, val_text, confidence)
			VALUES ($1, $2, $3, $4, 'bad-property', 0.500)
		`, "60000000-0000-0000-0000-000000000012", tenantA, entityA1, tenantStringPropertyB)
		expectPGCode(t, err, "23503")
	})

	t.Run("cross tenant statement value entity rejected", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `
			INSERT INTO statements (id, tenant_id, subject_id, property_id, val_entity, confidence)
			VALUES ($1, $2, $3, $4, $5, 0.500)
		`, "60000000-0000-0000-0000-000000000013", tenantA, entityA1, tenantEntityPropertyA, entityB1)
		expectPGCode(t, err, "23503")
	})

	t.Run("same tenant relation accepted", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `
			INSERT INTO relations (id, tenant_id, source_id, target_id, type, confidence)
			VALUES ($1, $2, $3, $4, 'ally', 0.900)
		`, "70000000-0000-0000-0000-000000000001", tenantA, entityA1, entityA2)
		if err != nil {
			t.Fatalf("same-tenant relation insert: %v", err)
		}
	})

	t.Run("cross tenant relation rejected", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `
			INSERT INTO relations (id, tenant_id, source_id, target_id, type, confidence)
			VALUES ($1, $2, $3, $4, 'ally', 0.900)
		`, "70000000-0000-0000-0000-000000000002", tenantA, entityA1, entityB1)
		expectPGCode(t, err, "23503")
	})

	t.Run("same tenant statement source accepted", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `
			INSERT INTO statement_sources (
				id, statement_id, source_id, excerpt, excerpt_offset_start, excerpt_offset_end, extraction_method, confidence, tenant_id
			) VALUES ($1, $2, $3, 'same-tenant', 0, 11, 'test', 0.900, $4)
		`, "80000000-0000-0000-0000-000000000001", statementA, sourceA, tenantA)
		if err != nil {
			t.Fatalf("same-tenant statement_source insert: %v", err)
		}
	})

	t.Run("cross tenant statement source statement rejected", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `
			INSERT INTO statement_sources (
				id, statement_id, source_id, excerpt, excerpt_offset_start, excerpt_offset_end, extraction_method, confidence, tenant_id
			) VALUES ($1, $2, $3, 'bad-statement', 0, 13, 'test', 0.900, $4)
		`, "80000000-0000-0000-0000-000000000002", statementB, sourceA, tenantA)
		expectPGCode(t, err, "23503")
	})

	t.Run("cross tenant statement source source rejected", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `
			INSERT INTO statement_sources (
				id, statement_id, source_id, excerpt, excerpt_offset_start, excerpt_offset_end, extraction_method, confidence, tenant_id
			) VALUES ($1, $2, $3, 'bad-source', 0, 10, 'test', 0.900, $4)
		`, "80000000-0000-0000-0000-000000000003", statementA, sourceB, tenantA)
		expectPGCode(t, err, "23503")
	})
}

// TestAlreadyMigratedDB_GetsPropertiesRLSPolicies mirrors the up-to-N-1 then
// up pattern used for the cross-tenant FK constraints migration (#283): a
// database already migrated up to the version immediately before the
// properties RLS fix (00008) must, on a subsequent "up", pick up the new
// table-specific policies on `properties` — same assurance a rolling
// production deploy needs (existing DBs get the fix, not just fresh ones).
func TestAlreadyMigratedDB_GetsPropertiesRLSPolicies(t *testing.T) {
	db := startMigrationsDB(t)
	ctx := context.Background()

	if err := goose.RunContext(ctx, "up-to", db, ".", "7"); err != nil {
		t.Fatalf("goose up-to 7: %v", err)
	}
	if propertiesPolicyExists(t, db, "properties_select_tenant_or_global") {
		t.Fatal("properties_select_tenant_or_global policy present before migration 00008 applied")
	}

	if err := goose.RunContext(ctx, "up", db, "."); err != nil {
		t.Fatalf("goose up: %v", err)
	}

	for _, policy := range []string{
		"properties_select_tenant_or_global",
		"properties_insert_tenant_only",
		"properties_update_tenant_only",
		"properties_delete_tenant_only",
	} {
		if !propertiesPolicyExists(t, db, policy) {
			t.Errorf("policy %s missing after goose up", policy)
		}
	}
	if propertiesPolicyExists(t, db, "tenant_isolation") {
		t.Error("generic tenant_isolation policy still present on properties after migration 00008")
	}
}

func propertiesPolicyExists(t *testing.T, db *sql.DB, policyName string) bool {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(
		context.Background(),
		"SELECT EXISTS (SELECT FROM pg_policies WHERE tablename = 'properties' AND policyname = $1)",
		policyName,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("checking policy %s: %v", policyName, err)
	}
	return exists
}

func expectPGCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected postgres error code %s, got nil", wantCode)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected postgres error code %s, got %T: %v", wantCode, err, err)
	}
	if pgErr.Code != wantCode {
		t.Fatalf("postgres error code=%s want %s (%v)", pgErr.Code, wantCode, err)
	}
}
