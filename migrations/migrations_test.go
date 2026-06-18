package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/pressly/goose/v3"
)

// startMigrationsDB spins up a throwaway Postgres container and returns a ready
// *sql.DB handle. Cleanup (purge + close) is registered on t.
func startMigrationsDB(t *testing.T) *sql.DB {
	t.Helper()
	pool, err := dockertest.NewPool("")
	if err != nil {
		t.Fatalf("dockertest.NewPool: %v", err)
	}
	repository, tag := postgresImage()
	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository:   repository,
		Tag:          tag,
		ExposedPorts: []string{"5432/tcp"},
		Env: []string{
			"POSTGRES_USER=factvault",
			"POSTGRES_PASSWORD=factvault",
			"POSTGRES_DB=factvault",
			"POSTGRES_INITDB_ARGS=--no-sync",
		},
	}, func(cfg *docker.HostConfig) {
		cfg.AutoRemove = true
		cfg.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		t.Fatalf("pool.RunWithOptions: %v", err)
	}
	if err := resource.Expire(120); err != nil {
		t.Fatalf("resource.Expire: %v", err)
	}
	t.Cleanup(func() {
		if err := pool.Purge(resource); err != nil {
			t.Logf("pool.Purge: %v", err)
		}
	})

	dsn := fmt.Sprintf("postgres://factvault:factvault@localhost:%s/factvault?sslmode=disable", resource.GetPort("5432/tcp"))
	var db *sql.DB
	if err := pool.Retry(func() error {
		var openErr error
		db, openErr = sql.Open("pgx", dsn)
		if openErr != nil {
			return openErr
		}
		return db.PingContext(context.Background())
	}); err != nil {
		t.Fatalf("postgres not ready: %v", err)
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
// up→down(one step)→assert column gone→up→assert column back. Without this,
// the rollback path is only ever exercised by hand.
func TestMigration00005_RollsBack(t *testing.T) {
	db := startMigrationsDB(t)

	// up to latest: meta column must be present.
	if err := goose.RunContext(context.Background(), "up", db, "."); err != nil {
		t.Fatalf("goose up: %v", err)
	}
	if !sourcesMetaColumnExists(t, db) {
		t.Fatal("sources.meta column missing after goose up")
	}

	// down ONE step: 00005 reverts, meta column must be gone.
	if err := goose.RunContext(context.Background(), "down", db, "."); err != nil {
		t.Fatalf("goose down (one step): %v", err)
	}
	if sourcesMetaColumnExists(t, db) {
		t.Fatal("sources.meta column still present after rolling back 00005")
	}

	// re-up: migration re-applies cleanly and the column returns.
	if err := goose.RunContext(context.Background(), "up", db, "."); err != nil {
		t.Fatalf("goose up (re-apply): %v", err)
	}
	if !sourcesMetaColumnExists(t, db) {
		t.Fatal("sources.meta column missing after re-applying 00005")
	}
}

func postgresImage() (repository, tag string) {
	image := os.Getenv("FACTVAULT_TEST_POSTGRES_IMAGE")
	if image == "" {
		image = "ankane/pgvector:latest"
	}
	slash := strings.LastIndexByte(image, '/')
	colon := strings.LastIndexByte(image, ':')
	if colon > slash {
		return image[:colon], image[colon+1:]
	}
	return image, "latest"
}
