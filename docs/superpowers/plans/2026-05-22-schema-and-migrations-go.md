# Schema and Migrations Implementation Plan — Go Rewrite

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Python+Alembic+SQLAlchemy Plan 1 implementation with a Go+goose+pgx+sqlc implementation. The Postgres schema itself doesn't change — same tables, same RLS policies, same `v_conflicts` view, same constraints. What changes is the language layer: migrations become goose SQL files, models become sqlc-generated Go structs, `tenant_context` becomes a Go helper, and tests run via dockertest instead of testcontainers-python.

**Architecture:**

Decision per Opus advisor (Decision 1 — Goose migration content strategy): Option B (schema dump), implemented as **2 goose files** rather than 1 or 13. `00001_initial_schema.sql` is a transactional DDL dump of all tables, constraints, non-HNSW indices, RLS policies, trigger functions, and the `v_conflicts` view. `00002_hnsw_indices.sql` uses `-- +goose NO TRANSACTION` because `CREATE INDEX CONCURRENTLY` cannot run inside a transaction block. Alternatives considered: 13 files (1:1 Alembic port) or 1 consolidated file. 13 files would have been correct but operationally noisy for a v0 project where the Alembic history lives in git permanently; 1 consolidated file is technically incorrect because `-- +goose NO TRANSACTION` would prevent all DDL from being transactional. Rationale: 2 files gives clean schema provenance, correct transaction semantics, and a simple migration ladder for operators going forward.

Decision per Opus advisor (Decision 2 — Test container strategy): Option A — one shared session-scoped Postgres+pgvector container via `TestMain`, with per-test transaction rollback using pgx subtransactions. Alternatives considered: per-package containers (strong isolation, slow). Rationale: the Python plan used the same pattern successfully; a single container with rollback-per-test is fast, and the schema-corruption risk is low since tests don't modify DDL.

Decision per Opus advisor (Decision 3 — `tenant_context` API shape): `db.TenantContext(ctx, tenantID) (context.Context, error)` — returns a derived context with the `app.tenant_id` GUC set via `SET LOCAL` inside a transaction, per AGENTS.md. The GUC name is `app.tenant_id` (not `app.current_tenant_id`). Alternatives considered: explicit `*db.Conn` with `Close()` (Option B), or a bare `SetTenantID(tx pgx.Tx, id pgtype.UUID) error` helper. Rationale: AGENTS.md already specifies the context-returning shape `ctx, err := db.TenantContext(ctx, tenantID)` as the canonical call site; adopting this makes every downstream worker and route handler consistent.

Decision per Opus advisor (Decision 4 — sqlc query organization): Option B — per-domain files (`entities.sql`, `properties.sql`, `statements.sql`, `qualifiers.sql`, `sources.sql`, etc.) under `internal/db/queries/`. Alternatives considered: monolithic `queries.sql`. Rationale: go-transition.md §4.5 already uses the `internal/db/queries/*.sql` glob pattern implying multiple files; with 10 tables and 5 plans of query growth, per-domain files are the only navigable structure.

**Tech Stack:** Go 1.22+, pgx v5, pgvector-go, sqlc, goose v3, stdlib testing + dockertest v3, Chainguard `wolfi-base` for the runtime container.

---

## This Plan Replaces the Python Plan 1 Implementation

This plan **replaces** — not extends — the Python Plan 1 implementation. The first execution task (Task 1) deletes all Python files. The schema persists in Postgres state; the Go code rebuilds against it from scratch.

**Files deleted in Task 1 (Plan 1 Python):**
- `factvault/__init__.py`
- `factvault/config.py`
- `factvault/db/__init__.py`
- `factvault/db/models.py`
- `factvault/db/engine.py`
- `factvault/db/rls.py`
- `factvault/db/schema.sql`
- `factvault/db/README.md`
- `factvault/db/migrations/` (entire directory including `env.py`, `script.py.mako`, and all 13 `versions/*.py`)
- `pyproject.toml`
- `alembic.ini`
- `tests/conftest.py`
- `tests/__init__.py`
- `tests/db/` (entire directory)
- `.venv/` (if present)

**Files deleted in Task 1 (Plan 2 Python, also present on main):**
- `factvault/collectors/`
- `factvault/workers/`
- `factvault/archiving/`
- `tests/collectors/`
- `tests/workers/`
- `tests/archiving/`
- `tests/integration/`
- `tests/fixtures/`

The Python file content is preserved in git history. The Go implementation is canonical going forward.

---

## File Structure

```
factvault/
├── cmd/factvault/main.go                    # Cobra root + subcommand registration
├── internal/
│   ├── version/
│   │   └── version.go                       # version constant
│   └── db/
│       ├── conn.go                          # pgx pool + pgvector type registration
│       ├── rls.go                           # TenantContext helper (SET LOCAL app.tenant_id)
│       ├── pgvector.go                      # pgvector type registration helper
│       └── queries/                         # sqlc input files — one per domain
│           ├── entities.sql
│           ├── properties.sql
│           ├── statements.sql
│           ├── qualifiers.sql
│           └── sources.sql
├── migrations/
│   ├── 00001_initial_schema.sql             # transactional: all tables, constraints, RLS, view
│   └── 00002_hnsw_indices.sql               # NO TRANSACTION: CREATE INDEX CONCURRENTLY
├── services/embedder/                       # Python sentence-transformers microservice scaffold
│   ├── app.py
│   ├── pyproject.toml
│   ├── Dockerfile
│   └── tests/test_app.py
├── internal/db/models.go                    # sqlc-generated; checked in
├── internal/db/querier.go                   # sqlc-generated; checked in
├── internal/db/db.go                        # sqlc-generated; checked in
├── internal/testdb/
│   └── testdb.go                            # dockertest fixture (shared session container)
├── sqlc.yaml
├── go.mod
├── go.sum
└── Makefile
```

---

## Tasks

### Task 1 — Delete Python implementation

Delete all Python source files from Plan 1 and Plan 2. The Go implementation starts clean.

- [ ] Verify pre-flight:

```bash
cd ~/projects/factvault
git status -sb       # must be clean
git branch --show-current  # must be main
git log --oneline -3  # 452f660 must be HEAD
```

Expected output:
```
## main...origin/main
main
452f660 Merge pull request #61 from petersimmons1972/chore/codex-tooling
```

- [ ] Remove Python files:

```bash
cd ~/projects/factvault

# Plan 1 Python — db layer
git rm -r factvault/

# Plan 1 Python — Alembic
git rm alembic.ini
git rm -r factvault/db/migrations/ 2>/dev/null || true  # already gone via factvault/

# Plan 1 Python — project config
git rm pyproject.toml

# Plan 1 Python — tests
git rm -r tests/

# Plan 1 Python — optional venv (untracked, not in git)
rm -rf .venv 2>/dev/null || true

# Verify nothing Python remains in the tracked tree
git ls-files '*.py' '*.cfg' 'alembic.ini' 'pyproject.toml'
# Expected: empty output
```

- [ ] Verify the Python files are gone:

```bash
git status --short | head -30
# Expected: all deleted files staged (D  factvault/..., D  tests/..., etc.)
# No untracked Python files

test ! -d factvault && echo "factvault/ removed OK"
test ! -f alembic.ini && echo "alembic.ini removed OK"
test ! -f pyproject.toml && echo "pyproject.toml removed OK"
test ! -d tests && echo "tests/ removed OK"
```

- [ ] Commit:

```bash
git commit -m "chore: delete Plan 1+2 Python code — replaced by Go (goose + pgx + dockertest)"
```

---

### Task 2 — Go project bootstrap

Stand up `go.mod`, the minimal Cobra binary, a version constant, and a `Makefile`. Verify the project compiles.

- [ ] Create `go.mod`:

```
module github.com/petersimmons1972/factvault

go 1.22

require (
	github.com/jackc/pgx/v5 v5.7.2
	github.com/ory/dockertest/v3 v3.11.0
	github.com/pgvector/pgvector-go v0.2.2
	github.com/pressly/goose/v3 v3.24.1
	github.com/spf13/cobra v1.8.1
	github.com/sqlc-dev/pqtype v0.3.0
)
```

(Run `go mod tidy` after creating files to populate `go.sum` and resolve exact versions.)

- [ ] Create `internal/version/version.go`:

```go
package version

const Version = "0.1.0"
```

- [ ] Create `cmd/factvault/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/version"
)

func main() {
	root := &cobra.Command{
		Use:     "factvault",
		Short:   "factvault — verifiable fact database",
		Version: version.Version,
	}

	root.AddCommand(newMigrateCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newMigrateCmd() *cobra.Command {
	var dsn string

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run goose database migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrations(cmd.Context(), dsn)
		},
	}

	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres DSN (overrides FACTVAULT_DATABASE_URL)")
	return cmd
}
```

- [ ] Create `internal/db/conn.go` stub (full implementation in Task 4):

```go
package db

import "context"

// runMigrations is implemented in migrate.go; conn.go holds pool construction.
// This stub keeps the package valid before Task 4.
var _ = context.Background
```

- [ ] Create `cmd/factvault/migrate.go`:

```go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func runMigrations(ctx context.Context, dsn string) error {
	if dsn == "" {
		dsn = os.Getenv("FACTVAULT_DATABASE_URL")
	}
	if dsn == "" {
		return fmt.Errorf("database DSN required: set --dsn flag or FACTVAULT_DATABASE_URL")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(nil) // use filesystem migrations directory
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}

	if err := goose.RunContext(ctx, "up", db, "migrations"); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}

	return nil
}
```

- [ ] Create `Makefile`:

```makefile
.PHONY: build test lint fmt generate migrate

BINARY := factvault
DSN    ?= $(FACTVAULT_DATABASE_URL)

build:
	go build -o bin/$(BINARY) ./cmd/factvault

test:
	go test ./... -count=1

lint:
	go vet ./...

fmt:
	gofmt -w .

generate:
	sqlc generate

migrate:
	go run ./cmd/factvault migrate --dsn "$(DSN)"
```

- [ ] Build and verify:

```bash
cd ~/projects/factvault
go mod tidy
go build ./...
```

Expected: no errors; `go.sum` populated.

- [ ] Commit:

```bash
git add go.mod go.sum cmd/ internal/version/ Makefile
git commit -m "feat(go): project bootstrap — go.mod, Cobra root, migrate subcommand, Makefile"
```

---

### Task 3 — goose migrations

Create the two goose SQL migration files. The SQL is extracted verbatim from the 13 Alembic migrations.

Decision per Opus advisor (Decision 1): Two files. `00001_initial_schema.sql` is transactional (no `NO TRANSACTION` annotation). `00002_hnsw_indices.sql` uses `-- +goose NO TRANSACTION` because `CREATE INDEX CONCURRENTLY` cannot run inside a transaction block. Alternatives considered: 13 files (1:1 Alembic port, preserves rollback granularity but operationally noisy for v0) and 1 file (technically incorrect — would require `NO TRANSACTION` annotation, making all DDL non-transactional). The 2-file split is the minimum correct decomposition.

- [ ] Create `migrations/` directory:

```bash
mkdir -p ~/projects/factvault/migrations
```

- [ ] Create `migrations/00001_initial_schema.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- Extensions
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- app_user role (non-superuser; owns all application queries)
-- NOTE: The migration creates NOLOGIN only. The init layer (compose: docker-entrypoint-initdb.d/;
-- K8s: init container from Secret) creates the role WITH LOGIN and the env-supplied password.
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'app_user') THEN
        CREATE ROLE app_user NOLOGIN;
    END IF;
END
$$;

-- -------------------------------------------------------------------------
-- entities
-- -------------------------------------------------------------------------
CREATE TABLE entities (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL,
    ext_id      TEXT,
    label       TEXT NOT NULL,
    type_uri    TEXT,
    description TEXT,
    embedding   vector(1024),
    meta        JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_entities_tenant_ext_id UNIQUE (tenant_id, ext_id) NULLS NOT DISTINCT
);

CREATE INDEX idx_entities_tenant ON entities (tenant_id);
CREATE INDEX idx_entities_label  ON entities (tenant_id, lower(label));
CREATE INDEX idx_entities_type   ON entities (tenant_id, type_uri);

-- -------------------------------------------------------------------------
-- properties
-- -------------------------------------------------------------------------
CREATE TABLE properties (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID,
    slug        TEXT NOT NULL,
    label       TEXT NOT NULL,
    value_type  TEXT NOT NULL
                CHECK (value_type IN ('entity_ref', 'string', 'number', 'date', 'url')),
    description TEXT,
    CONSTRAINT uq_properties_tenant_slug UNIQUE (tenant_id, slug) NULLS NOT DISTINCT
);

-- -------------------------------------------------------------------------
-- statements
-- -------------------------------------------------------------------------
CREATE TABLE statements (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    subject_id   UUID NOT NULL REFERENCES entities(id),
    property_id  UUID NOT NULL REFERENCES properties(id),
    val_entity   UUID REFERENCES entities(id),
    val_text     TEXT,
    val_number   NUMERIC,
    val_date     TIMESTAMPTZ,
    val_json     JSONB,
    rank         TEXT NOT NULL DEFAULT 'normal'
                 CHECK (rank IN ('preferred', 'normal', 'deprecated')),
    confidence   NUMERIC(4,3) NOT NULL
                 CHECK (confidence >= 0 AND confidence <= 1),
    embedding    vector(1024),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_statement_value_populated CHECK (
        (val_entity IS NOT NULL)::int +
        (val_text   IS NOT NULL)::int +
        (val_number IS NOT NULL)::int +
        (val_date   IS NOT NULL)::int = 1
    )
);

CREATE INDEX idx_statements_subject    ON statements (subject_id, property_id, rank);
CREATE INDEX idx_statements_tenant     ON statements (tenant_id, subject_id);
CREATE INDEX idx_statements_val_entity ON statements (val_entity) WHERE val_entity IS NOT NULL;
CREATE INDEX idx_statements_confidence ON statements (confidence DESC);

-- -------------------------------------------------------------------------
-- qualifiers
-- -------------------------------------------------------------------------
CREATE TABLE qualifiers (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    statement_id UUID NOT NULL REFERENCES statements(id) ON DELETE CASCADE,
    property_id  UUID NOT NULL REFERENCES properties(id),
    val_text     TEXT,
    val_number   NUMERIC,
    val_date     TIMESTAMPTZ,
    val_entity   UUID REFERENCES entities(id),
    CONSTRAINT chk_qualifier_value_populated CHECK (
        (val_entity IS NOT NULL)::int +
        (val_text   IS NOT NULL)::int +
        (val_number IS NOT NULL)::int +
        (val_date   IS NOT NULL)::int = 1
    )
);

CREATE INDEX idx_qualifiers_statement ON qualifiers (statement_id);

-- -------------------------------------------------------------------------
-- relations
-- -------------------------------------------------------------------------
CREATE TABLE relations (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    source_id    UUID NOT NULL REFERENCES entities(id),
    target_id    UUID NOT NULL REFERENCES entities(id),
    type         TEXT NOT NULL,
    weight       NUMERIC,
    confidence   NUMERIC(4,3) CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    description  TEXT,
    embedding    vector(1024),
    meta         JSONB NOT NULL DEFAULT '{}',
    statement_id UUID REFERENCES statements(id) ON DELETE CASCADE,
    CONSTRAINT uq_relations_tenant_source_target_type
        UNIQUE (tenant_id, source_id, target_id, type)
);

CREATE INDEX idx_relations_source    ON relations (tenant_id, source_id);
CREATE INDEX idx_relations_target    ON relations (tenant_id, target_id);
CREATE INDEX idx_relations_type      ON relations (tenant_id, type);

-- -------------------------------------------------------------------------
-- sources
-- -------------------------------------------------------------------------
CREATE TABLE sources (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL,
    url              TEXT NOT NULL,
    fetched_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    content_hash     TEXT NOT NULL,
    raw_html         BYTEA,
    raw_text         TEXT,
    archive_url      TEXT,
    publisher        TEXT,
    title            TEXT,
    published_at     TIMESTAMPTZ,
    embedding        vector(1024),
    last_verified_at TIMESTAMPTZ,
    status           TEXT NOT NULL DEFAULT 'collected'
                     CHECK (status IN (
                         'collected', 'archived', 'extracted',
                         'verified', 'link-rot', 'content-changed'
                     )),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_sources_tenant_url UNIQUE (tenant_id, url)
);

CREATE INDEX idx_sources_tenant_status ON sources (tenant_id, status);
CREATE INDEX idx_sources_last_verified ON sources (last_verified_at);
CREATE INDEX idx_sources_published_at  ON sources (published_at);

-- -------------------------------------------------------------------------
-- statement_sources
-- -------------------------------------------------------------------------
CREATE TABLE statement_sources (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    statement_id         UUID NOT NULL REFERENCES statements(id) ON DELETE CASCADE,
    source_id            UUID NOT NULL REFERENCES sources(id),
    excerpt              TEXT NOT NULL,
    excerpt_offset_start INTEGER NOT NULL CHECK (excerpt_offset_start >= 0),
    excerpt_offset_end   INTEGER NOT NULL CHECK (excerpt_offset_end > excerpt_offset_start),
    extraction_method    TEXT NOT NULL,
    extracted_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    confidence           NUMERIC(4,3) CHECK (
        confidence IS NULL OR (confidence >= 0 AND confidence <= 1)
    ),
    tenant_id            UUID NOT NULL,
    CONSTRAINT uq_statement_sources_stmt_src UNIQUE (statement_id, source_id)
);

CREATE INDEX idx_stmt_sources_statement ON statement_sources (statement_id);
CREATE INDEX idx_stmt_sources_source    ON statement_sources (source_id);

-- -------------------------------------------------------------------------
-- source_verifications (append-only)
-- -------------------------------------------------------------------------
CREATE TABLE source_verifications (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id        UUID NOT NULL REFERENCES sources(id),
    tenant_id        UUID NOT NULL,
    verified_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    status           TEXT NOT NULL CHECK (
        status IN ('live', 'link-rot', 'content-changed', 'excerpt-missing')
    ),
    new_content_hash TEXT,
    notes            TEXT
);

CREATE INDEX idx_source_verifications_source ON source_verifications (source_id, verified_at DESC);
CREATE INDEX idx_source_verifications_status ON source_verifications (status, verified_at DESC);

-- Append-only trigger
CREATE OR REPLACE FUNCTION deny_source_verifications_mutation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'source_verifications is append-only. DELETE and UPDATE are forbidden.';
END;
$$;

CREATE TRIGGER trg_source_verifications_no_update
    BEFORE UPDATE ON source_verifications
    FOR EACH ROW EXECUTE FUNCTION deny_source_verifications_mutation();

CREATE TRIGGER trg_source_verifications_no_delete
    BEFORE DELETE ON source_verifications
    FOR EACH ROW EXECUTE FUNCTION deny_source_verifications_mutation();

-- -------------------------------------------------------------------------
-- proposed_properties (strict-mode queue)
-- -------------------------------------------------------------------------
CREATE TABLE proposed_properties (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL,
    proposed_slug       TEXT NOT NULL,
    proposed_value_type TEXT NOT NULL CHECK (
        proposed_value_type IN ('entity_ref', 'string', 'number', 'date', 'url')
    ),
    proposed_by         TEXT NOT NULL,
    example_excerpt     TEXT,
    example_source_id   UUID REFERENCES sources(id),
    status              TEXT NOT NULL DEFAULT 'pending' CHECK (
        status IN ('pending', 'approved', 'rejected')
    ),
    reviewed_by         TEXT,
    reviewed_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ DEFAULT now(),
    CONSTRAINT uq_proposed_properties_tenant_slug_status
        UNIQUE (tenant_id, proposed_slug, status)
);

CREATE INDEX idx_proposed_properties_tenant_status
    ON proposed_properties (tenant_id, status);

-- -------------------------------------------------------------------------
-- dossiers (pre-computed bundle cache)
-- -------------------------------------------------------------------------
CREATE TABLE dossiers (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    entity_id    UUID NOT NULL REFERENCES entities(id),
    assembled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    bundle       JSONB NOT NULL,
    CONSTRAINT uq_dossiers_tenant_entity UNIQUE (tenant_id, entity_id)
);

CREATE INDEX idx_dossiers_tenant_assembled
    ON dossiers (tenant_id, assembled_at DESC);

-- -------------------------------------------------------------------------
-- RLS policies
-- -------------------------------------------------------------------------

-- Revoke all default PUBLIC access
REVOKE ALL ON ALL TABLES IN SCHEMA public FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM PUBLIC;

-- Grant app_user access
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO app_user;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO app_user;

-- Enable RLS on all domain tables
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'entities', 'properties', 'statements', 'relations',
        'sources', 'statement_sources', 'source_verifications',
        'proposed_properties', 'dossiers'
    ]
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    END LOOP;
END
$$;

-- Standard tenant isolation policy for tables with direct tenant_id column
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'entities', 'properties', 'statements', 'relations',
        'sources', 'statement_sources', 'source_verifications',
        'proposed_properties', 'dossiers'
    ]
    LOOP
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I
             USING (tenant_id = NULLIF(current_setting(''app.tenant_id'', true), '''')::uuid)',
            t
        );
    END LOOP;
END
$$;

-- qualifiers has no direct tenant_id; policy is EXISTS-based through statements
CREATE POLICY tenant_isolation ON qualifiers
    USING (
        EXISTS (
            SELECT 1 FROM statements s
            WHERE s.id = qualifiers.statement_id
              AND s.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
        )
    );

ALTER TABLE qualifiers ENABLE ROW LEVEL SECURITY;
ALTER TABLE qualifiers FORCE ROW LEVEL SECURITY;

-- -------------------------------------------------------------------------
-- v_conflicts view
-- -------------------------------------------------------------------------
CREATE OR REPLACE VIEW v_conflicts AS
SELECT
    s1.id           AS statement_a_id,
    s2.id           AS statement_b_id,
    s1.tenant_id,
    s1.subject_id,
    s1.property_id,
    p.slug          AS property_slug,
    s1.val_text     AS val_a_text,
    s1.val_number   AS val_a_number,
    s1.val_date     AS val_a_date,
    s1.val_entity   AS val_a_entity,
    s2.val_text     AS val_b_text,
    s2.val_number   AS val_b_number,
    s2.val_date     AS val_b_date,
    s2.val_entity   AS val_b_entity,
    s1.confidence   AS confidence_a,
    s2.confidence   AS confidence_b,
    s1.rank         AS rank_a,
    s2.rank         AS rank_b,
    s1.created_at   AS created_a,
    s2.created_at   AS created_b
FROM statements s1
JOIN statements s2
    ON  s1.subject_id  = s2.subject_id
    AND s1.property_id = s2.property_id
    AND s1.id < s2.id
    AND s1.rank != 'deprecated'
    AND s2.rank != 'deprecated'
JOIN properties p ON p.id = s1.property_id
WHERE
    (s1.val_text   IS DISTINCT FROM s2.val_text)   OR
    (s1.val_number IS DISTINCT FROM s2.val_number) OR
    (s1.val_date   IS DISTINCT FROM s2.val_date)   OR
    (s1.val_entity IS DISTINCT FROM s2.val_entity);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS v_conflicts;
DROP TRIGGER IF EXISTS trg_source_verifications_no_delete ON source_verifications;
DROP TRIGGER IF EXISTS trg_source_verifications_no_update ON source_verifications;
DROP FUNCTION IF EXISTS deny_source_verifications_mutation();
DROP TABLE IF EXISTS dossiers;
DROP TABLE IF EXISTS proposed_properties;
DROP TABLE IF EXISTS source_verifications;
DROP TABLE IF EXISTS statement_sources;
DROP TABLE IF EXISTS sources;
DROP TABLE IF EXISTS relations;
DROP TABLE IF EXISTS qualifiers;
DROP TABLE IF EXISTS statements;
DROP TABLE IF EXISTS properties;
DROP TABLE IF EXISTS entities;
-- +goose StatementEnd
```

- [ ] Create `migrations/00002_hnsw_indices.sql`:

```sql
-- +goose NO TRANSACTION

-- +goose Up
-- HNSW indices on embedding columns.
-- CREATE INDEX CONCURRENTLY cannot run inside a transaction block.
-- This file uses -- +goose NO TRANSACTION to allow concurrent index creation.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_entities_embedding
    ON entities USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_statements_embedding
    ON statements USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_relations_embedding
    ON relations USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_sources_embedding
    ON sources USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_entities_embedding;
DROP INDEX CONCURRENTLY IF EXISTS idx_statements_embedding;
DROP INDEX CONCURRENTLY IF EXISTS idx_relations_embedding;
DROP INDEX CONCURRENTLY IF EXISTS idx_sources_embedding;
```

- [ ] Write a migration smoke test using dockertest. Create `migrations/migrations_test.go`:

```go
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
	}, func(config *docker.HostConfig) {
		config.AutoRemove = true
		config.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		t.Fatalf("pool.Run: %v", err)
	}
	t.Cleanup(func() { _ = pool.Purge(resource) })

	dsn := fmt.Sprintf(
		"postgres://factvault:factvault@localhost:%s/factvault?sslmode=disable",
		resource.GetPort("5432/tcp"),
	)

	var db *sql.DB
	if err := pool.Retry(func() error {
		var err error
		db, err = sql.Open("pgx", dsn)
		if err != nil {
			return err
		}
		return db.PingContext(context.Background())
	}); err != nil {
		t.Fatalf("postgres not ready: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Run goose migrations from the migrations/ directory
	goose.SetBaseFS(nil)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose.SetDialect: %v", err)
	}
	if err := goose.RunContext(context.Background(), "up", db, "."); err != nil {
		t.Fatalf("goose up: %v", err)
	}

	// Verify all expected tables exist
	tables := []string{
		"entities", "properties", "statements", "qualifiers", "relations",
		"sources", "statement_sources", "source_verifications",
		"proposed_properties", "dossiers",
	}
	for _, tbl := range tables {
		var exists bool
		row := db.QueryRowContext(context.Background(),
			"SELECT EXISTS (SELECT FROM pg_tables WHERE tablename = $1)", tbl)
		if err := row.Scan(&exists); err != nil {
			t.Fatalf("checking table %s: %v", tbl, err)
		}
		if !exists {
			t.Errorf("table %s not found after migration", tbl)
		}
	}

	// Verify HNSW indices exist
	indices := []string{
		"idx_entities_embedding", "idx_statements_embedding",
		"idx_relations_embedding", "idx_sources_embedding",
	}
	for _, idx := range indices {
		var exists bool
		row := db.QueryRowContext(context.Background(),
			"SELECT EXISTS (SELECT FROM pg_indexes WHERE indexname = $1)", idx)
		if err := row.Scan(&exists); err != nil {
			t.Fatalf("checking index %s: %v", idx, err)
		}
		if !exists {
			t.Errorf("HNSW index %s not found after migration", idx)
		}
	}

	// Verify app_user role exists
	var roleExists bool
	row := db.QueryRowContext(context.Background(),
		"SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = 'app_user')")
	if err := row.Scan(&roleExists); err != nil {
		t.Fatalf("checking app_user role: %v", err)
	}
	if !roleExists {
		t.Error("app_user role not found after migration")
	}

	// Verify v_conflicts view exists
	var viewExists bool
	row = db.QueryRowContext(context.Background(),
		"SELECT EXISTS (SELECT FROM pg_views WHERE viewname = 'v_conflicts')")
	if err := row.Scan(&viewExists); err != nil {
		t.Fatalf("checking v_conflicts view: %v", err)
	}
	if !viewExists {
		t.Error("v_conflicts view not found after migration")
	}
}
```

- [ ] Run:

```bash
cd ~/projects/factvault
go test ./migrations/... -v -run TestMigrationsRunClean -timeout 120s
```

Expected: `PASS` — all tables present, HNSW indices present, app_user role present, v_conflicts view present.

- [ ] Commit:

```bash
git add migrations/
git commit -m "feat(migrations): goose SQL migrations — initial schema + HNSW indices"
```

---

### Task 4 — pgx pool + connection helper

Implement `internal/db/conn.go`. The `NewPool` constructor registers pgvector types via `AfterConnect`. Tests insert and read a `vector(1024)` column to prove registration works.

- [ ] Remove the stub created in Task 2:

```bash
rm ~/projects/factvault/internal/db/conn.go
```

- [ ] Create `internal/db/conn.go`:

```go
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

// NewPool opens a pgxpool.Pool against dsn and registers the pgvector custom
// type codec on every connection via AfterConnect. Every downstream query that
// scans into pgvector.Vector or encodes []float32 requires this registration.
//
// dsn must be a valid libpq connection string or DSN URL, e.g.:
//   "postgres://user:pass@localhost:5432/factvault?sslmode=disable"
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db.NewPool: parse config: %w", err)
	}

	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		pgxvec.RegisterTypes(ctx, conn)
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db.NewPool: create pool: %w", err)
	}

	// Verify the connection is live before returning.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db.NewPool: ping: %w", err)
	}

	return pool, nil
}

// vectorFromSlice converts []float32 to pgvector.Vector for use in INSERT/UPDATE.
func vectorFromSlice(v []float32) pgvector.Vector {
	return pgvector.NewVector(v)
}
```

- [ ] Create `internal/db/conn_test.go`:

```go
package db_test

import (
	"context"
	"testing"

	pgvector "github.com/pgvector/pgvector-go"

	"github.com/petersimmons1972/factvault/internal/testdb"
)

func TestNewPool_PingSucceeds(t *testing.T) {
	pool := testdb.New(t)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("pool.Ping: %v", err)
	}
}

func TestNewPool_VectorTypeRegistered(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("pool.Acquire: %v", err)
	}
	defer conn.Release()

	// Create a temporary table with a vector(1024) column.
	_, err = conn.Exec(ctx, "CREATE TEMP TABLE _vec_registration_test (v vector(1024))")
	if err != nil {
		t.Fatalf("CREATE TEMP TABLE: %v", err)
	}

	// Insert a sample 1024-dimension vector.
	sample := make([]float32, 1024)
	for i := range sample {
		sample[i] = float32(i) * 0.001
	}
	vec := pgvector.NewVector(sample)

	_, err = conn.Exec(ctx, "INSERT INTO _vec_registration_test VALUES ($1)", vec)
	if err != nil {
		t.Fatalf("INSERT vector: %v (is pgvector registered?)", err)
	}

	// Read it back.
	var result pgvector.Vector
	row := conn.QueryRow(ctx, "SELECT v FROM _vec_registration_test LIMIT 1")
	if err := row.Scan(&result); err != nil {
		t.Fatalf("Scan vector: %v", err)
	}

	got := result.Slice()
	if len(got) != 1024 {
		t.Fatalf("expected 1024-dimension vector, got %d", len(got))
	}
	if got[1] != float32(1)*0.001 {
		t.Fatalf("vector roundtrip mismatch at index 1: got %f, want %f", got[1], float32(1)*0.001)
	}
}
```

Note: this test depends on `internal/testdb` (Task 6). Write the stub first, then the full implementation in Task 6.

- [ ] Commit (after Task 6 completes — these two tasks are sequentially coupled):

```bash
git add internal/db/conn.go internal/db/conn_test.go
git commit -m "feat(db): pgx pool constructor with pgvector type registration"
```

---

### Task 5 — tenant_context helper

Implement `internal/db/rls.go`. The GUC name is `app.tenant_id` (per AGENTS.md). The API shape is `TenantContext(ctx, tenantID) (context.Context, error)` per AGENTS.md's canonical call site.

Decision per Opus advisor (Decision 3): `db.TenantContext(ctx, tenantID) (context.Context, error)` — returns a derived context. The implementation must begin a transaction and execute `SET LOCAL app.tenant_id = $1` within it, because `SET LOCAL` is transaction-scoped. The caller passes the returned context to sqlc-generated query functions; the context carries the Tx as a value, and query functions detect its presence via a DBTX extractor. Alternatives considered: bare `SetTenantID(tx pgx.Tx, id pgtype.UUID) error` (simpler but requires callers to manage Tx explicitly) and full context-wrapping via `BeforeAcquire` (incorrect — `SET LOCAL` cannot be set at acquire time outside a transaction). Rationale: AGENTS.md already specifies the `ctx, err := db.TenantContext(ctx, tenantID)` call site; the context-returning shape gives the cleanest downstream API.

Implementation note: because sqlc generates functions taking a `DBTX` interface (not a context value), the recommended pattern for Plan 1 is to expose both:
1. `TenantContext(ctx context.Context, tenantID pgtype.UUID) (context.Context, pgx.Tx, error)` — begins a Tx on the pool stored in ctx, sets the GUC, returns a derived ctx + the Tx for callers to pass as DBTX.
2. `func WithPool(ctx context.Context, pool *pgxpool.Pool) context.Context` — stores the pool in context so `TenantContext` can find it.

- [ ] Create `internal/db/rls.go`:

```go
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type contextKey int

const poolKey contextKey = iota

// WithPool stores a *pgxpool.Pool in the context so that TenantContext can
// acquire a connection from it without requiring callers to pass the pool
// explicitly. Call this once at startup or at the top of a request handler.
func WithPool(ctx context.Context, pool *pgxpool.Pool) context.Context {
	return context.WithValue(ctx, poolKey, pool)
}

// poolFromCtx retrieves the *pgxpool.Pool stored by WithPool.
func poolFromCtx(ctx context.Context) (*pgxpool.Pool, error) {
	pool, ok := ctx.Value(poolKey).(*pgxpool.Pool)
	if !ok || pool == nil {
		return nil, fmt.Errorf("db: no pool in context — call db.WithPool first")
	}
	return pool, nil
}

// TenantContext begins a transaction on the pool stored in ctx, executes
//
//	SET LOCAL app.tenant_id = '<tenantID>'
//
// and returns a derived context with the transaction embedded, plus the
// transaction itself for use as a sqlc DBTX argument.
//
// The caller MUST call tx.Rollback or tx.Commit when done. Typical usage:
//
//	ctx = db.WithPool(ctx, pool)
//	ctx, tx, err := db.TenantContext(ctx, tenantID)
//	if err != nil { return err }
//	defer tx.Rollback(ctx) // safe even after Commit
//
//	rows, err := queries.New(tx).ListEntities(ctx, tenantID)
//
// WARNING: Do NOT call tx.Commit() inside a loop or retry; SET LOCAL is
// transaction-scoped and is destroyed on commit. Begin a new TenantContext
// for each logical unit of work.
func TenantContext(ctx context.Context, tenantID pgtype.UUID) (context.Context, pgx.Tx, error) {
	pool, err := poolFromCtx(ctx)
	if err != nil {
		return ctx, nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return ctx, nil, fmt.Errorf("db.TenantContext: begin tx: %w", err)
	}

	// SET LOCAL is transaction-scoped; it reverts automatically on commit or rollback.
	// The UUID value is validated by pgtype — it can only contain hex digits and hyphens;
	// embedding it directly in the SQL string is safe.
	tenantStr := tenantID.String()
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL app.tenant_id = '%s'", tenantStr)); err != nil {
		_ = tx.Rollback(ctx)
		return ctx, nil, fmt.Errorf("db.TenantContext: SET LOCAL: %w", err)
	}

	return ctx, tx, nil
}
```

- [ ] Create `internal/db/rls_test.go`:

```go
package db_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/testdb"
)

// TestTenantContext_GUCIsSet verifies that after TenantContext, the GUC
// app.tenant_id matches the provided tenant ID.
func TestTenantContext_GUCIsSet(t *testing.T) {
	pool := testdb.New(t)
	ctx := db.WithPool(context.Background(), pool)

	var tenantID pgtype.UUID
	if err := tenantID.Scan("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"); err != nil {
		t.Fatalf("scan UUID: %v", err)
	}

	ctx, tx, err := db.TenantContext(ctx, tenantID)
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}
	defer tx.Rollback(ctx)

	var got string
	row := tx.QueryRow(ctx, "SELECT current_setting('app.tenant_id', true)")
	if err := row.Scan(&got); err != nil {
		t.Fatalf("QueryRow GUC: %v", err)
	}
	if got != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("app.tenant_id = %q, want %q", got, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	}
}

// TestTenantContext_RLSFiltersRows verifies that queries inside a TenantContext
// only return rows for that tenant, and cross-tenant rows are invisible.
func TestTenantContext_RLSFiltersRows(t *testing.T) {
	pool := testdb.New(t)

	// Insert two entities — one for tenant A, one for tenant B — as the
	// superuser (bypasses RLS) to set up test data.
	tenantA := "11111111-1111-1111-1111-111111111111"
	tenantB := "22222222-2222-2222-2222-222222222222"

	setupConn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire setup conn: %v", err)
	}
	defer setupConn.Release()

	_, err = setupConn.Exec(context.Background(),
		"INSERT INTO entities (tenant_id, label) VALUES ($1, 'Alpha Corp'), ($2, 'Beta Corp')",
		tenantA, tenantB,
	)
	if err != nil {
		t.Fatalf("INSERT test entities: %v", err)
	}

	// Query as tenant A — should see only Alpha Corp.
	var tenantAID pgtype.UUID
	if err := tenantAID.Scan(tenantA); err != nil {
		t.Fatalf("scan tenantA UUID: %v", err)
	}

	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, tenantAID)
	if err != nil {
		t.Fatalf("TenantContext(A): %v", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, "SELECT label FROM entities ORDER BY label")
	if err != nil {
		t.Fatalf("Query entities as tenant A: %v", err)
	}
	defer rows.Close()

	var labels []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		labels = append(labels, label)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	if len(labels) != 1 || labels[0] != "Alpha Corp" {
		t.Errorf("tenant A query returned %v, want [Alpha Corp]", labels)
	}
}

// TestTenantContext_CrossTenantReturnsZeroRows verifies that tenant A cannot
// see tenant B's rows.
func TestTenantContext_CrossTenantReturnsZeroRows(t *testing.T) {
	pool := testdb.New(t)

	tenantA := "33333333-3333-3333-3333-333333333333"
	tenantB := "44444444-4444-4444-4444-444444444444"

	setupConn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire setup conn: %v", err)
	}
	defer setupConn.Release()

	_, err = setupConn.Exec(context.Background(),
		"INSERT INTO entities (tenant_id, label) VALUES ($1, 'Gamma Corp')",
		tenantB,
	)
	if err != nil {
		t.Fatalf("INSERT tenant B entity: %v", err)
	}
	setupConn.Release()

	// Query as tenant A — should see zero rows.
	var tenantAID pgtype.UUID
	if err := tenantAID.Scan(tenantA); err != nil {
		t.Fatalf("scan tenantA UUID: %v", err)
	}

	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, tenantAID)
	if err != nil {
		t.Fatalf("TenantContext(A): %v", err)
	}
	defer tx.Rollback(ctx)

	var count int
	row := tx.QueryRow(ctx, "SELECT COUNT(*) FROM entities")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("COUNT entities: %v", err)
	}
	if count != 0 {
		t.Errorf("tenant A sees %d rows, want 0 (tenant B's rows must be invisible)", count)
	}
}

// TestTenantContext_GUCResetsAfterRollback verifies that after Rollback,
// the GUC is no longer set on the connection (SET LOCAL is tx-scoped).
func TestTenantContext_GUCResetsAfterRollback(t *testing.T) {
	pool := testdb.New(t)

	var tenantID pgtype.UUID
	if err := tenantID.Scan("55555555-5555-5555-5555-555555555555"); err != nil {
		t.Fatalf("scan UUID: %v", err)
	}

	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, tenantID)
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// After rollback, a new connection from the pool should not have the GUC set.
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("pool.Acquire: %v", err)
	}
	defer conn.Release()

	var gucVal string
	row := conn.QueryRow(context.Background(), "SELECT current_setting('app.tenant_id', true)")
	if err := row.Scan(&gucVal); err != nil {
		t.Fatalf("QueryRow GUC: %v", err)
	}
	// current_setting returns empty string when missing_ok=true and GUC is unset
	if gucVal != "" {
		t.Errorf("app.tenant_id = %q after rollback, want empty string", gucVal)
	}
}
```

- [ ] Commit (after Task 6 is complete and tests pass):

```bash
git add internal/db/rls.go internal/db/rls_test.go
git commit -m "feat(db): TenantContext helper — SET LOCAL app.tenant_id with pgx.Tx"
```

---

### Task 6 — dockertest fixtures

Implement `internal/testdb/testdb.go`. Provides `New(t *testing.T) *pgxpool.Pool` returning a fully-migrated pool. One shared container per test binary via `TestMain`, reset per test via transaction rollback.

Decision per Opus advisor (Decision 2): Option A — shared session-scoped container via `TestMain`, per-test connection + rollback. The container starts once via `TestMain` in `internal/testdb/testdb.go`; each call to `New(t)` returns a pool connected to that container. Per-test isolation is achieved by beginning a transaction at the start of each test and rolling back in `t.Cleanup`. Alternatives considered: per-package containers (stronger isolation, 3-5s overhead per package). Rationale: matches the Python plan's session-scoped testcontainers pattern; fast; the schema-corruption risk (temp tables surviving rollback) is acceptable since Plan 1 tests don't use temp tables outside conn_test.go.

- [ ] Create `internal/testdb/testdb.go`:

```go
// Package testdb provides a shared dockertest Postgres+pgvector container
// for integration tests. The container is started once per test binary via
// TestMain; individual tests call testdb.New(t) to obtain a pool connected
// to that container.
//
// Usage in a test file:
//
//	func TestMain(m *testing.M) {
//	    testdb.StartContainer()
//	    code := m.Run()
//	    testdb.StopContainer()
//	    os.Exit(code)
//	}
//
//	func TestSomething(t *testing.T) {
//	    pool := testdb.New(t)
//	    // pool is connected to the migrated Postgres container
//	}
package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"sync"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/pressly/goose/v3"
	"testing"

	"github.com/petersimmons1972/factvault/internal/db"
)

var (
	once        sync.Once
	globalPool  *dockertest.Pool
	globalRes   *dockertest.Resource
	globalDSN   string
	startErr    error
)

// StartContainer starts the shared Postgres+pgvector container and runs goose
// migrations. Call from TestMain before m.Run().
func StartContainer() {
	once.Do(func() {
		var err error
		globalPool, err = dockertest.NewPool("")
		if err != nil {
			startErr = fmt.Errorf("dockertest.NewPool: %w", err)
			return
		}

		globalRes, err = globalPool.RunWithOptions(&dockertest.RunOptions{
			Repository: "pgvector/pgvector",
			Tag:        "pg16",
			Env: []string{
				"POSTGRES_USER=factvault_test",
				"POSTGRES_PASSWORD=factvault_test",
				"POSTGRES_DB=factvault_test",
			},
		}, func(config *docker.HostConfig) {
			config.AutoRemove = true
			config.RestartPolicy = docker.RestartPolicy{Name: "no"}
		})
		if err != nil {
			startErr = fmt.Errorf("pool.Run: %w", err)
			return
		}

		globalDSN = fmt.Sprintf(
			"postgres://factvault_test:factvault_test@localhost:%s/factvault_test?sslmode=disable",
			globalRes.GetPort("5432/tcp"),
		)

		// Wait for Postgres to be ready.
		var sqlDB *sql.DB
		if err := globalPool.Retry(func() error {
			var err error
			sqlDB, err = sql.Open("pgx", globalDSN)
			if err != nil {
				return err
			}
			return sqlDB.PingContext(context.Background())
		}); err != nil {
			startErr = fmt.Errorf("postgres not ready: %w", err)
			return
		}
		defer sqlDB.Close()

		// Run goose migrations. The migrations/ directory is at the project root.
		// When tests run from a package subdirectory, resolve the path relative to
		// the module root using the GOMODCACHE or a known relative path.
		migrationsDir := migrationsPath()
		goose.SetBaseFS(nil)
		if err := goose.SetDialect("postgres"); err != nil {
			startErr = fmt.Errorf("goose.SetDialect: %w", err)
			return
		}
		if err := goose.RunContext(context.Background(), "up", sqlDB, migrationsDir); err != nil {
			startErr = fmt.Errorf("goose up: %w", err)
			return
		}

		slog.Info("testdb: container ready", "dsn_host", "localhost:"+globalRes.GetPort("5432/tcp"))
	})
}

// StopContainer purges the shared container. Call from TestMain after m.Run().
func StopContainer() {
	if globalPool != nil && globalRes != nil {
		_ = globalPool.Purge(globalRes)
	}
}

// New returns a *pgxpool.Pool connected to the shared test container.
// The pool has pgvector types registered via db.NewPool.
// The test is registered a cleanup function that closes the pool on test completion.
//
// Tests that need per-test isolation should wrap their queries in a transaction
// and call tx.Rollback in t.Cleanup — not call New() for each query.
func New(t *testing.T) *db.Pool {
	t.Helper()

	if startErr != nil {
		t.Fatalf("testdb: container start failed: %v", startErr)
	}
	if globalDSN == "" {
		t.Fatal("testdb: StartContainer has not been called — add TestMain to this package")
	}

	pool, err := db.NewPool(context.Background(), globalDSN)
	if err != nil {
		t.Fatalf("testdb.New: db.NewPool: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}

// migrationsPath returns the absolute path to the migrations/ directory.
// It walks up from the current working directory until it finds go.mod,
// then returns <root>/migrations.
func migrationsPath() string {
	dir, err := os.Getwd()
	if err != nil {
		return "migrations"
	}
	for {
		if _, err := os.Stat(dir + "/go.mod"); err == nil {
			return dir + "/migrations"
		}
		parent := dir[:len(dir)-len("/"+lastSegment(dir))]
		if parent == dir {
			return "migrations" // fallback
		}
		dir = parent
	}
}

func lastSegment(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
```

Note: `db.Pool` is a type alias for `*pgxpool.Pool` — add this to `internal/db/conn.go`:

```go
// Pool is a type alias for *pgxpool.Pool, exported for use in testdb.
type Pool = pgxpool.Pool
```

- [ ] Create `internal/testdb/testmain_example_test.go` (shows the TestMain pattern every test package using testdb must follow):

```go
package testdb_test

import (
	"os"
	"testing"

	"github.com/petersimmons1972/factvault/internal/testdb"
)

// TestMain is the required entry point for any test package that uses testdb.New.
// Copy this pattern into every _test package that needs a database.
func TestMain(m *testing.M) {
	testdb.StartContainer()
	code := m.Run()
	testdb.StopContainer()
	os.Exit(code)
}

func TestContainerIsReachable(t *testing.T) {
	pool := testdb.New(t)
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatalf("pool.Ping: %v", err)
	}
}
```

- [ ] Run:

```bash
cd ~/projects/factvault
go test ./internal/testdb/... -v -timeout 120s
```

Expected:
```
--- PASS: TestContainerIsReachable (X.XXs)
PASS
```

- [ ] Commit:

```bash
git add internal/testdb/
git commit -m "feat(testdb): shared dockertest Postgres container fixture for integration tests"
```

---

### Task 7 — sqlc setup

Create `sqlc.yaml` and the initial per-domain query files. Run `sqlc generate` to produce `internal/db/models.go`, `internal/db/querier.go`, and `internal/db/db.go`. Commit both SQL inputs and generated Go.

Decision per Opus advisor (Decision 4): Option B — per-domain files under `internal/db/queries/`. Alternatives considered: monolithic `queries.sql`. Rationale: the go-transition.md §4.5 glob pattern `internal/db/queries/*.sql` implies multiple files; 10 tables across 5 plans makes per-domain the only navigable structure.

- [ ] Create `sqlc.yaml`:

```yaml
version: "2"
sql:
  - engine: "postgresql"
    queries: "internal/db/queries"
    schema: "migrations"
    gen:
      go:
        package: "db"
        out: "internal/db"
        sql_package: "pgx/v5"
        emit_json_tags: true
        emit_prepared_queries: false
        emit_interface: true
        emit_exact_table_names: false
        overrides:
          - db_type: "uuid"
            go_type: "github.com/jackc/pgx/v5/pgtype.UUID"
          - db_type: "pg_catalog.timestamptz"
            go_type: "github.com/jackc/pgx/v5/pgtype.Timestamptz"
          - db_type: "pg_catalog.numeric"
            go_type: "github.com/jackc/pgx/v5/pgtype.Numeric"
          - db_type: "vector"
            go_type: "github.com/pgvector/pgvector-go.Vector"
```

- [ ] Create `internal/db/queries/entities.sql`:

```sql
-- name: GetEntity :one
SELECT id, tenant_id, ext_id, label, type_uri, description, embedding, meta, created_at, updated_at
FROM entities
WHERE id = @id;

-- name: GetEntityByExtID :one
SELECT id, tenant_id, ext_id, label, type_uri, description, embedding, meta, created_at, updated_at
FROM entities
WHERE tenant_id = @tenant_id AND ext_id = @ext_id;

-- name: ListEntities :many
SELECT id, tenant_id, ext_id, label, type_uri, description, meta, created_at, updated_at
FROM entities
WHERE tenant_id = @tenant_id
ORDER BY lower(label);

-- name: CreateEntity :one
INSERT INTO entities (tenant_id, ext_id, label, type_uri, description, meta)
VALUES (@tenant_id, @ext_id, @label, @type_uri, @description, @meta)
RETURNING id, tenant_id, ext_id, label, type_uri, description, embedding, meta, created_at, updated_at;

-- name: UpdateEntityEmbedding :exec
UPDATE entities
SET embedding = @embedding, updated_at = now()
WHERE id = @id;

-- name: DeleteEntity :exec
DELETE FROM entities WHERE id = @id;
```

- [ ] Create `internal/db/queries/properties.sql`:

```sql
-- name: GetProperty :one
SELECT id, tenant_id, slug, label, value_type, description
FROM properties
WHERE id = @id;

-- name: GetPropertyBySlug :one
SELECT id, tenant_id, slug, label, value_type, description
FROM properties
WHERE (tenant_id = @tenant_id OR tenant_id IS NULL) AND slug = @slug
ORDER BY tenant_id NULLS LAST
LIMIT 1;

-- name: ListProperties :many
SELECT id, tenant_id, slug, label, value_type, description
FROM properties
WHERE tenant_id = @tenant_id OR tenant_id IS NULL
ORDER BY slug;

-- name: CreateProperty :one
INSERT INTO properties (tenant_id, slug, label, value_type, description)
VALUES (@tenant_id, @slug, @label, @value_type, @description)
RETURNING id, tenant_id, slug, label, value_type, description;
```

- [ ] Create `internal/db/queries/statements.sql`:

```sql
-- name: GetStatement :one
SELECT id, tenant_id, subject_id, property_id,
       val_entity, val_text, val_number, val_date, val_json,
       rank, confidence, embedding, created_at
FROM statements
WHERE id = @id;

-- name: ListStatementsBySubject :many
SELECT id, tenant_id, subject_id, property_id,
       val_entity, val_text, val_number, val_date, val_json,
       rank, confidence, created_at
FROM statements
WHERE tenant_id = @tenant_id AND subject_id = @subject_id
  AND rank != 'deprecated'
ORDER BY property_id, confidence DESC;

-- name: CreateStatement :one
INSERT INTO statements
    (tenant_id, subject_id, property_id, val_entity, val_text, val_number, val_date, rank, confidence)
VALUES
    (@tenant_id, @subject_id, @property_id, @val_entity, @val_text, @val_number, @val_date, @rank, @confidence)
RETURNING id, tenant_id, subject_id, property_id,
          val_entity, val_text, val_number, val_date, val_json,
          rank, confidence, embedding, created_at;

-- name: UpdateStatementConfidence :exec
UPDATE statements SET confidence = @confidence WHERE id = @id;

-- name: UpdateStatementRank :exec
UPDATE statements SET rank = @rank WHERE id = @id;

-- name: UpdateStatementEmbedding :exec
UPDATE statements SET embedding = @embedding WHERE id = @id;
```

- [ ] Create `internal/db/queries/qualifiers.sql`:

```sql
-- name: ListQualifiersByStatement :many
SELECT id, statement_id, property_id,
       val_text, val_number, val_date, val_entity
FROM qualifiers
WHERE statement_id = @statement_id
ORDER BY property_id;

-- name: CreateQualifier :one
INSERT INTO qualifiers (statement_id, property_id, val_text, val_number, val_date, val_entity)
VALUES (@statement_id, @property_id, @val_text, @val_number, @val_date, @val_entity)
RETURNING id, statement_id, property_id, val_text, val_number, val_date, val_entity;

-- name: DeleteQualifiersByStatement :exec
DELETE FROM qualifiers WHERE statement_id = @statement_id;
```

- [ ] Create `internal/db/queries/sources.sql`:

```sql
-- name: GetSource :one
SELECT id, tenant_id, url, fetched_at, content_hash,
       raw_html, raw_text, archive_url, publisher, title,
       published_at, embedding, last_verified_at, status, created_at
FROM sources
WHERE id = @id;

-- name: GetSourceByURL :one
SELECT id, tenant_id, url, fetched_at, content_hash,
       raw_html, raw_text, archive_url, publisher, title,
       published_at, embedding, last_verified_at, status, created_at
FROM sources
WHERE tenant_id = @tenant_id AND url = @url;

-- name: ListSourcesByStatus :many
SELECT id, tenant_id, url, fetched_at, content_hash,
       raw_text, archive_url, publisher, title,
       published_at, last_verified_at, status, created_at
FROM sources
WHERE tenant_id = @tenant_id AND status = @status
ORDER BY fetched_at DESC;

-- name: CreateSource :one
INSERT INTO sources
    (tenant_id, url, content_hash, raw_html, publisher, title, published_at, status)
VALUES
    (@tenant_id, @url, @content_hash, @raw_html, @publisher, @title, @published_at, @status)
RETURNING id, tenant_id, url, fetched_at, content_hash,
          raw_html, raw_text, archive_url, publisher, title,
          published_at, embedding, last_verified_at, status, created_at;

-- name: UpdateSourceArchived :exec
UPDATE sources
SET raw_text    = @raw_text,
    raw_html    = @raw_html,
    archive_url = @archive_url,
    status      = 'archived'
WHERE id = @id;

-- name: UpdateSourceStatus :exec
UPDATE sources SET status = @status WHERE id = @id;

-- name: UpdateSourceEmbedding :exec
UPDATE sources SET embedding = @embedding WHERE id = @id;

-- name: ListSourcesPendingVerification :many
SELECT id, tenant_id, url, content_hash, raw_text, last_verified_at, status
FROM sources
WHERE last_verified_at IS NULL
   OR last_verified_at < now() - INTERVAL '7 days'
ORDER BY last_verified_at ASC NULLS FIRST
LIMIT @lim;
```

- [ ] Run sqlc generate:

```bash
cd ~/projects/factvault
sqlc generate
```

Expected: no errors; `internal/db/models.go`, `internal/db/querier.go`, and `internal/db/db.go` created.

- [ ] Verify generated code compiles:

```bash
go build ./internal/db/...
```

Expected: no errors.

- [ ] Commit:

```bash
git add sqlc.yaml internal/db/queries/ internal/db/models.go internal/db/querier.go internal/db/db.go
git commit -m "feat(db): sqlc setup — per-domain query files + generated Go models and querier"
```

---

### Task 8 — Entities + Properties sqlc queries and tests

Tests prove insert + read + RLS isolation for `entities` and `properties` using the sqlc-generated functions.

- [ ] Create `internal/db/entities_test.go`:

```go
package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/testdb"
)

func TestMain(m *testing.M) {
	testdb.StartContainer()
	code := m.Run()
	testdb.StopContainer()
	os.Exit(code)
}

func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatalf("scan UUID %q: %v", s, err)
	}
	return u
}

func TestCreateAndGetEntity(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")

	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, tenantID)
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	created, err := q.CreateEntity(ctx, db.CreateEntityParams{
		TenantID:    tenantID,
		Label:       "Acme Corp",
		TypeUri:     pgtype.Text{String: "https://schema.org/Organization", Valid: true},
		Description: pgtype.Text{String: "Test entity", Valid: true},
		Meta:        []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if created.Label != "Acme Corp" {
		t.Errorf("label = %q, want %q", created.Label, "Acme Corp")
	}

	fetched, err := q.GetEntity(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if fetched.ID != created.ID {
		t.Errorf("GetEntity returned wrong ID")
	}
}

func TestListEntities_RLSIsolation(t *testing.T) {
	pool := testdb.New(t)
	tenantA := mustUUID(t, "11111111-1111-1111-1111-111111111111")
	tenantB := mustUUID(t, "22222222-2222-2222-2222-222222222222")

	// Insert entities for both tenants as superuser (bypass RLS).
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	_, err = conn.Exec(context.Background(),
		"INSERT INTO entities (tenant_id, label) VALUES ($1, 'A-Corp'), ($2, 'B-Corp')",
		tenantA, tenantB,
	)
	conn.Release()
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	// Query as tenant A.
	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, tenantA)
	if err != nil {
		t.Fatalf("TenantContext(A): %v", err)
	}
	defer tx.Rollback(ctx)

	rows, err := db.New(tx).ListEntities(ctx, tenantA)
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}

	for _, row := range rows {
		if row.TenantID != tenantA {
			t.Errorf("tenant A sees row belonging to tenant %v", row.TenantID)
		}
	}
}

func TestEntity_LabelNotNull(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")

	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	_, err = conn.Exec(context.Background(),
		"INSERT INTO entities (tenant_id) VALUES ($1)", tenantID)
	if err == nil {
		t.Fatal("expected NOT NULL violation, got nil error")
	}
}

func TestEntity_UniqueExtIDPerTenant(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "cccccccc-cccc-cccc-cccc-cccccccccccc")

	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	_, err = conn.Exec(context.Background(),
		"INSERT INTO entities (tenant_id, label, ext_id) VALUES ($1, 'X', 'Q123'), ($1, 'Y', 'Q123')",
		tenantID)
	if err == nil {
		t.Fatal("expected unique constraint violation, got nil error")
	}
}
```

- [ ] Create `internal/db/properties_test.go`:

```go
package db_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/testdb"
)

func TestCreateAndGetProperty(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "dddddddd-dddd-dddd-dddd-dddddddddddd")

	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, tenantID)
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	created, err := q.CreateProperty(ctx, db.CreatePropertyParams{
		TenantID:  pgtype.UUID{Bytes: tenantID.Bytes, Valid: true},
		Slug:      "founded_in",
		Label:     "Founded in",
		ValueType: "date",
	})
	if err != nil {
		t.Fatalf("CreateProperty: %v", err)
	}
	if created.Slug != "founded_in" {
		t.Errorf("slug = %q, want founded_in", created.Slug)
	}

	fetched, err := q.GetProperty(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetProperty: %v", err)
	}
	if fetched.ValueType != "date" {
		t.Errorf("value_type = %q, want date", fetched.ValueType)
	}
}

func TestProperty_ValueTypeCheck(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")

	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	_, err = conn.Exec(context.Background(),
		"INSERT INTO properties (tenant_id, slug, label, value_type) VALUES ($1, 'bad', 'Bad', 'invalid_type')",
		tenantID)
	if err == nil {
		t.Fatal("expected CHECK constraint violation, got nil error")
	}
}

func TestProperty_UniqueSlugPerTenant(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "ffffffff-ffff-ffff-ffff-ffffffffffff")

	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	_, err = conn.Exec(context.Background(),
		"INSERT INTO properties (tenant_id, slug, label, value_type) VALUES ($1, 'ceo', 'CEO', 'entity_ref'), ($1, 'ceo', 'Chief Exec', 'entity_ref')",
		tenantID)
	if err == nil {
		t.Fatal("expected unique constraint violation, got nil error")
	}
}

func TestProperty_SystemWideNullTenant(t *testing.T) {
	pool := testdb.New(t)
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	_, err = conn.Exec(context.Background(),
		"INSERT INTO properties (tenant_id, slug, label, value_type) VALUES (NULL, 'instance_of_test', 'Instance of', 'entity_ref')")
	if err != nil {
		t.Fatalf("INSERT system-wide property: %v", err)
	}
}
```

- [ ] Run:

```bash
cd ~/projects/factvault
go test ./internal/db/... -v -run 'TestCreate|TestList|TestEntity|TestProperty' -timeout 120s
```

Expected: all tests pass.

- [ ] Commit:

```bash
git add internal/db/entities_test.go internal/db/properties_test.go
git commit -m "test(db): entities + properties sqlc queries — insert, read, RLS isolation, constraints"
```

---

### Task 9 — Statements + Qualifiers sqlc queries and tests

Tests cover CHECK constraints (rank enum, confidence range), CASCADE behavior on qualifiers, and RLS isolation.

- [ ] Create `internal/db/statements_test.go`:

```go
package db_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/testdb"
)

// helpers shared across statement and qualifier tests

func insertTestEntity(t *testing.T, ctx context.Context, q *db.Queries, tenantID pgtype.UUID, label string) pgtype.UUID {
	t.Helper()
	e, err := q.CreateEntity(ctx, db.CreateEntityParams{
		TenantID: tenantID,
		Label:    label,
		Meta:     []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("CreateEntity(%q): %v", label, err)
	}
	return e.ID
}

func insertTestProperty(t *testing.T, ctx context.Context, q *db.Queries, tenantID pgtype.UUID, slug, valueType string) pgtype.UUID {
	t.Helper()
	p, err := q.CreateProperty(ctx, db.CreatePropertyParams{
		TenantID:  pgtype.UUID{Bytes: tenantID.Bytes, Valid: true},
		Slug:      slug,
		Label:     slug,
		ValueType: valueType,
	})
	if err != nil {
		t.Fatalf("CreateProperty(%q): %v", slug, err)
	}
	return p.ID
}

func TestCreateStatement_ValText(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "aaaa0001-0000-0000-0000-000000000001")

	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, tenantID)
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)
	subjectID := insertTestEntity(t, ctx, q, tenantID, "Acme")
	propID := insertTestProperty(t, ctx, q, tenantID, "name_stmt_test", "string")

	stmt, err := q.CreateStatement(ctx, db.CreateStatementParams{
		TenantID:   tenantID,
		SubjectID:  subjectID,
		PropertyID: propID,
		ValText:    pgtype.Text{String: "Acme Corporation", Valid: true},
		Rank:       "normal",
		Confidence: pgtype.Numeric{}, // set below
	})
	if err != nil {
		t.Fatalf("CreateStatement: %v", err)
	}
	if !stmt.ValText.Valid || stmt.ValText.String != "Acme Corporation" {
		t.Errorf("val_text = %v, want 'Acme Corporation'", stmt.ValText)
	}
	if stmt.Rank != "normal" {
		t.Errorf("rank = %q, want normal", stmt.Rank)
	}
}

func TestStatement_RankCheckConstraint(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "aaaa0002-0000-0000-0000-000000000002")

	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	_, err = conn.Exec(context.Background(),
		`INSERT INTO entities (tenant_id, label) VALUES ($1, 'E')`, tenantID)
	// Use a raw INSERT to bypass sqlc type safety and exercise the DB constraint.
	_, err = conn.Exec(context.Background(),
		`INSERT INTO statements (tenant_id, subject_id, property_id, val_text, rank, confidence)
		 SELECT $1, e.id, p.id, 'v', 'invalid_rank', 0.5
		 FROM entities e, properties p
		 WHERE e.tenant_id = $1 AND p.slug = 'name_stmt_test'
		 LIMIT 1`,
		tenantID,
	)
	if err == nil {
		t.Fatal("expected rank CHECK constraint violation, got nil")
	}
}

func TestStatement_ConfidenceRangeCheck(t *testing.T) {
	pool := testdb.New(t)
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	tenantID := mustUUID(t, "aaaa0003-0000-0000-0000-000000000003")
	_, _ = conn.Exec(context.Background(),
		`INSERT INTO entities (tenant_id, label) VALUES ($1, 'E2')`, tenantID)
	_, _ = conn.Exec(context.Background(),
		`INSERT INTO properties (tenant_id, slug, label, value_type) VALUES ($1, 'conf_test_prop', 'CT', 'string')`, tenantID)

	_, err = conn.Exec(context.Background(),
		`INSERT INTO statements (tenant_id, subject_id, property_id, val_text, confidence)
		 SELECT $1, e.id, p.id, 'x', 1.5
		 FROM entities e, properties p
		 WHERE e.tenant_id = $1 AND p.slug = 'conf_test_prop'
		 LIMIT 1`,
		tenantID,
	)
	if err == nil {
		t.Fatal("expected confidence CHECK constraint violation for value > 1, got nil")
	}
}

func TestStatement_ValuePopulatedCheck(t *testing.T) {
	pool := testdb.New(t)
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	tenantID := mustUUID(t, "aaaa0004-0000-0000-0000-000000000004")
	_, _ = conn.Exec(context.Background(),
		`INSERT INTO entities (tenant_id, label) VALUES ($1, 'E3')`, tenantID)
	_, _ = conn.Exec(context.Background(),
		`INSERT INTO properties (tenant_id, slug, label, value_type) VALUES ($1, 'vp_test_prop', 'VP', 'string')`, tenantID)

	_, err = conn.Exec(context.Background(),
		`INSERT INTO statements (tenant_id, subject_id, property_id, confidence)
		 SELECT $1, e.id, p.id, 0.5
		 FROM entities e, properties p
		 WHERE e.tenant_id = $1 AND p.slug = 'vp_test_prop'
		 LIMIT 1`,
		tenantID,
	)
	if err == nil {
		t.Fatal("expected value_populated CHECK constraint violation, got nil")
	}
}

func TestStatement_ListBySubject_RLSIsolation(t *testing.T) {
	pool := testdb.New(t)
	tenantA := mustUUID(t, "aaaa0005-0000-0000-0000-000000000005")
	tenantB := mustUUID(t, "aaaa0006-0000-0000-0000-000000000006")

	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	// Insert entities and statements for both tenants via superuser.
	_, _ = conn.Exec(context.Background(),
		`INSERT INTO entities (tenant_id, label) VALUES ($1, 'AE'), ($2, 'BE')`, tenantA, tenantB)
	_, _ = conn.Exec(context.Background(),
		`INSERT INTO properties (tenant_id, slug, label, value_type) VALUES ($1, 'rls_stmt_test', 'RLS', 'string'), ($2, 'rls_stmt_test', 'RLS', 'string')`,
		tenantA, tenantB)

	var subjectA, subjectB pgtype.UUID
	_ = conn.QueryRow(context.Background(), `SELECT id FROM entities WHERE tenant_id = $1`, tenantA).Scan(&subjectA)
	_ = conn.QueryRow(context.Background(), `SELECT id FROM entities WHERE tenant_id = $1`, tenantB).Scan(&subjectB)

	var propA, propB pgtype.UUID
	_ = conn.QueryRow(context.Background(), `SELECT id FROM properties WHERE tenant_id = $1 AND slug = 'rls_stmt_test'`, tenantA).Scan(&propA)
	_ = conn.QueryRow(context.Background(), `SELECT id FROM properties WHERE tenant_id = $1 AND slug = 'rls_stmt_test'`, tenantB).Scan(&propB)

	conn.Exec(context.Background(),
		`INSERT INTO statements (tenant_id, subject_id, property_id, val_text, confidence)
		 VALUES ($1, $2, $3, 'val_a', 0.5), ($4, $5, $6, 'val_b', 0.5)`,
		tenantA, subjectA, propA,
		tenantB, subjectB, propB,
	)
	conn.Release()

	// Query as tenant A via TenantContext.
	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, tenantA)
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}
	defer tx.Rollback(ctx)

	stmts, err := db.New(tx).ListStatementsBySubject(ctx, db.ListStatementsBySubjectParams{
		TenantID:  tenantA,
		SubjectID: subjectA,
	})
	if err != nil {
		t.Fatalf("ListStatementsBySubject: %v", err)
	}
	for _, s := range stmts {
		if s.TenantID != tenantA {
			t.Errorf("tenant A sees statement belonging to tenant %v", s.TenantID)
		}
	}
}
```

- [ ] Create `internal/db/qualifiers_test.go`:

```go
package db_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/testdb"
)

func TestQualifier_InsertAndList(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "bbbb0001-0000-0000-0000-000000000001")

	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, tenantID)
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)
	subjectID := insertTestEntity(t, ctx, q, tenantID, "Entity-Q")
	propID := insertTestProperty(t, ctx, q, tenantID, "qual_prop_test", "string")

	stmt, err := q.CreateStatement(ctx, db.CreateStatementParams{
		TenantID:   tenantID,
		SubjectID:  subjectID,
		PropertyID: propID,
		ValText:    pgtype.Text{String: "val", Valid: true},
		Rank:       "normal",
	})
	if err != nil {
		t.Fatalf("CreateStatement: %v", err)
	}

	qualPropID := insertTestProperty(t, ctx, q, tenantID, "point_in_time_test", "date")

	created, err := q.CreateQualifier(ctx, db.CreateQualifierParams{
		StatementID: stmt.ID,
		PropertyID:  qualPropID,
		ValText:     pgtype.Text{String: "2025-01-01", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateQualifier: %v", err)
	}
	if !created.ValText.Valid {
		t.Fatal("qualifier val_text not set")
	}

	listed, err := q.ListQualifiersByStatement(ctx, stmt.ID)
	if err != nil {
		t.Fatalf("ListQualifiersByStatement: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 qualifier, got %d", len(listed))
	}
}

func TestQualifier_CascadeOnStatementDelete(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "bbbb0002-0000-0000-0000-000000000002")

	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	var entityID, propID, stmtID pgtype.UUID
	_ = conn.QueryRow(context.Background(),
		`INSERT INTO entities (tenant_id, label) VALUES ($1, 'CascadeE') RETURNING id`, tenantID).Scan(&entityID)
	_ = conn.QueryRow(context.Background(),
		`INSERT INTO properties (tenant_id, slug, label, value_type) VALUES ($1, 'cascade_prop_test', 'CP', 'string') RETURNING id`, tenantID).Scan(&propID)
	_ = conn.QueryRow(context.Background(),
		`INSERT INTO statements (tenant_id, subject_id, property_id, val_text, confidence) VALUES ($1, $2, $3, 'v', 0.5) RETURNING id`,
		tenantID, entityID, propID).Scan(&stmtID)
	conn.Exec(context.Background(),
		`INSERT INTO qualifiers (statement_id, property_id, val_text) VALUES ($1, $2, 'q')`, stmtID, propID)

	// Delete the statement — qualifiers should cascade.
	conn.Exec(context.Background(), `DELETE FROM statements WHERE id = $1`, stmtID)

	var count int
	conn.QueryRow(context.Background(), `SELECT COUNT(*) FROM qualifiers WHERE statement_id = $1`, stmtID).Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 qualifiers after statement delete, got %d", count)
	}
}

func TestQualifier_ValuePopulatedCheck(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "bbbb0003-0000-0000-0000-000000000003")

	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	var entityID, propID, stmtID pgtype.UUID
	_ = conn.QueryRow(context.Background(),
		`INSERT INTO entities (tenant_id, label) VALUES ($1, 'QVE') RETURNING id`, tenantID).Scan(&entityID)
	_ = conn.QueryRow(context.Background(),
		`INSERT INTO properties (tenant_id, slug, label, value_type) VALUES ($1, 'qvp_test', 'QV', 'string') RETURNING id`, tenantID).Scan(&propID)
	_ = conn.QueryRow(context.Background(),
		`INSERT INTO statements (tenant_id, subject_id, property_id, val_text, confidence) VALUES ($1, $2, $3, 'v', 0.5) RETURNING id`,
		tenantID, entityID, propID).Scan(&stmtID)

	// INSERT qualifier with no value column set — must fail.
	_, err = conn.Exec(context.Background(),
		`INSERT INTO qualifiers (statement_id, property_id) VALUES ($1, $2)`, stmtID, propID)
	if err == nil {
		t.Fatal("expected chk_qualifier_value_populated violation, got nil")
	}
}
```

- [ ] Run:

```bash
cd ~/projects/factvault
go test ./internal/db/... -v -run 'TestCreate|TestStatement|TestQualifier' -timeout 120s
```

Expected: all tests pass.

- [ ] Commit:

```bash
git add internal/db/statements_test.go internal/db/qualifiers_test.go
git commit -m "test(db): statements + qualifiers — constraints, cascade, RLS isolation"
```

---

### Task 10 — Sources sqlc queries and tests

Tests cover the status enum, `raw_text` nullability, and tenant isolation.

- [ ] Create `internal/db/sources_test.go`:

```go
package db_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/testdb"
)

func TestCreateSource_DefaultStatusCollected(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "cccc0001-0000-0000-0000-000000000001")

	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, tenantID)
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	src, err := q.CreateSource(ctx, db.CreateSourceParams{
		TenantID:    tenantID,
		Url:         "https://example.com/article-1",
		ContentHash: "abc123deadbeef",
		Status:      "collected",
	})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	if src.Status != "collected" {
		t.Errorf("status = %q, want collected", src.Status)
	}
	if src.RawText.Valid {
		t.Error("raw_text should be NULL at collect stage")
	}
}

func TestSource_StatusCheckConstraint(t *testing.T) {
	pool := testdb.New(t)
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	tenantID := mustUUID(t, "cccc0002-0000-0000-0000-000000000002")

	_, err = conn.Exec(context.Background(),
		`INSERT INTO sources (tenant_id, url, content_hash, status) VALUES ($1, 'https://x.com', 'hash', 'invalid_status')`,
		tenantID)
	if err == nil {
		t.Fatal("expected status CHECK constraint violation, got nil")
	}
}

func TestSource_UniqueURLPerTenant(t *testing.T) {
	pool := testdb.New(t)
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	tenantID := mustUUID(t, "cccc0003-0000-0000-0000-000000000003")

	_, _ = conn.Exec(context.Background(),
		`INSERT INTO sources (tenant_id, url, content_hash) VALUES ($1, 'https://dup.com', 'h1')`, tenantID)
	_, err = conn.Exec(context.Background(),
		`INSERT INTO sources (tenant_id, url, content_hash) VALUES ($1, 'https://dup.com', 'h2')`, tenantID)
	if err == nil {
		t.Fatal("expected uq_sources_tenant_url violation, got nil")
	}
}

func TestSource_SameURLDifferentTenants(t *testing.T) {
	pool := testdb.New(t)
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	tenantA := mustUUID(t, "cccc0004-0000-0000-0000-000000000004")
	tenantB := mustUUID(t, "cccc0005-0000-0000-0000-000000000005")

	_, err = conn.Exec(context.Background(),
		`INSERT INTO sources (tenant_id, url, content_hash) VALUES ($1, 'https://shared.com', 'ha'), ($2, 'https://shared.com', 'hb')`,
		tenantA, tenantB)
	if err != nil {
		t.Fatalf("same URL different tenants should succeed: %v", err)
	}
}

func TestListSourcesByStatus_RLSIsolation(t *testing.T) {
	pool := testdb.New(t)
	tenantA := mustUUID(t, "cccc0006-0000-0000-0000-000000000006")
	tenantB := mustUUID(t, "cccc0007-0000-0000-0000-000000000007")

	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	conn.Exec(context.Background(),
		`INSERT INTO sources (tenant_id, url, content_hash, status)
		 VALUES ($1, 'https://a-source.com', 'ha', 'collected'),
		        ($2, 'https://b-source.com', 'hb', 'collected')`,
		tenantA, tenantB)
	conn.Release()

	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, tenantA)
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}
	defer tx.Rollback(ctx)

	srcs, err := db.New(tx).ListSourcesByStatus(ctx, db.ListSourcesByStatusParams{
		TenantID: tenantA,
		Status:   "collected",
	})
	if err != nil {
		t.Fatalf("ListSourcesByStatus: %v", err)
	}
	for _, s := range srcs {
		if s.TenantID != tenantA {
			t.Errorf("tenant A sees source belonging to tenant %v", s.TenantID)
		}
	}
}

func TestUpdateSourceArchived_PopulatesRawText(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "cccc0008-0000-0000-0000-000000000008")

	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, tenantID)
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)
	src, err := q.CreateSource(ctx, db.CreateSourceParams{
		TenantID:    tenantID,
		Url:         "https://archive-test.com/a",
		ContentHash: "archivedHash",
		Status:      "collected",
	})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}

	err = q.UpdateSourceArchived(ctx, db.UpdateSourceArchivedParams{
		ID:         src.ID,
		RawText:    pgtype.Text{String: "the full article text", Valid: true},
		ArchiveUrl: pgtype.Text{String: "https://web.archive.org/web/2025/https://archive-test.com/a", Valid: true},
	})
	if err != nil {
		t.Fatalf("UpdateSourceArchived: %v", err)
	}

	fetched, err := q.GetSource(ctx, src.ID)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if !fetched.RawText.Valid || fetched.RawText.String != "the full article text" {
		t.Errorf("raw_text = %v, want 'the full article text'", fetched.RawText)
	}
	if fetched.Status != "archived" {
		t.Errorf("status = %q, want archived", fetched.Status)
	}
}
```

- [ ] Run:

```bash
cd ~/projects/factvault
go test ./internal/db/... -v -run 'TestCreate|TestSource|TestList|TestUpdate' -timeout 120s
```

Expected: all tests pass.

- [ ] Commit:

```bash
git add internal/db/sources_test.go
git commit -m "test(db): sources — status enum, raw_text nullability, tenant isolation"
```

---

### Task 11 — pgvector embedding column verification

Tests confirm `vector(1024)` columns work end-to-end via pgx: insert a sample vector, run an HNSW-indexed nearest-neighbor query, assert correct ordering. This validates the pgvector-go integration without committing to any embedding-population logic (that lands in Plan 3).

- [ ] Create `internal/db/pgvector_test.go`:

```go
package db_test

import (
	"context"
	"math"
	"testing"

	pgvector "github.com/pgvector/pgvector-go"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/testdb"
)

// makeUnitVector returns a 1024-dimension unit vector with all components
// set to 1/sqrt(1024). Used as a deterministic test embedding.
func makeUnitVector() pgvector.Vector {
	dim := 1024
	v := make([]float32, dim)
	val := float32(1.0 / math.Sqrt(float64(dim)))
	for i := range v {
		v[i] = val
	}
	return pgvector.NewVector(v)
}

// makeZeroVector returns a 1024-dimension zero vector.
func makeZeroVector() pgvector.Vector {
	return pgvector.NewVector(make([]float32, 1024))
}

// makeOrthoVector returns a 1024-dimension vector with only the first component set.
// This vector is orthogonal to makeUnitVector in a useful sense for ordering tests.
func makeOrthoVector() pgvector.Vector {
	v := make([]float32, 1024)
	v[0] = 1.0
	return pgvector.NewVector(v)
}

// TestEmbedding_RoundTrip verifies that a vector(1024) column stores and
// retrieves a vector correctly via pgvector-go.
func TestEmbedding_RoundTrip(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "dddd0001-0000-0000-0000-000000000001")

	ctx := db.WithPool(context.Background(), pool)
	ctx, tx, err := db.TenantContext(ctx, tenantID)
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	// Create an entity with an embedding.
	entity, err := q.CreateEntity(ctx, db.CreateEntityParams{
		TenantID: tenantID,
		Label:    "VectorEntity",
		Meta:     []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	vec := makeUnitVector()
	if err := q.UpdateEntityEmbedding(ctx, db.UpdateEntityEmbeddingParams{
		ID:        entity.ID,
		Embedding: vec,
	}); err != nil {
		t.Fatalf("UpdateEntityEmbedding: %v", err)
	}

	// Fetch and verify the vector roundtripped correctly.
	fetched, err := q.GetEntity(ctx, entity.ID)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}

	gotSlice := fetched.Embedding.Slice()
	if len(gotSlice) != 1024 {
		t.Fatalf("expected 1024-dim vector, got %d", len(gotSlice))
	}

	wantVal := float32(1.0 / math.Sqrt(1024.0))
	if math.Abs(float64(gotSlice[0]-wantVal)) > 1e-5 {
		t.Errorf("vector[0] = %f, want ~%f", gotSlice[0], wantVal)
	}
}

// TestEmbedding_HNSWNearestNeighborOrdering verifies that HNSW-indexed
// cosine similarity search returns results in the correct order.
//
// Setup: three entities with distinct embeddings.
//   - "Near": unit vector (all components equal, normalized) — most similar to query
//   - "Far": unit vector with only first component set — less similar
//   - "Zero": zero vector — least similar (undefined cosine, treated as maximally distant)
//
// Query: nearest neighbors to the unit vector.
// Expected order: Near first, then Far.
func TestEmbedding_HNSWNearestNeighborOrdering(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "dddd0002-0000-0000-0000-000000000002")

	// Insert entities with distinct embeddings as superuser (bypasses RLS).
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	type testEntity struct {
		label string
		vec   pgvector.Vector
	}
	entities := []testEntity{
		{"Near", makeUnitVector()},
		{"Far", makeOrthoVector()},
	}

	for _, e := range entities {
		_, err := conn.Exec(context.Background(),
			`INSERT INTO entities (tenant_id, label, embedding) VALUES ($1, $2, $3)`,
			tenantID, e.label, e.vec,
		)
		if err != nil {
			t.Fatalf("INSERT entity %q: %v", e.label, err)
		}
	}
	conn.Release()

	// Query nearest neighbors using HNSW index via cosine distance.
	// app.tenant_id must be set for RLS; use a raw connection with SET LOCAL.
	queryConn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire query conn: %v", err)
	}
	defer queryConn.Release()

	tx2, err := queryConn.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx2.Rollback(context.Background())

	tenantStr := tenantID.String()
	if _, err := tx2.Exec(context.Background(),
		`SET LOCAL app.tenant_id = '`+tenantStr+`'`); err != nil {
		t.Fatalf("SET LOCAL: %v", err)
	}

	queryVec := makeUnitVector()
	rows, err := tx2.Query(context.Background(),
		`SELECT label, embedding <=> $1 AS distance
		 FROM entities
		 WHERE tenant_id = $2
		 ORDER BY embedding <=> $1
		 LIMIT 2`,
		queryVec, tenantID,
	)
	if err != nil {
		t.Fatalf("HNSW query: %v", err)
	}
	defer rows.Close()

	type result struct {
		label    string
		distance float64
	}
	var results []result
	for rows.Next() {
		var r result
		if err := rows.Scan(&r.label, &r.distance); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// "Near" (unit vector) should be closest to the unit query vector (distance ~ 0).
	if results[0].label != "Near" {
		t.Errorf("first result = %q (distance %f), want Near", results[0].label, results[0].distance)
	}

	// Distances should be non-decreasing.
	if results[0].distance > results[1].distance {
		t.Errorf("results not in ascending distance order: %f > %f",
			results[0].distance, results[1].distance)
	}

	t.Logf("HNSW results: %s (%.6f), %s (%.6f)",
		results[0].label, results[0].distance,
		results[1].label, results[1].distance)
}

// TestEmbedding_NullEmbeddingAccepted verifies that entities without embeddings
// (embedding IS NULL) can be created and do not appear in HNSW queries that
// filter on embedding IS NOT NULL.
func TestEmbedding_NullEmbeddingAccepted(t *testing.T) {
	pool := testdb.New(t)
	tenantID := mustUUID(t, "dddd0003-0000-0000-0000-000000000003")

	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	_, err = conn.Exec(context.Background(),
		`INSERT INTO entities (tenant_id, label) VALUES ($1, 'NoEmbedding')`, tenantID)
	if err != nil {
		t.Fatalf("INSERT entity without embedding: %v", err)
	}

	var isNull bool
	conn.QueryRow(context.Background(),
		`SELECT embedding IS NULL FROM entities WHERE tenant_id = $1 AND label = 'NoEmbedding'`,
		tenantID).Scan(&isNull)
	if !isNull {
		t.Error("expected embedding to be NULL for entity inserted without embedding")
	}
}
```

- [ ] Run all integration tests:

```bash
cd ~/projects/factvault
go test ./internal/db/... -v -timeout 180s
```

Expected: all tests pass, including HNSW ordering test.

- [ ] Run full test suite:

```bash
cd ~/projects/factvault
go test ./... -count=1 -timeout 180s
```

Expected: all packages pass.

- [ ] Commit:

```bash
git add internal/db/pgvector_test.go
git commit -m "test(db): pgvector embedding roundtrip + HNSW nearest-neighbor ordering verification"
```

---

<!-- PASS 1 END — Pass 2 appends Tasks 12-22 + self-review below this line -->
