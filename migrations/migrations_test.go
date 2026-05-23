package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/pressly/goose/v3"
)

func TestMigrationsRunClean(t *testing.T) {
	pool, err := dockertest.NewPool("")
	if err != nil {
		t.Fatalf("dockertest.NewPool: %v", err)
	}

	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "pgvector/pgvector",
		Tag:        "pg16",
		Env: []string{
			"POSTGRES_USER=factvault",
			"POSTGRES_PASSWORD=factvault",
			"POSTGRES_DB=factvault",
		},
	}, func(cfg *docker.HostConfig) {
		cfg.AutoRemove = true
		cfg.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		t.Fatalf("pool.RunWithOptions: %v", err)
	}
	t.Cleanup(func() { _ = pool.Purge(resource) })

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
	defer db.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose.SetDialect: %v", err)
	}
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

	var roleExists bool
	if err := db.QueryRowContext(context.Background(), "SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = 'app_user')").Scan(&roleExists); err != nil {
		t.Fatalf("checking role: %v", err)
	}
	if !roleExists {
		t.Error("app_user role missing")
	}

	var viewExists bool
	if err := db.QueryRowContext(context.Background(), "SELECT EXISTS (SELECT FROM pg_views WHERE viewname = 'v_conflicts')").Scan(&viewExists); err != nil {
		t.Fatalf("checking view: %v", err)
	}
	if !viewExists {
		t.Error("v_conflicts view missing")
	}
}
