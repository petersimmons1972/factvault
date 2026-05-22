# factvault/db — Database Layer

## Running Migrations Locally

### Prerequisites

- Postgres 16 with pgvector extension (or use the bundled Docker image)
- `FACTVAULT_DATABASE_URL` set in your environment

### Quick Start

```bash
# Start the database (pgvector-enabled)
docker compose up -d postgres

# Run all migrations
alembic upgrade head

# Check current revision
alembic current

# Downgrade one step
alembic downgrade -1
```

## Setting `FACTVAULT_DATABASE_URL`

```bash
export FACTVAULT_DATABASE_URL="postgresql+psycopg://factvault:factvault@localhost:5432/factvault"
```

Format: `postgresql+psycopg://<user>:<password>@<host>:<port>/<dbname>`

The URL is read by `factvault/config.py` and passed to SQLAlchemy's `create_engine()`. Alembic reads it from `alembic.ini` via `%(FACTVAULT_DATABASE_URL)s` interpolation.

## Tenant Context Pattern

All domain tables have Row-Level Security enforced at the database layer. Every connection that reads or writes data **must** set `app.tenant_id` before executing any query.

Use the `tenant_context` context manager:

```python
from uuid import UUID
from sqlalchemy import create_engine, text
from factvault.db.rls import tenant_context

engine = create_engine("postgresql+psycopg://factvault:factvault@localhost:5432/factvault")

TENANT_ID = UUID("a1b2c3d4-e5f6-7890-abcd-ef1234567890")

with engine.connect() as conn:
    with conn.begin():
        with tenant_context(conn, TENANT_ID):
            rows = conn.execute(text("SELECT id, label FROM entities")).fetchall()
            for row in rows:
                print(row.id, row.label)
```

`SET LOCAL app.tenant_id` is scoped to the current transaction. When the transaction ends (commit or rollback), the setting is automatically cleared — no explicit cleanup.

**Never run queries outside a `tenant_context` block** unless you are the table owner (migration runner) or running explicit maintenance queries.

## Writing Tests Against the Schema

Tests use `testcontainers-python` to spin up a real Postgres + pgvector container. The `conn` fixture (defined in `tests/conftest.py`) provides a ready-to-use SQLAlchemy `Connection` with all migrations applied.

```python
# tests/db/test_my_feature.py

from uuid import uuid4
from sqlalchemy import text

TENANT = uuid4()


def test_something(conn):
    conn.execute(
        text("SET LOCAL app.tenant_id = :tid"),
        {"tid": str(TENANT)},
    )
    conn.execute(
        text("INSERT INTO entities (id, tenant_id, label) VALUES (:id, :tid, 'Test')"),
        {"id": str(uuid4()), "tid": str(TENANT)},
    )
    rows = conn.execute(
        text("SELECT label FROM entities WHERE tenant_id = :tid"),
        {"tid": str(TENANT)},
    ).fetchall()
    assert len(rows) == 1
    assert rows[0].label == "Test"
```

Run tests:

```bash
# All db tests
pytest tests/db/ -v

# Single file
pytest tests/db/test_rls_isolation.py -v
```

The `conn` fixture rolls back between tests — no manual cleanup needed.

> **Important — superuser bypasses RLS:** The `conn` fixture connects as the Postgres superuser.
> Superusers bypass Row-Level Security unconditionally, even with `FORCE ROW LEVEL SECURITY` in effect.
> Tests using `conn` do **not** exercise RLS policies. For RLS-sensitive tests, use the `app_engine`
> fixture instead (which connects as a non-superuser `app_user` role). See
> `tests/db/test_rls_isolation.py` for examples.

## Migration style conventions

Two migration styles appear in this codebase, both correct:

- **SQLAlchemy form** (`op.create_table` + `sa.Column(..., TIMESTAMP(timezone=True))`) — used in 0002–0006, 0010, 0011, 0012, 0013. Preferred for new migrations; supports autogenerate via Alembic.
- **Raw SQL form** (`op.execute("CREATE TABLE ... TIMESTAMPTZ ...")`) — used in 0007, 0008, 0009 where we needed Postgres-specific features (triggers, complex CHECK constraints). Both `TIMESTAMP WITH TIME ZONE` and `TIMESTAMPTZ` produce identical column types; the form follows the migration style.

When adding a new migration: prefer the SQLAlchemy form unless you need Postgres-only DDL (triggers, partial unique indexes, materialised views, etc.).

## What This Plan Covers

**Plan 1 — Schema and Migrations** (this plan):

- All 13 migrations: pgvector extension, all domain tables, embedding columns, HNSW indices, RLS policies, `v_conflicts` view.
- SQLAlchemy ORM models (`factvault/db/models.py`).
- `tenant_context` helper (`factvault/db/rls.py`).
- Full test suite proving constraints, append-only enforcement, RLS isolation, and embedding round-trips.
- CI workflow (`.github/workflows/ci.yml`).

## What Comes in Plans 2–5

| Plan | Scope |
|------|-------|
| **Plan 2 — Source Pipeline** | `workers/collect.py`, `workers/archive.py`, `workers/verify.py`, collector implementations, excerpt-offset check |
| **Plan 3 — Fact Pipeline** | `workers/extract.py`, `workers/corroborate.py`, `workers/relate.py`, LLM extractor, deterministic extractors, confidence formula |
| **Plan 4 — Bundle and Retrieval** | `factvault/assembler/`, FastAPI API, MCP server, `factvault doctor` CLI |
| **Plan 5 — Deploy and Examples** | K8s manifests, docker-compose production stack, four runnable examples with fixtures |
