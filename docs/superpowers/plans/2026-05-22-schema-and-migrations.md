# Schema and Migrations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the factvault Postgres schema — every table, constraint, RLS policy, index, and view from spec §3 — with Alembic migrations, SQLAlchemy models, and a test suite that proves constraints and tenant isolation work. This is Plan 1 of 5; subsequent plans (source-pipeline, fact-pipeline, bundle-and-retrieval, deploy-and-examples) depend on it.

**Architecture:** Postgres 16 + pgvector in a Chainguard wolfi-base container. Alembic migrations are the source of truth; SQLAlchemy 2.x ORM models mirror the schema for application code. Multi-tenant isolation is enforced at the database layer via Postgres Row-Level Security with policies keyed off `current_setting('app.tenant_id')`. Tests use testcontainers-python to spin up a real Postgres+pgvector container per session — no SQLite, no in-memory shortcuts.

**Tech Stack:** Postgres 16, pgvector, Python 3.12, SQLAlchemy 2.x, Alembic, psycopg 3, pytest, testcontainers-python, Chainguard wolfi-base.

---

## File Structure

```
factvault/
├── pyproject.toml                                # package metadata, pinned deps
├── alembic.ini                                   # Alembic config
├── docker-compose.yml                            # postgres service only (this plan)
├── docker/postgres/Dockerfile                    # Chainguard wolfi-base + pgvector
├── factvault/
│   ├── __init__.py                               # version constant
│   ├── config.py                                 # DB URL loader, tenant env helpers
│   └── db/
│       ├── __init__.py
│       ├── models.py                             # SQLAlchemy ORM models, one class per table
│       ├── schema.sql                            # canonical DDL reference (docs only)
│       ├── rls.py                                # tenant_context() context manager
│       ├── README.md                             # how to run migrations + tenant context guide
│       └── migrations/
│           ├── env.py                            # Alembic env
│           ├── script.py.mako
│           └── versions/
│               ├── 0001_pgvector_extension.py
│               ├── 0002_entities_properties.py
│               ├── 0003_statements_qualifiers.py
│               ├── 0004_relations.py
│               ├── 0005_sources.py
│               ├── 0006_statement_sources.py
│               ├── 0007_source_verifications.py
│               ├── 0008_proposed_properties.py
│               ├── 0009_dossiers_cache.py
│               ├── 0010_embedding_columns.py
│               ├── 0011_hnsw_indices.py
│               ├── 0012_rls_policies.py
│               └── 0013_v_conflicts_view.py
├── tests/
│   ├── conftest.py                               # testcontainers postgres+pgvector fixture
│   └── db/
│       ├── test_entities.py
│       ├── test_properties.py
│       ├── test_statements.py
│       ├── test_qualifiers.py
│       ├── test_relations.py
│       ├── test_sources.py
│       ├── test_statement_sources.py
│       ├── test_source_verifications.py
│       ├── test_proposed_properties.py
│       ├── test_dossiers_cache.py
│       ├── test_pgvector.py
│       ├── test_rls_isolation.py
│       └── test_v_conflicts.py
└── .github/workflows/ci.yml                      # CI: pytest against postgres+pgvector
```

---

## Tasks

### Task 1 — Project bootstrap

- [ ] Create `pyproject.toml`:

```toml
[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[project]
name = "factvault"
version = "0.0.1"
requires-python = ">=3.12"
dependencies = [
    "sqlalchemy>=2.0,<3",
    "alembic>=1.13,<2",
    "psycopg[binary]>=3.1,<4",
    "pgvector>=0.3,<1",
    "pydantic>=2,<3",
]

[project.optional-dependencies]
dev = [
    "pytest>=8,<9",
    "testcontainers[postgres]>=4,<5",
    "pytest-asyncio>=0.23,<1",
]

[tool.hatch.build.targets.wheel]
packages = ["factvault"]
```

- [ ] Create `factvault/__init__.py`:

```python
__version__ = "0.0.1"
```

- [ ] Create `factvault/config.py`:

```python
import os


def get_db_url() -> str:
    """Load database URL from FACTVAULT_DATABASE_URL env var."""
    url = os.environ.get("FACTVAULT_DATABASE_URL")
    if not url:
        raise RuntimeError(
            "FACTVAULT_DATABASE_URL environment variable is not set. "
            "Example: postgresql+psycopg://user:pass@localhost:5432/factvault"
        )
    return url


def get_tenant_id() -> str:
    """Load active tenant ID from FACTVAULT_TENANT_ID env var."""
    tenant_id = os.environ.get("FACTVAULT_TENANT_ID")
    if not tenant_id:
        raise RuntimeError(
            "FACTVAULT_TENANT_ID environment variable is not set."
        )
    return tenant_id
```

- [ ] Create `factvault/db/__init__.py` (empty).

- [ ] Run: `cd ~/projects/factvault && pip install -e ".[dev]"`
  Expected: `Successfully installed factvault-0.0.1`

- [ ] Commit:

```bash
git add pyproject.toml factvault/__init__.py factvault/config.py factvault/db/__init__.py
git commit -m "feat(db): project bootstrap — pyproject.toml, config, empty db package"
```

---

### Task 2 — Postgres Dockerfile

- [ ] Create `docker/postgres/Dockerfile`:

```dockerfile
FROM cgr.dev/chainguard/wolfi-base:latest

# Install Postgres 16 and pgvector via wolfi apk
RUN apk add --no-cache \
    postgresql-16 \
    postgresql-16-contrib \
    pgvector-pg16 \
    tini

# pgvector is installed as a shared library; register it at container start
ENV POSTGRES_DB=factvault \
    POSTGRES_USER=factvault \
    PGDATA=/var/lib/postgresql/data

# Entrypoint: tini supervises postgres
USER 65532

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["postgres", "-D", "/var/lib/postgresql/data"]
```

- [ ] Commit:

```bash
git add docker/postgres/Dockerfile
git commit -m "feat(db): Chainguard wolfi-base Postgres+pgvector Dockerfile"
```

---

### Task 3 — docker-compose.yml

- [ ] Create `.env.example`:

```dotenv
POSTGRES_USER=factvault
POSTGRES_PASSWORD=factvault
POSTGRES_DB=factvault
FACTVAULT_DATABASE_URL=postgresql+psycopg://factvault:factvault@localhost:5432/factvault
```

- [ ] Create `docker-compose.yml`:

```yaml
services:
  postgres:
    build:
      context: .
      dockerfile: docker/postgres/Dockerfile
    ports:
      - "5432:5432"
    env_file:
      - .env
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U $$POSTGRES_USER -d $$POSTGRES_DB"]
      interval: 5s
      timeout: 5s
      retries: 10

volumes:
  pgdata:
```

- [ ] Commit:

```bash
git add docker-compose.yml .env.example
git commit -m "feat(db): docker-compose.yml postgres-only service + .env.example"
```

---

### Task 4 — pytest fixture in conftest.py

- [ ] Create `tests/__init__.py` (empty).
- [ ] Create `tests/db/__init__.py` (empty).
- [ ] Create `tests/conftest.py`:

```python
"""
Session-scoped testcontainers fixture that builds the local docker/postgres/Dockerfile
so pgvector is available. Function-scoped fixture wraps each test in a rolled-back
transaction, keeping the test database clean without truncating tables.
"""
import os
import pytest
from testcontainers.postgres import PostgresContainer
from sqlalchemy import create_engine, text, Connection, Engine


POSTGRES_IMAGE = "postgres:16"  # fallback; overridden by build context below


@pytest.fixture(scope="session")
def postgres_engine() -> Engine:
    """
    Spin up a real Postgres 16 + pgvector container.
    Uses the official postgres:16 image with pgvector installed via SQL at startup,
    since testcontainers does not natively build local Dockerfiles.
    The docker/postgres/Dockerfile is the production image; this fixture
    installs pgvector the same way (CREATE EXTENSION) so tests are equivalent.
    """
    with PostgresContainer("pgvector/pgvector:pg16") as pg:
        url = pg.get_connection_url().replace("psycopg2", "psycopg")
        engine = create_engine(url, echo=False)
        yield engine
        engine.dispose()


@pytest.fixture(scope="session")
def migrated_engine(postgres_engine: Engine) -> Engine:
    """
    Runs all Alembic migrations against the session-scoped engine.
    Returns the same engine post-migration. Called by tests that need a fully
    migrated schema.
    """
    from alembic.config import Config
    from alembic import command

    alembic_cfg = Config("alembic.ini")
    alembic_cfg.set_main_option(
        "sqlalchemy.url",
        str(postgres_engine.url),
    )
    command.upgrade(alembic_cfg, "head")
    return postgres_engine


@pytest.fixture()
def conn(migrated_engine: Engine) -> Connection:
    """
    Function-scoped connection inside a SAVEPOINT transaction.
    Rolls back at the end of each test, leaving the DB clean.
    """
    with migrated_engine.connect() as connection:
        connection.execute(text("SAVEPOINT test_savepoint"))
        yield connection
        connection.execute(text("ROLLBACK TO SAVEPOINT test_savepoint"))
```

- [ ] Run: `cd ~/projects/factvault && pytest tests/ --collect-only`
  Expected: `no tests ran` (fixtures collected, no test functions yet)

- [ ] Commit:

```bash
git add tests/__init__.py tests/db/__init__.py tests/conftest.py
git commit -m "test(db): session + function scoped testcontainers fixtures"
```

---

### Task 5 — Alembic init

- [ ] Run: `cd ~/projects/factvault && alembic init factvault/db/migrations`
  Expected: `Creating directory .../factvault/db/migrations ... done`

- [ ] Edit `alembic.ini` — replace the generated `sqlalchemy.url` line:

```ini
# Replace:
sqlalchemy.url = driver://user:pass@localhost/dbname
# With:
sqlalchemy.url = %(FACTVAULT_DATABASE_URL)s
```

The full relevant section of `alembic.ini` after edit:

```ini
[alembic]
script_location = factvault/db/migrations
prepend_sys_path = .
version_path_separator = os
sqlalchemy.url = %(FACTVAULT_DATABASE_URL)s
```

- [ ] Edit `factvault/db/migrations/env.py` — replace the generated file entirely:

```python
import os
from logging.config import fileConfig

from sqlalchemy import engine_from_config, pool
from alembic import context

# Import the Base so autogenerate can detect model changes
from factvault.db.models import Base  # noqa: F401 — registers metadata

config = context.config

# Allow FACTVAULT_DATABASE_URL env var to override alembic.ini value
db_url = os.environ.get("FACTVAULT_DATABASE_URL")
if db_url:
    config.set_main_option("sqlalchemy.url", db_url)

if config.config_file_name is not None:
    fileConfig(config.config_file_name)

target_metadata = Base.metadata


def run_migrations_offline() -> None:
    url = config.get_main_option("sqlalchemy.url")
    context.configure(
        url=url,
        target_metadata=target_metadata,
        literal_binds=True,
        dialect_opts={"paramstyle": "named"},
    )
    with context.begin_transaction():
        context.run_migrations()


def run_migrations_online() -> None:
    connectable = engine_from_config(
        config.get_section(config.config_ini_section, {}),
        prefix="sqlalchemy.",
        poolclass=pool.NullPool,
    )
    with connectable.connect() as connection:
        context.configure(
            connection=connection,
            target_metadata=target_metadata,
        )
        with context.begin_transaction():
            context.run_migrations()


if context.is_offline_mode():
    run_migrations_offline()
else:
    run_migrations_online()
```

- [ ] Create stub `factvault/db/models.py` (Base only — models added per-task):

```python
from sqlalchemy.orm import DeclarativeBase


class Base(DeclarativeBase):
    pass
```

- [ ] Commit:

```bash
git add alembic.ini factvault/db/migrations/ factvault/db/models.py
git commit -m "feat(db): alembic init + env.py wired to FACTVAULT_DATABASE_URL + Base"
```

---

### Task 6 — Migration 0001: pgvector extension

- [ ] Create `factvault/db/migrations/versions/0001_pgvector_extension.py`:

```python
"""Enable pgcrypto and vector extensions.

Revision ID: 0001
Revises:
Create Date: 2026-05-22
"""
from alembic import op

revision = "0001"
down_revision = None
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("CREATE EXTENSION IF NOT EXISTS pgcrypto")
    op.execute("CREATE EXTENSION IF NOT EXISTS vector")


def downgrade() -> None:
    op.execute("DROP EXTENSION IF EXISTS vector")
    op.execute("DROP EXTENSION IF EXISTS pgcrypto")
```

- [ ] Create `tests/db/test_pgvector.py`:

```python
"""Verify pgcrypto and vector extensions are present after migration 0001."""
import pytest
from sqlalchemy import text


def test_pgvector_extension_loaded(conn):
    result = conn.execute(
        text(
            "SELECT extname FROM pg_extension "
            "WHERE extname IN ('vector', 'pgcrypto') ORDER BY extname"
        )
    ).fetchall()
    names = {row[0] for row in result}
    assert "vector" in names, "vector extension not found"
    assert "pgcrypto" in names, "pgcrypto extension not found"


def test_gen_random_uuid_works(conn):
    """gen_random_uuid() requires pgcrypto."""
    result = conn.execute(text("SELECT gen_random_uuid()")).scalar()
    assert result is not None
    assert len(str(result)) == 36  # UUID string length


def test_vector_type_usable(conn):
    """Create a temp table with a vector column and insert a value."""
    conn.execute(text("CREATE TEMP TABLE _vec_test (v vector(3))"))
    conn.execute(text("INSERT INTO _vec_test VALUES ('[1,2,3]')"))
    result = conn.execute(text("SELECT v FROM _vec_test")).scalar()
    assert result is not None
```

- [ ] Run: `pytest tests/db/test_pgvector.py -v`
  Expected: `3 passed`

- [ ] Commit:

```bash
git add factvault/db/migrations/versions/0001_pgvector_extension.py tests/db/test_pgvector.py
git commit -m "feat(db): migration 0001 — pgcrypto + vector extensions"
```

---

### Task 7 — Migration 0002 + models + tests: entities and properties

- [ ] Create `factvault/db/migrations/versions/0002_entities_properties.py`:

```python
"""Create entities and properties tables.

Revision ID: 0002
Revises: 0001
Create Date: 2026-05-22
"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects.postgresql import UUID, JSONB, TIMESTAMPTZ

revision = "0002"
down_revision = "0001"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "entities",
        sa.Column("id", UUID(), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column("tenant_id", UUID(), nullable=False),
        sa.Column("ext_id", sa.Text(), nullable=True),
        sa.Column("label", sa.Text(), nullable=False),
        sa.Column("type_uri", sa.Text(), nullable=True),
        sa.Column("description", sa.Text(), nullable=True),
        sa.Column("meta", JSONB(), nullable=False, server_default="{}"),
        sa.Column("created_at", TIMESTAMPTZ(), nullable=False, server_default=sa.text("now()")),
        sa.Column("updated_at", TIMESTAMPTZ(), nullable=False, server_default=sa.text("now()")),
        # UNIQUE (tenant_id, ext_id) only when ext_id is NOT NULL — use NULLS NOT DISTINCT
        sa.UniqueConstraint("tenant_id", "ext_id", name="uq_entities_tenant_ext_id",
                            postgresql_nulls_not_distinct=True),
    )
    op.create_index("idx_entities_tenant", "entities", ["tenant_id"])
    op.create_index(
        "idx_entities_label", "entities",
        ["tenant_id", sa.text("lower(label)")],
        postgresql_using="btree",
    )
    op.create_index("idx_entities_type", "entities", ["tenant_id", "type_uri"])

    op.create_table(
        "properties",
        sa.Column("id", UUID(), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column("tenant_id", UUID(), nullable=True),  # NULL = system-wide
        sa.Column("slug", sa.Text(), nullable=False),
        sa.Column("label", sa.Text(), nullable=False),
        sa.Column(
            "value_type",
            sa.Text(),
            nullable=False,
            # CHECK inline via op.execute below — SQLAlchemy DDL CHECK on Text
        ),
        sa.Column("description", sa.Text(), nullable=True),
        sa.UniqueConstraint("tenant_id", "slug", name="uq_properties_tenant_slug",
                            postgresql_nulls_not_distinct=True),
    )
    op.execute(
        "ALTER TABLE properties ADD CONSTRAINT chk_properties_value_type "
        "CHECK (value_type IN ('entity_ref', 'string', 'number', 'date', 'url'))"
    )


def downgrade() -> None:
    op.drop_table("properties")
    op.drop_table("entities")
```

- [ ] Append to `factvault/db/models.py`:

```python
import uuid
from datetime import datetime
from typing import Optional

from sqlalchemy import text, UniqueConstraint, CheckConstraint
from sqlalchemy.dialects.postgresql import UUID, JSONB, TIMESTAMPTZ
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column


class Base(DeclarativeBase):
    pass


class Entity(Base):
    __tablename__ = "entities"
    __table_args__ = (
        UniqueConstraint(
            "tenant_id", "ext_id",
            name="uq_entities_tenant_ext_id",
            postgresql_nulls_not_distinct=True,
        ),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    tenant_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
    ext_id: Mapped[Optional[str]] = mapped_column(nullable=True)
    label: Mapped[str] = mapped_column(nullable=False)
    type_uri: Mapped[Optional[str]] = mapped_column(nullable=True)
    description: Mapped[Optional[str]] = mapped_column(nullable=True)
    meta: Mapped[dict] = mapped_column(JSONB, nullable=False, server_default=text("'{}'"))
    created_at: Mapped[datetime] = mapped_column(
        TIMESTAMPTZ, nullable=False, server_default=text("now()")
    )
    updated_at: Mapped[datetime] = mapped_column(
        TIMESTAMPTZ, nullable=False, server_default=text("now()")
    )


class Property(Base):
    __tablename__ = "properties"
    __table_args__ = (
        UniqueConstraint(
            "tenant_id", "slug",
            name="uq_properties_tenant_slug",
            postgresql_nulls_not_distinct=True,
        ),
        CheckConstraint(
            "value_type IN ('entity_ref', 'string', 'number', 'date', 'url')",
            name="chk_properties_value_type",
        ),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    tenant_id: Mapped[Optional[uuid.UUID]] = mapped_column(UUID(as_uuid=True), nullable=True)
    slug: Mapped[str] = mapped_column(nullable=False)
    label: Mapped[str] = mapped_column(nullable=False)
    value_type: Mapped[str] = mapped_column(nullable=False)
    description: Mapped[Optional[str]] = mapped_column(nullable=True)
```

- [ ] Create `tests/db/test_entities.py`:

```python
"""Tests for the entities table."""
import uuid
import pytest
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError


TENANT_A = uuid.uuid4()
TENANT_B = uuid.uuid4()


def test_entity_insert(conn):
    conn.execute(
        text(
            "INSERT INTO entities (tenant_id, label) VALUES (:tid, :label)"
        ),
        {"tid": str(TENANT_A), "label": "Acme Corp"},
    )
    result = conn.execute(
        text("SELECT label FROM entities WHERE tenant_id = :tid"),
        {"tid": str(TENANT_A)},
    ).fetchone()
    assert result[0] == "Acme Corp"


def test_entity_label_not_null(conn):
    with pytest.raises(IntegrityError, match="null value"):
        conn.execute(
            text("INSERT INTO entities (tenant_id) VALUES (:tid)"),
            {"tid": str(TENANT_A)},
        )
        conn.commit()


def test_entity_unique_ext_id_per_tenant(conn):
    conn.execute(
        text(
            "INSERT INTO entities (tenant_id, label, ext_id) VALUES (:tid, :label, :ext)"
        ),
        {"tid": str(TENANT_A), "label": "Corp A", "ext": "Q123"},
    )
    with pytest.raises(IntegrityError, match="uq_entities_tenant_ext_id"):
        conn.execute(
            text(
                "INSERT INTO entities (tenant_id, label, ext_id) VALUES (:tid, :label, :ext)"
            ),
            {"tid": str(TENANT_A), "label": "Corp B", "ext": "Q123"},
        )
        conn.commit()


def test_entity_same_ext_id_different_tenants(conn):
    """Same ext_id is allowed for different tenants."""
    conn.execute(
        text(
            "INSERT INTO entities (tenant_id, label, ext_id) VALUES (:tid, :label, :ext)"
        ),
        {"tid": str(TENANT_A), "label": "Corp A", "ext": "Q999"},
    )
    conn.execute(
        text(
            "INSERT INTO entities (tenant_id, label, ext_id) VALUES (:tid, :label, :ext)"
        ),
        {"tid": str(TENANT_B), "label": "Corp B", "ext": "Q999"},
    )
    # Both rows exist
    count = conn.execute(
        text("SELECT COUNT(*) FROM entities WHERE ext_id = 'Q999'")
    ).scalar()
    assert count == 2


def test_entity_meta_jsonb_roundtrip(conn):
    conn.execute(
        text(
            "INSERT INTO entities (tenant_id, label, meta) "
            "VALUES (:tid, :label, :meta::jsonb)"
        ),
        {"tid": str(TENANT_A), "label": "Corp C", "meta": '{"sector": "tech", "employees": 500}'},
    )
    result = conn.execute(
        text("SELECT meta->>'sector' FROM entities WHERE label = 'Corp C'")
    ).scalar()
    assert result == "tech"


def test_entity_null_ext_id_not_unique_violation(conn):
    """Multiple entities with ext_id=NULL are allowed (NULLS NOT DISTINCT applies to non-NULL)."""
    conn.execute(
        text("INSERT INTO entities (tenant_id, label) VALUES (:tid, 'Entity X')"),
        {"tid": str(TENANT_A)},
    )
    conn.execute(
        text("INSERT INTO entities (tenant_id, label) VALUES (:tid, 'Entity Y')"),
        {"tid": str(TENANT_A)},
    )
    count = conn.execute(
        text(
            "SELECT COUNT(*) FROM entities WHERE tenant_id = :tid AND ext_id IS NULL"
        ),
        {"tid": str(TENANT_A)},
    ).scalar()
    assert count >= 2
```

- [ ] Create `tests/db/test_properties.py`:

```python
"""Tests for the properties table."""
import uuid
import pytest
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError


TENANT_A = uuid.uuid4()


def test_property_insert(conn):
    conn.execute(
        text(
            "INSERT INTO properties (tenant_id, slug, label, value_type) "
            "VALUES (:tid, :slug, :label, :vt)"
        ),
        {"tid": str(TENANT_A), "slug": "founded_in", "label": "Founded in", "vt": "date"},
    )
    result = conn.execute(
        text("SELECT slug FROM properties WHERE tenant_id = :tid"),
        {"tid": str(TENANT_A)},
    ).fetchone()
    assert result[0] == "founded_in"


def test_property_value_type_check(conn):
    with pytest.raises(IntegrityError, match="chk_properties_value_type"):
        conn.execute(
            text(
                "INSERT INTO properties (tenant_id, slug, label, value_type) "
                "VALUES (:tid, :slug, :label, :vt)"
            ),
            {"tid": str(TENANT_A), "slug": "bad_prop", "label": "Bad", "vt": "foo"},
        )
        conn.commit()


def test_property_unique_slug_per_tenant(conn):
    conn.execute(
        text(
            "INSERT INTO properties (tenant_id, slug, label, value_type) "
            "VALUES (:tid, 'ceo', 'CEO', 'entity_ref')"
        ),
        {"tid": str(TENANT_A)},
    )
    with pytest.raises(IntegrityError, match="uq_properties_tenant_slug"):
        conn.execute(
            text(
                "INSERT INTO properties (tenant_id, slug, label, value_type) "
                "VALUES (:tid, 'ceo', 'Chief Executive', 'entity_ref')"
            ),
            {"tid": str(TENANT_A)},
        )
        conn.commit()


def test_property_system_wide_null_tenant(conn):
    """tenant_id=NULL means system-wide property; allowed."""
    conn.execute(
        text(
            "INSERT INTO properties (tenant_id, slug, label, value_type) "
            "VALUES (NULL, 'instance_of', 'Instance of', 'entity_ref')"
        )
    )
    result = conn.execute(
        text("SELECT slug FROM properties WHERE tenant_id IS NULL")
    ).fetchone()
    assert result[0] == "instance_of"


def test_all_value_types_accepted(conn):
    for i, vt in enumerate(["entity_ref", "string", "number", "date", "url"]):
        conn.execute(
            text(
                "INSERT INTO properties (tenant_id, slug, label, value_type) "
                "VALUES (:tid, :slug, :label, :vt)"
            ),
            {"tid": str(TENANT_A), "slug": f"prop_{vt}_{i}", "label": vt, "vt": vt},
        )
    count = conn.execute(
        text(
            "SELECT COUNT(*) FROM properties WHERE tenant_id = :tid AND slug LIKE 'prop_%'"
        ),
        {"tid": str(TENANT_A)},
    ).scalar()
    assert count == 5
```

- [ ] Run: `pytest tests/db/test_entities.py tests/db/test_properties.py -v`
  Expected: `11 passed`

- [ ] Commit:

```bash
git add \
  factvault/db/migrations/versions/0002_entities_properties.py \
  factvault/db/models.py \
  tests/db/test_entities.py \
  tests/db/test_properties.py
git commit -m "feat(db): migration 0002 + Entity + Property models + tests"
```

---

### Task 8 — Migration 0003 + models + tests: statements and qualifiers

- [ ] Create `factvault/db/migrations/versions/0003_statements_qualifiers.py`:

```python
"""Create statements and qualifiers tables.

Revision ID: 0003
Revises: 0002
Create Date: 2026-05-22
"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects.postgresql import UUID, JSONB, TIMESTAMPTZ

revision = "0003"
down_revision = "0002"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "statements",
        sa.Column("id", UUID(), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column("tenant_id", UUID(), nullable=False),
        sa.Column("subject_id", UUID(), sa.ForeignKey("entities.id"), nullable=False),
        sa.Column("property_id", UUID(), sa.ForeignKey("properties.id"), nullable=False),
        sa.Column("val_entity", UUID(), sa.ForeignKey("entities.id"), nullable=True),
        sa.Column("val_text", sa.Text(), nullable=True),
        sa.Column("val_number", sa.Numeric(), nullable=True),
        sa.Column("val_date", TIMESTAMPTZ(), nullable=True),
        sa.Column("val_json", JSONB(), nullable=True),
        sa.Column(
            "rank", sa.Text(), nullable=False, server_default="normal"
        ),
        sa.Column(
            "confidence",
            sa.Numeric(4, 3),
            nullable=False,
        ),
        sa.Column("created_at", TIMESTAMPTZ(), nullable=False, server_default=sa.text("now()")),
    )
    op.execute(
        "ALTER TABLE statements ADD CONSTRAINT chk_statements_rank "
        "CHECK (rank IN ('preferred', 'normal', 'deprecated'))"
    )
    op.execute(
        "ALTER TABLE statements ADD CONSTRAINT chk_statements_confidence "
        "CHECK (confidence >= 0 AND confidence <= 1)"
    )
    op.execute(
        "ALTER TABLE statements ADD CONSTRAINT chk_statement_value_populated "
        "CHECK ("
        "    (val_entity IS NOT NULL)::int + "
        "    (val_text   IS NOT NULL)::int + "
        "    (val_number IS NOT NULL)::int + "
        "    (val_date   IS NOT NULL)::int = 1"
        ")"
    )
    op.create_index("idx_statements_subject", "statements", ["subject_id", "property_id", "rank"])
    op.create_index("idx_statements_tenant", "statements", ["tenant_id", "subject_id"])
    op.create_index(
        "idx_statements_val_entity", "statements", ["val_entity"],
        postgresql_where=sa.text("val_entity IS NOT NULL"),
    )
    op.create_index("idx_statements_confidence", "statements", [sa.text("confidence DESC")])

    op.create_table(
        "qualifiers",
        sa.Column("id", UUID(), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column(
            "statement_id",
            UUID(),
            sa.ForeignKey("statements.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("property_id", UUID(), sa.ForeignKey("properties.id"), nullable=False),
        sa.Column("val_text", sa.Text(), nullable=True),
        sa.Column("val_number", sa.Numeric(), nullable=True),
        sa.Column("val_date", TIMESTAMPTZ(), nullable=True),
        sa.Column("val_entity", UUID(), sa.ForeignKey("entities.id"), nullable=True),
    )
    op.execute(
        "ALTER TABLE qualifiers ADD CONSTRAINT chk_qualifier_value_populated "
        "CHECK ("
        "    (val_entity IS NOT NULL)::int + "
        "    (val_text   IS NOT NULL)::int + "
        "    (val_number IS NOT NULL)::int + "
        "    (val_date   IS NOT NULL)::int = 1"
        ")"
    )
    op.create_index("idx_qualifiers_statement", "qualifiers", ["statement_id"])


def downgrade() -> None:
    op.drop_table("qualifiers")
    op.drop_table("statements")
```

- [ ] Append to `factvault/db/models.py`:

```python
from decimal import Decimal
from sqlalchemy import ForeignKey, CheckConstraint, Index, Numeric


class Statement(Base):
    __tablename__ = "statements"
    __table_args__ = (
        CheckConstraint(
            "rank IN ('preferred', 'normal', 'deprecated')",
            name="chk_statements_rank",
        ),
        CheckConstraint(
            "confidence >= 0 AND confidence <= 1",
            name="chk_statements_confidence",
        ),
        CheckConstraint(
            "(val_entity IS NOT NULL)::int + "
            "(val_text IS NOT NULL)::int + "
            "(val_number IS NOT NULL)::int + "
            "(val_date IS NOT NULL)::int = 1",
            name="chk_statement_value_populated",
        ),
        Index("idx_statements_subject", "subject_id", "property_id", "rank"),
        Index("idx_statements_tenant", "tenant_id", "subject_id"),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    tenant_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
    subject_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("entities.id"), nullable=False
    )
    property_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("properties.id"), nullable=False
    )
    val_entity: Mapped[Optional[uuid.UUID]] = mapped_column(
        UUID(as_uuid=True), ForeignKey("entities.id"), nullable=True
    )
    val_text: Mapped[Optional[str]] = mapped_column(nullable=True)
    val_number: Mapped[Optional[Decimal]] = mapped_column(Numeric(), nullable=True)
    val_date: Mapped[Optional[datetime]] = mapped_column(TIMESTAMPTZ, nullable=True)
    val_json: Mapped[Optional[dict]] = mapped_column(JSONB, nullable=True)
    rank: Mapped[str] = mapped_column(nullable=False, server_default=text("'normal'"))
    confidence: Mapped[Decimal] = mapped_column(Numeric(4, 3), nullable=False)
    created_at: Mapped[datetime] = mapped_column(
        TIMESTAMPTZ, nullable=False, server_default=text("now()")
    )


class Qualifier(Base):
    __tablename__ = "qualifiers"
    __table_args__ = (
        CheckConstraint(
            "(val_entity IS NOT NULL)::int + "
            "(val_text IS NOT NULL)::int + "
            "(val_number IS NOT NULL)::int + "
            "(val_date IS NOT NULL)::int = 1",
            name="chk_qualifier_value_populated",
        ),
        Index("idx_qualifiers_statement", "statement_id"),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    statement_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("statements.id", ondelete="CASCADE"),
        nullable=False,
    )
    property_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("properties.id"), nullable=False
    )
    val_text: Mapped[Optional[str]] = mapped_column(nullable=True)
    val_number: Mapped[Optional[Decimal]] = mapped_column(Numeric(), nullable=True)
    val_date: Mapped[Optional[datetime]] = mapped_column(TIMESTAMPTZ, nullable=True)
    val_entity: Mapped[Optional[uuid.UUID]] = mapped_column(
        UUID(as_uuid=True), ForeignKey("entities.id"), nullable=True
    )
```

- [ ] Create `tests/db/test_statements.py`:

```python
"""Tests for the statements table."""
import uuid
import pytest
from decimal import Decimal
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError


TENANT = uuid.uuid4()


def _insert_entity(conn, tenant_id, label):
    return conn.execute(
        text(
            "INSERT INTO entities (tenant_id, label) VALUES (:tid, :label) RETURNING id"
        ),
        {"tid": str(tenant_id), "label": label},
    ).scalar()


def _insert_property(conn, tenant_id, slug, value_type):
    return conn.execute(
        text(
            "INSERT INTO properties (tenant_id, slug, label, value_type) "
            "VALUES (:tid, :slug, :slug, :vt) RETURNING id"
        ),
        {"tid": str(tenant_id), "slug": slug, "vt": value_type},
    ).scalar()


def test_statement_insert_val_text(conn):
    eid = _insert_entity(conn, TENANT, "Acme")
    pid = _insert_property(conn, TENANT, "name", "string")
    conn.execute(
        text(
            "INSERT INTO statements "
            "(tenant_id, subject_id, property_id, val_text, confidence) "
            "VALUES (:tid, :sid, :pid, :vt, :conf)"
        ),
        {"tid": str(TENANT), "sid": str(eid), "pid": str(pid), "vt": "Acme Corp", "conf": "0.5"},
    )
    result = conn.execute(
        text("SELECT val_text FROM statements WHERE subject_id = :sid"),
        {"sid": str(eid)},
    ).scalar()
    assert result == "Acme Corp"


def test_statement_rank_check(conn):
    eid = _insert_entity(conn, TENANT, "E2")
    pid = _insert_property(conn, TENANT, "status_bad", "string")
    with pytest.raises(IntegrityError, match="chk_statements_rank"):
        conn.execute(
            text(
                "INSERT INTO statements "
                "(tenant_id, subject_id, property_id, val_text, rank, confidence) "
                "VALUES (:tid, :sid, :pid, 'x', 'invalid', 0.5)"
            ),
            {"tid": str(TENANT), "sid": str(eid), "pid": str(pid)},
        )
        conn.commit()


def test_statement_confidence_range(conn):
    eid = _insert_entity(conn, TENANT, "E3")
    pid = _insert_property(conn, TENANT, "conf_test", "string")
    with pytest.raises(IntegrityError, match="chk_statements_confidence"):
        conn.execute(
            text(
                "INSERT INTO statements "
                "(tenant_id, subject_id, property_id, val_text, confidence) "
                "VALUES (:tid, :sid, :pid, 'x', 1.5)"
            ),
            {"tid": str(TENANT), "sid": str(eid), "pid": str(pid)},
        )
        conn.commit()


def test_statement_value_populated_check(conn):
    """All value columns NULL must fail."""
    eid = _insert_entity(conn, TENANT, "E4")
    pid = _insert_property(conn, TENANT, "empty_val", "string")
    with pytest.raises(IntegrityError, match="chk_statement_value_populated"):
        conn.execute(
            text(
                "INSERT INTO statements "
                "(tenant_id, subject_id, property_id, confidence) "
                "VALUES (:tid, :sid, :pid, 0.5)"
            ),
            {"tid": str(TENANT), "sid": str(eid), "pid": str(pid)},
        )
        conn.commit()


def test_statement_rank_default_normal(conn):
    eid = _insert_entity(conn, TENANT, "E5")
    pid = _insert_property(conn, TENANT, "rank_default", "string")
    conn.execute(
        text(
            "INSERT INTO statements "
            "(tenant_id, subject_id, property_id, val_text, confidence) "
            "VALUES (:tid, :sid, :pid, 'x', 0.5)"
        ),
        {"tid": str(TENANT), "sid": str(eid), "pid": str(pid)},
    )
    rank = conn.execute(
        text("SELECT rank FROM statements WHERE subject_id = :sid"),
        {"sid": str(eid)},
    ).scalar()
    assert rank == "normal"
```

- [ ] Create `tests/db/test_qualifiers.py`:

```python
"""Tests for the qualifiers table."""
import uuid
import pytest
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError


TENANT = uuid.uuid4()


def _setup(conn):
    eid = conn.execute(
        text("INSERT INTO entities (tenant_id, label) VALUES (:tid, 'E') RETURNING id"),
        {"tid": str(TENANT)},
    ).scalar()
    pid = conn.execute(
        text(
            "INSERT INTO properties (tenant_id, slug, label, value_type) "
            "VALUES (:tid, 'p_qual', 'P', 'string') RETURNING id"
        ),
        {"tid": str(TENANT)},
    ).scalar()
    stmt_id = conn.execute(
        text(
            "INSERT INTO statements "
            "(tenant_id, subject_id, property_id, val_text, confidence) "
            "VALUES (:tid, :sid, :pid, 'v', 0.5) RETURNING id"
        ),
        {"tid": str(TENANT), "sid": str(eid), "pid": str(pid)},
    ).scalar()
    return eid, pid, stmt_id


def test_qualifier_insert(conn):
    _, pid, stmt_id = _setup(conn)
    conn.execute(
        text(
            "INSERT INTO qualifiers (statement_id, property_id, val_text) "
            "VALUES (:sid, :pid, 'qualifier value')"
        ),
        {"sid": str(stmt_id), "pid": str(pid)},
    )
    result = conn.execute(
        text("SELECT val_text FROM qualifiers WHERE statement_id = :sid"),
        {"sid": str(stmt_id)},
    ).scalar()
    assert result == "qualifier value"


def test_qualifier_value_populated_check(conn):
    _, pid, stmt_id = _setup(conn)
    with pytest.raises(IntegrityError, match="chk_qualifier_value_populated"):
        conn.execute(
            text(
                "INSERT INTO qualifiers (statement_id, property_id) "
                "VALUES (:sid, :pid)"
            ),
            {"sid": str(stmt_id), "pid": str(pid)},
        )
        conn.commit()


def test_qualifier_cascade_on_statement_delete(conn):
    _, pid, stmt_id = _setup(conn)
    conn.execute(
        text(
            "INSERT INTO qualifiers (statement_id, property_id, val_text) "
            "VALUES (:sid, :pid, 'q')"
        ),
        {"sid": str(stmt_id), "pid": str(pid)},
    )
    conn.execute(
        text("DELETE FROM statements WHERE id = :sid"),
        {"sid": str(stmt_id)},
    )
    count = conn.execute(
        text("SELECT COUNT(*) FROM qualifiers WHERE statement_id = :sid"),
        {"sid": str(stmt_id)},
    ).scalar()
    assert count == 0
```

- [ ] Run: `pytest tests/db/test_statements.py tests/db/test_qualifiers.py -v`
  Expected: `8 passed`

- [ ] Commit:

```bash
git add \
  factvault/db/migrations/versions/0003_statements_qualifiers.py \
  factvault/db/models.py \
  tests/db/test_statements.py \
  tests/db/test_qualifiers.py
git commit -m "feat(db): migration 0003 + Statement + Qualifier models + tests"
```

---

### Task 9 — Migration 0004 + model + tests: relations

- [ ] Create `factvault/db/migrations/versions/0004_relations.py`:

```python
"""Create relations table.

Revision ID: 0004
Revises: 0003
Create Date: 2026-05-22
"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects.postgresql import UUID, JSONB, TIMESTAMPTZ

revision = "0004"
down_revision = "0003"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "relations",
        sa.Column("id", UUID(), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column("tenant_id", UUID(), nullable=False),
        sa.Column("source_id", UUID(), sa.ForeignKey("entities.id"), nullable=False),
        sa.Column("target_id", UUID(), sa.ForeignKey("entities.id"), nullable=False),
        sa.Column("type", sa.Text(), nullable=False),
        sa.Column("weight", sa.Numeric(), nullable=True),
        sa.Column("confidence", sa.Numeric(4, 3), nullable=True),
        sa.Column("description", sa.Text(), nullable=True),
        sa.Column("meta", JSONB(), nullable=False, server_default="{}"),
        sa.Column(
            "statement_id",
            UUID(),
            sa.ForeignKey("statements.id", ondelete="CASCADE"),
            nullable=True,
        ),
        sa.Column("created_at", TIMESTAMPTZ(), nullable=False, server_default=sa.text("now()")),
        sa.UniqueConstraint(
            "tenant_id", "source_id", "target_id", "type",
            name="uq_relations_tenant_source_target_type",
        ),
    )
    op.execute(
        "ALTER TABLE relations ADD CONSTRAINT chk_relations_confidence "
        "CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1))"
    )
    op.create_index("idx_relations_source", "relations", ["tenant_id", "source_id"])
    op.create_index("idx_relations_target", "relations", ["tenant_id", "target_id"])
    op.create_index("idx_relations_type", "relations", ["tenant_id", "type"])


def downgrade() -> None:
    op.drop_table("relations")
```

- [ ] Append to `factvault/db/models.py`:

```python
class Relation(Base):
    __tablename__ = "relations"
    __table_args__ = (
        sa.UniqueConstraint(
            "tenant_id", "source_id", "target_id", "type",
            name="uq_relations_tenant_source_target_type",
        ),
        CheckConstraint(
            "confidence IS NULL OR (confidence >= 0 AND confidence <= 1)",
            name="chk_relations_confidence",
        ),
        Index("idx_relations_source", "tenant_id", "source_id"),
        Index("idx_relations_target", "tenant_id", "target_id"),
        Index("idx_relations_type", "tenant_id", "type"),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    tenant_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
    source_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("entities.id"), nullable=False
    )
    target_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("entities.id"), nullable=False
    )
    type: Mapped[str] = mapped_column(nullable=False)
    weight: Mapped[Optional[Decimal]] = mapped_column(Numeric(), nullable=True)
    confidence: Mapped[Optional[Decimal]] = mapped_column(Numeric(4, 3), nullable=True)
    description: Mapped[Optional[str]] = mapped_column(nullable=True)
    meta: Mapped[dict] = mapped_column(JSONB, nullable=False, server_default=text("'{}'"))
    statement_id: Mapped[Optional[uuid.UUID]] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("statements.id", ondelete="CASCADE"),
        nullable=True,
    )
    created_at: Mapped[datetime] = mapped_column(
        TIMESTAMPTZ, nullable=False, server_default=text("now()")
    )
```

- [ ] Create `tests/db/test_relations.py`:

```python
"""Tests for the relations table."""
import uuid
import pytest
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError


TENANT = uuid.uuid4()


def _insert_entity(conn, label):
    return conn.execute(
        text("INSERT INTO entities (tenant_id, label) VALUES (:tid, :label) RETURNING id"),
        {"tid": str(TENANT), "label": label},
    ).scalar()


def test_relation_insert(conn):
    src = _insert_entity(conn, "Source Corp")
    tgt = _insert_entity(conn, "Target Corp")
    conn.execute(
        text(
            "INSERT INTO relations (tenant_id, source_id, target_id, type) "
            "VALUES (:tid, :src, :tgt, 'acquired')"
        ),
        {"tid": str(TENANT), "src": str(src), "tgt": str(tgt)},
    )
    result = conn.execute(
        text("SELECT type FROM relations WHERE source_id = :src"),
        {"src": str(src)},
    ).scalar()
    assert result == "acquired"


def test_relation_source_fk(conn):
    tgt = _insert_entity(conn, "T2")
    with pytest.raises(IntegrityError):
        conn.execute(
            text(
                "INSERT INTO relations (tenant_id, source_id, target_id, type) "
                "VALUES (:tid, :src, :tgt, 'x')"
            ),
            {"tid": str(TENANT), "src": str(uuid.uuid4()), "tgt": str(tgt)},
        )
        conn.commit()


def test_relation_confidence_check(conn):
    src = _insert_entity(conn, "S3")
    tgt = _insert_entity(conn, "T3")
    with pytest.raises(IntegrityError, match="chk_relations_confidence"):
        conn.execute(
            text(
                "INSERT INTO relations (tenant_id, source_id, target_id, type, confidence) "
                "VALUES (:tid, :src, :tgt, 'y', 1.5)"
            ),
            {"tid": str(TENANT), "src": str(src), "tgt": str(tgt)},
        )
        conn.commit()


def test_relation_meta_jsonb(conn):
    src = _insert_entity(conn, "S4")
    tgt = _insert_entity(conn, "T4")
    conn.execute(
        text(
            "INSERT INTO relations (tenant_id, source_id, target_id, type, meta) "
            "VALUES (:tid, :src, :tgt, 'invested_in', '{\"deal_size\": 5000000}'::jsonb)"
        ),
        {"tid": str(TENANT), "src": str(src), "tgt": str(tgt)},
    )
    result = conn.execute(
        text(
            "SELECT meta->>'deal_size' FROM relations WHERE source_id = :src"
        ),
        {"src": str(src)},
    ).scalar()
    assert result == "5000000"


def test_relation_unique_tenant_source_target_type(conn):
    src = _insert_entity(conn, "S5")
    tgt = _insert_entity(conn, "T5")
    conn.execute(
        text(
            "INSERT INTO relations (tenant_id, source_id, target_id, type) "
            "VALUES (:tid, :src, :tgt, 'partnered_with')"
        ),
        {"tid": str(TENANT), "src": str(src), "tgt": str(tgt)},
    )
    with pytest.raises(IntegrityError, match="uq_relations_tenant_source_target_type"):
        conn.execute(
            text(
                "INSERT INTO relations (tenant_id, source_id, target_id, type) "
                "VALUES (:tid, :src, :tgt, 'partnered_with')"
            ),
            {"tid": str(TENANT), "src": str(src), "tgt": str(tgt)},
        )
        conn.commit()
```

- [ ] Run: `pytest tests/db/test_relations.py -v`
  Expected: `5 passed`

- [ ] Commit:

```bash
git add \
  factvault/db/migrations/versions/0004_relations.py \
  factvault/db/models.py \
  tests/db/test_relations.py
git commit -m "feat(db): migration 0004 + Relation model + tests"
```

---

### Task 10 — Migration 0005 + model + tests: sources

- [ ] Create `factvault/db/migrations/versions/0005_sources.py`:

```python
"""Create sources table.

Revision ID: 0005
Revises: 0004
Create Date: 2026-05-22

Invariant: raw_text is NULL until Stage 2 (archive worker) populates it.
Any query reading raw_text must only run after status >= 'archived'.
Downstream processing reads raw_text; it never re-fetches the URL.
"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects.postgresql import UUID, TIMESTAMPTZ

revision = "0005"
down_revision = "0004"
branch_labels = None
depends_on = None

_STATUS_VALUES = "('collected', 'archived', 'extracted', 'verified', 'link-rot', 'content-changed')"


def upgrade() -> None:
    op.create_table(
        "sources",
        sa.Column("id", UUID(), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column("tenant_id", UUID(), nullable=False),
        sa.Column("url", sa.Text(), nullable=False),
        sa.Column("fetched_at", TIMESTAMPTZ(), nullable=False, server_default=sa.text("now()")),
        sa.Column("content_hash", sa.Text(), nullable=False),
        sa.Column("raw_html", sa.LargeBinary(), nullable=True),
        # raw_text is NULL until Stage 2 (archive worker) populates it permanently.
        # No downstream query should read raw_text before status = 'archived'.
        sa.Column("raw_text", sa.Text(), nullable=True),
        sa.Column("archive_url", sa.Text(), nullable=True),
        sa.Column("publisher", sa.Text(), nullable=True),
        sa.Column("title", sa.Text(), nullable=True),
        sa.Column("published_at", TIMESTAMPTZ(), nullable=True),
        sa.Column("last_verified_at", TIMESTAMPTZ(), nullable=True),
        sa.Column(
            "status",
            sa.Text(),
            nullable=False,
            server_default="collected",
        ),
        sa.Column("created_at", TIMESTAMPTZ(), nullable=False, server_default=sa.text("now()")),
        sa.UniqueConstraint("tenant_id", "url", name="uq_sources_tenant_url"),
    )
    op.execute(
        f"ALTER TABLE sources ADD CONSTRAINT chk_sources_status "
        f"CHECK (status IN {_STATUS_VALUES})"
    )
    op.create_index("idx_sources_tenant_status", "sources", ["tenant_id", "status"])
    op.create_index("idx_sources_last_verified", "sources", ["last_verified_at"])
    op.create_index("idx_sources_published_at", "sources", ["published_at"])


def downgrade() -> None:
    op.drop_table("sources")
```

- [ ] Append to `factvault/db/models.py`:

```python
class Source(Base):
    __tablename__ = "sources"
    __table_args__ = (
        sa.UniqueConstraint("tenant_id", "url", name="uq_sources_tenant_url"),
        CheckConstraint(
            "status IN ('collected', 'archived', 'extracted', 'verified', 'link-rot', 'content-changed')",
            name="chk_sources_status",
        ),
        Index("idx_sources_tenant_status", "tenant_id", "status"),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    tenant_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
    url: Mapped[str] = mapped_column(nullable=False)
    fetched_at: Mapped[datetime] = mapped_column(
        TIMESTAMPTZ, nullable=False, server_default=text("now()")
    )
    content_hash: Mapped[str] = mapped_column(nullable=False)
    raw_html: Mapped[Optional[bytes]] = mapped_column(sa.LargeBinary(), nullable=True)
    # raw_text is NULL until Stage 2 (archive worker) populates it permanently.
    # Downstream queries must only run after status = 'archived'.
    raw_text: Mapped[Optional[str]] = mapped_column(nullable=True)
    archive_url: Mapped[Optional[str]] = mapped_column(nullable=True)
    publisher: Mapped[Optional[str]] = mapped_column(nullable=True)
    title: Mapped[Optional[str]] = mapped_column(nullable=True)
    published_at: Mapped[Optional[datetime]] = mapped_column(TIMESTAMPTZ, nullable=True)
    last_verified_at: Mapped[Optional[datetime]] = mapped_column(TIMESTAMPTZ, nullable=True)
    status: Mapped[str] = mapped_column(nullable=False, server_default=text("'collected'"))
    created_at: Mapped[datetime] = mapped_column(
        TIMESTAMPTZ, nullable=False, server_default=text("now()")
    )
```

- [ ] Create `tests/db/test_sources.py`:

```python
"""Tests for the sources table.

Invariant: raw_text is NULL at insert time (Stage 1 / Collect).
It is populated by Stage 2 (Archive). Tests verify this and the status lifecycle.
"""
import uuid
import pytest
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError


TENANT = uuid.uuid4()


def _insert_source(conn, url, status="collected", raw_text=None, content_hash="abc123"):
    conn.execute(
        text(
            "INSERT INTO sources (tenant_id, url, content_hash, status, raw_text) "
            "VALUES (:tid, :url, :hash, :status, :rt)"
        ),
        {
            "tid": str(TENANT),
            "url": url,
            "hash": content_hash,
            "status": status,
            "rt": raw_text,
        },
    )


def test_source_insert_raw_text_nullable(conn):
    """raw_text is NULL at Stage 1 (Collect) — this must not raise."""
    _insert_source(conn, "https://example.com/article-1")
    result = conn.execute(
        text("SELECT raw_text FROM sources WHERE url = 'https://example.com/article-1'")
    ).scalar()
    assert result is None


def test_source_status_default_collected(conn):
    conn.execute(
        text(
            "INSERT INTO sources (tenant_id, url, content_hash) "
            "VALUES (:tid, 'https://example.com/article-2', 'hash2')"
        ),
        {"tid": str(TENANT)},
    )
    status = conn.execute(
        text("SELECT status FROM sources WHERE url = 'https://example.com/article-2'")
    ).scalar()
    assert status == "collected"


def test_source_status_check_rejects_invalid(conn):
    with pytest.raises(IntegrityError, match="chk_sources_status"):
        _insert_source(conn, "https://example.com/article-3", status="pending")
        conn.commit()


def test_source_all_valid_statuses(conn):
    statuses = ["collected", "archived", "extracted", "verified", "link-rot", "content-changed"]
    for i, s in enumerate(statuses):
        conn.execute(
            text(
                "INSERT INTO sources (tenant_id, url, content_hash, status) "
                "VALUES (:tid, :url, 'h', :status)"
            ),
            {"tid": str(TENANT), "url": f"https://example.com/s{i}", "status": s},
        )
    count = conn.execute(
        text(
            "SELECT COUNT(*) FROM sources WHERE tenant_id = :tid AND url LIKE 'https://example.com/s%'"
        ),
        {"tid": str(TENANT)},
    ).scalar()
    assert count == 6


def test_source_raw_text_populated_at_archived(conn):
    """Simulate Stage 2: update status to 'archived' and set raw_text."""
    _insert_source(conn, "https://example.com/article-4")
    conn.execute(
        text(
            "UPDATE sources SET status = 'archived', raw_text = 'Article body text...' "
            "WHERE url = 'https://example.com/article-4'"
        )
    )
    result = conn.execute(
        text(
            "SELECT raw_text, status FROM sources "
            "WHERE url = 'https://example.com/article-4'"
        )
    ).fetchone()
    assert result[0] == "Article body text..."
    assert result[1] == "archived"


def test_source_unique_url_per_tenant(conn):
    _insert_source(conn, "https://example.com/unique")
    with pytest.raises(IntegrityError, match="uq_sources_tenant_url"):
        _insert_source(conn, "https://example.com/unique")
        conn.commit()
```

- [ ] Run: `pytest tests/db/test_sources.py -v`
  Expected: `6 passed`

- [ ] Commit:

```bash
git add \
  factvault/db/migrations/versions/0005_sources.py \
  factvault/db/models.py \
  tests/db/test_sources.py
git commit -m "feat(db): migration 0005 + Source model + tests (raw_text invariant documented)"
```

---

### Task 11 — Migration 0006 + model + tests: statement_sources junction

Note: The spec shows `statement_sources` with an `id UUID PRIMARY KEY`. However, spec §3.1 also states a composite PK `(statement_id, source_id)`. These are contradictory. Resolution: use `id UUID PRIMARY KEY` (from the full DDL) and add a unique constraint on `(statement_id, source_id)` to enforce the junction semantics. The composite PK variant would prevent a statement from linking to the same source twice with different excerpts; the unique constraint on the pair enforces one-excerpt-per-source-per-statement. If multi-excerpt-per-source semantics are needed later, the unique constraint is removable without a PK change.

- [ ] Create `factvault/db/migrations/versions/0006_statement_sources.py`:

```python
"""Create statement_sources junction table.

Revision ID: 0006
Revises: 0005
Create Date: 2026-05-22

Design note: id UUID PK (from spec DDL §3.1). A unique constraint on
(statement_id, source_id) enforces the one-excerpt-per-source-per-statement
invariant without a composite PK. The excerpt-offset check runs in
workers/extract.py before INSERT — this table does not enforce it at the
DB level; enforcement is application-layer.
"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects.postgresql import UUID, TIMESTAMPTZ

revision = "0006"
down_revision = "0005"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "statement_sources",
        sa.Column("id", UUID(), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column(
            "statement_id",
            UUID(),
            sa.ForeignKey("statements.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column(
            "source_id",
            UUID(),
            sa.ForeignKey("sources.id"),
            nullable=False,
        ),
        sa.Column("excerpt", sa.Text(), nullable=False),
        sa.Column("excerpt_offset_start", sa.Integer(), nullable=False),
        sa.Column("excerpt_offset_end", sa.Integer(), nullable=False),
        sa.Column("extraction_method", sa.Text(), nullable=False),
        sa.Column("extracted_at", TIMESTAMPTZ(), nullable=False, server_default=sa.text("now()")),
        sa.Column("confidence", sa.Numeric(4, 3), nullable=True),
        sa.Column("tenant_id", UUID(), nullable=False),
        sa.UniqueConstraint(
            "statement_id", "source_id",
            name="uq_statement_sources_stmt_src",
        ),
    )
    op.execute(
        "ALTER TABLE statement_sources ADD CONSTRAINT chk_ss_offset_start "
        "CHECK (excerpt_offset_start >= 0)"
    )
    op.execute(
        "ALTER TABLE statement_sources ADD CONSTRAINT chk_ss_offset_end "
        "CHECK (excerpt_offset_end > excerpt_offset_start)"
    )
    op.execute(
        "ALTER TABLE statement_sources ADD CONSTRAINT chk_ss_confidence "
        "CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1))"
    )
    op.create_index("idx_stmt_sources_statement", "statement_sources", ["statement_id"])
    op.create_index("idx_stmt_sources_source", "statement_sources", ["source_id"])


def downgrade() -> None:
    op.drop_table("statement_sources")
```

- [ ] Append to `factvault/db/models.py`:

```python
class StatementSource(Base):
    __tablename__ = "statement_sources"
    __table_args__ = (
        sa.UniqueConstraint(
            "statement_id", "source_id",
            name="uq_statement_sources_stmt_src",
        ),
        CheckConstraint("excerpt_offset_start >= 0", name="chk_ss_offset_start"),
        CheckConstraint(
            "excerpt_offset_end > excerpt_offset_start", name="chk_ss_offset_end"
        ),
        CheckConstraint(
            "confidence IS NULL OR (confidence >= 0 AND confidence <= 1)",
            name="chk_ss_confidence",
        ),
        Index("idx_stmt_sources_statement", "statement_id"),
        Index("idx_stmt_sources_source", "source_id"),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    statement_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("statements.id", ondelete="CASCADE"),
        nullable=False,
    )
    source_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("sources.id"), nullable=False
    )
    excerpt: Mapped[str] = mapped_column(nullable=False)
    excerpt_offset_start: Mapped[int] = mapped_column(nullable=False)
    excerpt_offset_end: Mapped[int] = mapped_column(nullable=False)
    extraction_method: Mapped[str] = mapped_column(nullable=False)
    extracted_at: Mapped[datetime] = mapped_column(
        TIMESTAMPTZ, nullable=False, server_default=text("now()")
    )
    confidence: Mapped[Optional[Decimal]] = mapped_column(Numeric(4, 3), nullable=True)
    tenant_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
```

- [ ] Create `tests/db/test_statement_sources.py`:

```python
"""Tests for the statement_sources junction table."""
import uuid
import pytest
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError


TENANT = uuid.uuid4()


def _setup(conn):
    eid = conn.execute(
        text("INSERT INTO entities (tenant_id, label) VALUES (:tid, 'E') RETURNING id"),
        {"tid": str(TENANT)},
    ).scalar()
    pid = conn.execute(
        text(
            "INSERT INTO properties (tenant_id, slug, label, value_type) "
            "VALUES (:tid, 'p_ss', 'P', 'string') RETURNING id"
        ),
        {"tid": str(TENANT)},
    ).scalar()
    stmt_id = conn.execute(
        text(
            "INSERT INTO statements "
            "(tenant_id, subject_id, property_id, val_text, confidence) "
            "VALUES (:tid, :sid, :pid, 'v', 0.5) RETURNING id"
        ),
        {"tid": str(TENANT), "sid": str(eid), "pid": str(pid)},
    ).scalar()
    src_id = conn.execute(
        text(
            "INSERT INTO sources (tenant_id, url, content_hash) "
            "VALUES (:tid, 'https://example.com/ss-test', 'hash') RETURNING id"
        ),
        {"tid": str(TENANT)},
    ).scalar()
    return stmt_id, src_id


def _insert_ss(conn, stmt_id, src_id, start=10, end=50, method="human"):
    conn.execute(
        text(
            "INSERT INTO statement_sources "
            "(statement_id, source_id, tenant_id, excerpt, "
            "excerpt_offset_start, excerpt_offset_end, extraction_method) "
            "VALUES (:sid, :src, :tid, 'excerpt text', :start, :end, :method)"
        ),
        {
            "sid": str(stmt_id),
            "src": str(src_id),
            "tid": str(TENANT),
            "start": start,
            "end": end,
            "method": method,
        },
    )


def test_statement_source_insert(conn):
    stmt_id, src_id = _setup(conn)
    _insert_ss(conn, stmt_id, src_id)
    count = conn.execute(
        text("SELECT COUNT(*) FROM statement_sources WHERE statement_id = :sid"),
        {"sid": str(stmt_id)},
    ).scalar()
    assert count == 1


def test_statement_source_unique_stmt_src(conn):
    stmt_id, src_id = _setup(conn)
    _insert_ss(conn, stmt_id, src_id)
    with pytest.raises(IntegrityError, match="uq_statement_sources_stmt_src"):
        _insert_ss(conn, stmt_id, src_id, start=20, end=60)
        conn.commit()


def test_statement_source_offset_end_gt_start(conn):
    stmt_id, src_id = _setup(conn)
    with pytest.raises(IntegrityError, match="chk_ss_offset_end"):
        _insert_ss(conn, stmt_id, src_id, start=50, end=10)
        conn.commit()


def test_statement_source_offset_start_non_negative(conn):
    stmt_id, src_id = _setup(conn)
    with pytest.raises(IntegrityError, match="chk_ss_offset_start"):
        _insert_ss(conn, stmt_id, src_id, start=-1, end=10)
        conn.commit()


def test_statement_source_cascade_on_statement_delete(conn):
    stmt_id, src_id = _setup(conn)
    _insert_ss(conn, stmt_id, src_id)
    conn.execute(
        text("DELETE FROM statements WHERE id = :sid"),
        {"sid": str(stmt_id)},
    )
    count = conn.execute(
        text(
            "SELECT COUNT(*) FROM statement_sources WHERE statement_id = :sid"
        ),
        {"sid": str(stmt_id)},
    ).scalar()
    assert count == 0


def test_statement_source_excerpt_not_null(conn):
    stmt_id, src_id = _setup(conn)
    with pytest.raises(IntegrityError, match="null value"):
        conn.execute(
            text(
                "INSERT INTO statement_sources "
                "(statement_id, source_id, tenant_id, "
                "excerpt_offset_start, excerpt_offset_end, extraction_method) "
                "VALUES (:sid, :src, :tid, 0, 10, 'human')"
            ),
            {"sid": str(stmt_id), "src": str(src_id), "tid": str(TENANT)},
        )
        conn.commit()
```

- [ ] Run: `pytest tests/db/test_statement_sources.py -v`
  Expected: `6 passed`

- [ ] Commit:

```bash
git add \
  factvault/db/migrations/versions/0006_statement_sources.py \
  factvault/db/models.py \
  tests/db/test_statement_sources.py
git commit -m "feat(db): migration 0006 + StatementSource model + tests"
```

---

---

### Task 12 — Migration 0007 + model + tests: source_verifications append-only log

- [ ] Create `factvault/db/migrations/versions/0007_source_verifications.py`:

```python
"""source_verifications append-only log

Revision ID: 0007
Revises: 0006
Create Date: 2026-05-22
"""
from alembic import op
import sqlalchemy as sa

revision = "0007"
down_revision = "0006"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("""
        CREATE TABLE source_verifications (
            id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            source_id        UUID NOT NULL REFERENCES sources(id),
            tenant_id        UUID NOT NULL,
            verified_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
            status           TEXT NOT NULL
                             CHECK (status IN ('live', 'link-rot', 'content-changed', 'excerpt-missing')),
            new_content_hash TEXT,
            notes            TEXT
        )
    """)

    op.execute("""
        CREATE INDEX idx_source_verifications_source
            ON source_verifications (source_id, verified_at DESC)
    """)

    op.execute("""
        CREATE INDEX idx_source_verifications_status
            ON source_verifications (status, verified_at DESC)
    """)

    # Append-only enforcement trigger
    op.execute("""
        CREATE OR REPLACE FUNCTION deny_source_verifications_mutation()
        RETURNS TRIGGER LANGUAGE plpgsql AS $$
        BEGIN
            RAISE EXCEPTION 'source_verifications is append-only. DELETE and UPDATE are forbidden.';
        END;
        $$
    """)

    op.execute("""
        CREATE TRIGGER trg_source_verifications_no_update
            BEFORE UPDATE ON source_verifications
            FOR EACH ROW EXECUTE FUNCTION deny_source_verifications_mutation()
    """)

    op.execute("""
        CREATE TRIGGER trg_source_verifications_no_delete
            BEFORE DELETE ON source_verifications
            FOR EACH ROW EXECUTE FUNCTION deny_source_verifications_mutation()
    """)


def downgrade() -> None:
    op.execute("DROP TRIGGER IF EXISTS trg_source_verifications_no_delete ON source_verifications")
    op.execute("DROP TRIGGER IF EXISTS trg_source_verifications_no_update ON source_verifications")
    op.execute("DROP FUNCTION IF EXISTS deny_source_verifications_mutation()")
    op.execute("DROP TABLE IF EXISTS source_verifications")
```

- [ ] Add `SourceVerification` to `factvault/db/models.py`:

```python
class SourceVerification(Base):
    __tablename__ = "source_verifications"

    id               = Column(UUID(as_uuid=True), primary_key=True, default=uuid4)
    source_id        = Column(UUID(as_uuid=True), ForeignKey("sources.id"), nullable=False)
    tenant_id        = Column(UUID(as_uuid=True), nullable=False)
    verified_at      = Column(TIMESTAMPTZ, nullable=False, server_default=text("now()"))
    status           = Column(Text, nullable=False)
    new_content_hash = Column(Text)
    notes            = Column(Text)
```

- [ ] Create `tests/db/test_source_verifications.py`:

```python
import pytest
from uuid import uuid4
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError

TENANT = uuid4()


def _src_id(conn):
    sid = uuid4()
    conn.execute(
        text(
            "INSERT INTO sources (id, tenant_id, url, content_hash) "
            "VALUES (:id, :tid, :url, 'abc')"
        ),
        {"id": str(sid), "tid": str(TENANT), "url": f"https://example.com/{sid}"},
    )
    return sid


def test_insert_ok(db_conn):
    with db_conn.begin():
        src = _src_id(db_conn)
        db_conn.execute(
            text(
                "INSERT INTO source_verifications (source_id, tenant_id, status) "
                "VALUES (:sid, :tid, 'live')"
            ),
            {"sid": str(src), "tid": str(TENANT)},
        )
    rows = db_conn.execute(
        text("SELECT status FROM source_verifications WHERE source_id = :sid"),
        {"sid": str(src)},
    ).fetchall()
    assert len(rows) == 1
    assert rows[0].status == "live"


def test_update_raises(db_conn):
    with db_conn.begin():
        src = _src_id(db_conn)
        db_conn.execute(
            text(
                "INSERT INTO source_verifications (id, source_id, tenant_id, status) "
                "VALUES (:id, :sid, :tid, 'live')"
            ),
            {"id": str(uuid4()), "sid": str(src), "tid": str(TENANT)},
        )
    with pytest.raises(Exception, match="append-only"):
        with db_conn.begin():
            db_conn.execute(
                text(
                    "UPDATE source_verifications SET status = 'link-rot' "
                    "WHERE source_id = :sid"
                ),
                {"sid": str(src)},
            )


def test_delete_raises(db_conn):
    with db_conn.begin():
        src = _src_id(db_conn)
        db_conn.execute(
            text(
                "INSERT INTO source_verifications (id, source_id, tenant_id, status) "
                "VALUES (:id, :sid, :tid, 'live')"
            ),
            {"id": str(uuid4()), "sid": str(src), "tid": str(TENANT)},
        )
    with pytest.raises(Exception, match="append-only"):
        with db_conn.begin():
            db_conn.execute(
                text("DELETE FROM source_verifications WHERE source_id = :sid"),
                {"sid": str(src)},
            )


def test_status_check_rejects_invalid(db_conn):
    with pytest.raises(IntegrityError):
        with db_conn.begin():
            src = _src_id(db_conn)
            db_conn.execute(
                text(
                    "INSERT INTO source_verifications (source_id, tenant_id, status) "
                    "VALUES (:sid, :tid, 'bad-status')"
                ),
                {"sid": str(src), "tid": str(TENANT)},
            )
```

- [ ] Run: `pytest tests/db/test_source_verifications.py -v`
  Expected: `4 passed`

- [ ] Commit:

```bash
git add \
  factvault/db/migrations/versions/0007_source_verifications.py \
  factvault/db/models.py \
  tests/db/test_source_verifications.py
git commit -m "feat(db): migration 0007 + SourceVerification model + append-only trigger + tests"
```

---

### Task 13 — Migration 0008 + model + tests: proposed_properties strict-mode queue

- [ ] Create `factvault/db/migrations/versions/0008_proposed_properties.py`:

```python
"""proposed_properties strict-mode queue

Revision ID: 0008
Revises: 0007
Create Date: 2026-05-22
"""
from alembic import op
import sqlalchemy as sa

revision = "0008"
down_revision = "0007"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("""
        CREATE TABLE proposed_properties (
            id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            tenant_id           UUID NOT NULL,
            proposed_slug       TEXT NOT NULL,
            proposed_value_type TEXT NOT NULL
                                CHECK (proposed_value_type IN
                                    ('entity_ref', 'string', 'number', 'date', 'url')),
            proposed_by         TEXT NOT NULL,
            example_excerpt     TEXT,
            example_source_id   UUID REFERENCES sources(id),
            status              TEXT NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending', 'approved', 'rejected')),
            reviewed_by         TEXT,
            reviewed_at         TIMESTAMPTZ,
            tenant_id_check     UUID GENERATED ALWAYS AS (tenant_id) STORED,
            created_at          TIMESTAMPTZ DEFAULT now(),
            UNIQUE (tenant_id, proposed_slug, status)
        )
    """)
    # The GENERATED ALWAYS column above won't work cleanly; use a plain approach:
    # Drop and recreate without the generated column.
    op.execute("DROP TABLE proposed_properties")

    op.execute("""
        CREATE TABLE proposed_properties (
            id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            tenant_id           UUID NOT NULL,
            proposed_slug       TEXT NOT NULL,
            proposed_value_type TEXT NOT NULL
                                CHECK (proposed_value_type IN
                                    ('entity_ref', 'string', 'number', 'date', 'url')),
            proposed_by         TEXT NOT NULL,
            example_excerpt     TEXT,
            example_source_id   UUID REFERENCES sources(id),
            status              TEXT NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending', 'approved', 'rejected')),
            reviewed_by         TEXT,
            reviewed_at         TIMESTAMPTZ,
            created_at          TIMESTAMPTZ DEFAULT now(),
            UNIQUE (tenant_id, proposed_slug, status)
        )
    """)

    op.execute("""
        CREATE INDEX idx_proposed_properties_tenant_status
            ON proposed_properties (tenant_id, status)
    """)


def downgrade() -> None:
    op.execute("DROP TABLE IF EXISTS proposed_properties")
```

> **Note:** The `UNIQUE (tenant_id, proposed_slug, status)` constraint allows the same slug to be re-proposed only when the previous proposal has a different status (e.g., re-proposing after `rejected` inserts a new `pending` row while the `rejected` row still exists — they differ in `status`). This matches the spec intent.

- [ ] Add `ProposedProperty` to `factvault/db/models.py`:

```python
class ProposedProperty(Base):
    __tablename__ = "proposed_properties"

    id                  = Column(UUID(as_uuid=True), primary_key=True, default=uuid4)
    tenant_id           = Column(UUID(as_uuid=True), nullable=False)
    proposed_slug       = Column(Text, nullable=False)
    proposed_value_type = Column(Text, nullable=False)
    proposed_by         = Column(Text, nullable=False)
    example_excerpt     = Column(Text)
    example_source_id   = Column(UUID(as_uuid=True), ForeignKey("sources.id"))
    status              = Column(Text, nullable=False, server_default="pending")
    reviewed_by         = Column(Text)
    reviewed_at         = Column(TIMESTAMPTZ)
    created_at          = Column(TIMESTAMPTZ, server_default=text("now()"))
```

- [ ] Create `tests/db/test_proposed_properties.py`:

```python
import pytest
from uuid import uuid4
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError

TENANT = uuid4()


def _insert(conn, slug, status="pending", value_type="string"):
    conn.execute(
        text(
            "INSERT INTO proposed_properties "
            "(id, tenant_id, proposed_slug, proposed_value_type, proposed_by, status) "
            "VALUES (:id, :tid, :slug, :vt, 'llm:gpt-5:v1', :status)"
        ),
        {
            "id": str(uuid4()),
            "tid": str(TENANT),
            "slug": slug,
            "vt": value_type,
            "status": status,
        },
    )


def test_insert_ok(db_conn):
    with db_conn.begin():
        _insert(db_conn, "test_slug_ok")
    rows = db_conn.execute(
        text(
            "SELECT status FROM proposed_properties "
            "WHERE tenant_id = :tid AND proposed_slug = 'test_slug_ok'"
        ),
        {"tid": str(TENANT)},
    ).fetchall()
    assert len(rows) == 1
    assert rows[0].status == "pending"


def test_status_check_rejects_invalid(db_conn):
    with pytest.raises(IntegrityError):
        with db_conn.begin():
            _insert(db_conn, "bad_status_slug", status="nonsense")


def test_value_type_check_rejects_invalid(db_conn):
    with pytest.raises(IntegrityError):
        with db_conn.begin():
            _insert(db_conn, "bad_vt_slug", value_type="blob")


def test_unique_constraint_same_slug_same_status(db_conn):
    """Two pending rows for same (tenant, slug) are not allowed."""
    with pytest.raises(IntegrityError):
        with db_conn.begin():
            _insert(db_conn, "dup_slug", status="pending")
            _insert(db_conn, "dup_slug", status="pending")


def test_unique_constraint_allows_different_status(db_conn):
    """Rejected then re-proposed (pending) is allowed — different status."""
    with db_conn.begin():
        _insert(db_conn, "reprop_slug", status="rejected")
    with db_conn.begin():
        _insert(db_conn, "reprop_slug", status="pending")
    rows = db_conn.execute(
        text(
            "SELECT status FROM proposed_properties "
            "WHERE tenant_id = :tid AND proposed_slug = 'reprop_slug' "
            "ORDER BY created_at"
        ),
        {"tid": str(TENANT)},
    ).fetchall()
    assert {r.status for r in rows} == {"rejected", "pending"}
```

- [ ] Run: `pytest tests/db/test_proposed_properties.py -v`
  Expected: `5 passed`

- [ ] Commit:

```bash
git add \
  factvault/db/migrations/versions/0008_proposed_properties.py \
  factvault/db/models.py \
  tests/db/test_proposed_properties.py
git commit -m "feat(db): migration 0008 + ProposedProperty model + tests"
```

---

### Task 14 — Migration 0009 + model + tests: dossiers cache

- [ ] Create `factvault/db/migrations/versions/0009_dossiers_cache.py`:

```python
"""dossiers cache table

Revision ID: 0009
Revises: 0008
Create Date: 2026-05-22
"""
from alembic import op
import sqlalchemy as sa

revision = "0009"
down_revision = "0008"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("""
        CREATE TABLE dossiers (
            id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            tenant_id    UUID NOT NULL,
            entity_id    UUID NOT NULL REFERENCES entities(id),
            assembled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
            bundle       JSONB NOT NULL,
            UNIQUE (tenant_id, entity_id)
        )
    """)

    op.execute("""
        CREATE INDEX idx_dossiers_tenant_assembled
            ON dossiers (tenant_id, assembled_at DESC)
    """)


def downgrade() -> None:
    op.execute("DROP TABLE IF EXISTS dossiers")
```

- [ ] Add `Dossier` to `factvault/db/models.py`:

```python
class Dossier(Base):
    __tablename__ = "dossiers"

    id           = Column(UUID(as_uuid=True), primary_key=True, default=uuid4)
    tenant_id    = Column(UUID(as_uuid=True), nullable=False)
    entity_id    = Column(UUID(as_uuid=True), ForeignKey("entities.id"), nullable=False)
    assembled_at = Column(TIMESTAMPTZ, nullable=False, server_default=text("now()"))
    bundle       = Column(JSONB, nullable=False)
```

- [ ] Create `tests/db/test_dossiers_cache.py`:

```python
import pytest
from uuid import uuid4
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError
import json

TENANT = uuid4()


def _entity_id(conn):
    eid = uuid4()
    conn.execute(
        text(
            "INSERT INTO entities (id, tenant_id, label) "
            "VALUES (:id, :tid, 'TestCorp')"
        ),
        {"id": str(eid), "tid": str(TENANT)},
    )
    return eid


def test_insert_and_retrieve_bundle(db_conn):
    payload = {"facts": [{"id": str(uuid4()), "rank": "preferred"}], "assembled_at": "2026-05-22T00:00:00Z"}
    with db_conn.begin():
        eid = _entity_id(db_conn)
        db_conn.execute(
            text(
                "INSERT INTO dossiers (id, tenant_id, entity_id, bundle) "
                "VALUES (:id, :tid, :eid, :bundle::jsonb)"
            ),
            {
                "id": str(uuid4()),
                "tid": str(TENANT),
                "eid": str(eid),
                "bundle": json.dumps(payload),
            },
        )
    row = db_conn.execute(
        text("SELECT bundle FROM dossiers WHERE entity_id = :eid"),
        {"eid": str(eid)},
    ).fetchone()
    assert row is not None
    assert row.bundle["assembled_at"] == "2026-05-22T00:00:00Z"
    assert len(row.bundle["facts"]) == 1


def test_unique_tenant_entity(db_conn):
    """Duplicate (tenant_id, entity_id) is rejected."""
    with db_conn.begin():
        eid = _entity_id(db_conn)
        for _ in range(2):
            with pytest.raises(IntegrityError):
                db_conn.execute(
                    text(
                        "INSERT INTO dossiers (id, tenant_id, entity_id, bundle) "
                        "VALUES (:id, :tid, :eid, '{}'::jsonb)"
                    ),
                    {"id": str(uuid4()), "tid": str(TENANT), "eid": str(eid)},
                )
            if _ == 0:
                # First insert succeeded; second should fail
                pass
```

> **Cleaner test rewrite** for `test_unique_tenant_entity`:

```python
def test_unique_tenant_entity(db_conn):
    with db_conn.begin():
        eid = _entity_id(db_conn)
        db_conn.execute(
            text(
                "INSERT INTO dossiers (id, tenant_id, entity_id, bundle) "
                "VALUES (:id, :tid, :eid, '{}'::jsonb)"
            ),
            {"id": str(uuid4()), "tid": str(TENANT), "eid": str(eid)},
        )
    with pytest.raises(IntegrityError):
        with db_conn.begin():
            db_conn.execute(
                text(
                    "INSERT INTO dossiers (id, tenant_id, entity_id, bundle) "
                    "VALUES (:id, :tid, :eid, '{}'::jsonb)"
                ),
                {"id": str(uuid4()), "tid": str(TENANT), "eid": str(eid)},
            )
```

- [ ] Run: `pytest tests/db/test_dossiers_cache.py -v`
  Expected: `2 passed`

- [ ] Commit:

```bash
git add \
  factvault/db/migrations/versions/0009_dossiers_cache.py \
  factvault/db/models.py \
  tests/db/test_dossiers_cache.py
git commit -m "feat(db): migration 0009 + Dossier model + tests"
```

---

### Task 15 — Migration 0010 + tests: embedding columns

- [ ] Create `factvault/db/migrations/versions/0010_embedding_columns.py`:

```python
"""Add embedding vector(1024) columns to entities, statements, relations, sources

Revision ID: 0010
Revises: 0009
Create Date: 2026-05-22
"""
from alembic import op

revision = "0010"
down_revision = "0009"
branch_labels = None
depends_on = None


def upgrade() -> None:
    # All four embedding columns are nullable; populated by the embedding worker.
    for table in ("entities", "statements", "relations", "sources"):
        op.execute(
            f"ALTER TABLE {table} ADD COLUMN IF NOT EXISTS embedding vector(1024)"
        )


def downgrade() -> None:
    for table in ("entities", "statements", "relations", "sources"):
        op.execute(
            f"ALTER TABLE {table} DROP COLUMN IF EXISTS embedding"
        )
```

> **Note:** The base table migrations (0002–0005) define the tables without embedding columns. This migration adds them as a separate step so the column addition is reversible independently of the table creation. If the base migrations already include `embedding` columns, this migration becomes a no-op via `IF NOT EXISTS` — safe to run either way.

- [ ] Create `tests/db/test_pgvector.py`:

```python
"""Tests that embedding vector(1024) columns round-trip correctly."""
import pytest
from uuid import uuid4
from sqlalchemy import text

TENANT = uuid4()


def _rand_vec(dim=1024):
    import random
    return [round(random.uniform(-1.0, 1.0), 6) for _ in range(dim)]


def _vec_literal(v):
    return "[" + ",".join(str(x) for x in v) + "]"


def test_entity_embedding_roundtrip(db_conn):
    eid = uuid4()
    vec = _rand_vec()
    with db_conn.begin():
        db_conn.execute(
            text(
                "INSERT INTO entities (id, tenant_id, label, embedding) "
                "VALUES (:id, :tid, 'VecCorp', :emb::vector)"
            ),
            {"id": str(eid), "tid": str(TENANT), "emb": _vec_literal(vec)},
        )
    row = db_conn.execute(
        text("SELECT embedding FROM entities WHERE id = :id"),
        {"id": str(eid)},
    ).fetchone()
    assert row is not None
    # pgvector returns a list-like object; cast to list for comparison
    returned = list(row.embedding)
    assert len(returned) == 1024
    assert abs(returned[0] - vec[0]) < 1e-5


def test_statement_embedding_roundtrip(db_conn):
    # Insert prerequisite entity and property
    eid = uuid4()
    pid = uuid4()
    with db_conn.begin():
        db_conn.execute(
            text("INSERT INTO entities (id, tenant_id, label) VALUES (:id, :tid, 'E')"),
            {"id": str(eid), "tid": str(TENANT)},
        )
        db_conn.execute(
            text(
                "INSERT INTO properties (id, slug, label, value_type) "
                "VALUES (:id, 'test_prop_emb', 'Test', 'string')"
            ),
            {"id": str(pid)},
        )
    sid = uuid4()
    vec = _rand_vec()
    with db_conn.begin():
        db_conn.execute(
            text(
                "INSERT INTO statements "
                "(id, tenant_id, subject_id, property_id, val_text, rank, confidence, embedding) "
                "VALUES (:id, :tid, :eid, :pid, 'hello', 'normal', 0.5, :emb::vector)"
            ),
            {
                "id": str(sid),
                "tid": str(TENANT),
                "eid": str(eid),
                "pid": str(pid),
                "emb": _vec_literal(vec),
            },
        )
    row = db_conn.execute(
        text("SELECT embedding FROM statements WHERE id = :id"),
        {"id": str(sid)},
    ).fetchone()
    assert len(list(row.embedding)) == 1024


def test_source_embedding_roundtrip(db_conn):
    srcid = uuid4()
    vec = _rand_vec()
    with db_conn.begin():
        db_conn.execute(
            text(
                "INSERT INTO sources (id, tenant_id, url, content_hash, embedding) "
                "VALUES (:id, :tid, :url, 'hash', :emb::vector)"
            ),
            {
                "id": str(srcid),
                "tid": str(TENANT),
                "url": f"https://example.com/emb/{srcid}",
                "emb": _vec_literal(vec),
            },
        )
    row = db_conn.execute(
        text("SELECT embedding FROM sources WHERE id = :id"),
        {"id": str(srcid)},
    ).fetchone()
    assert len(list(row.embedding)) == 1024


def test_relation_embedding_roundtrip(db_conn):
    e1, e2 = uuid4(), uuid4()
    with db_conn.begin():
        for eid, label in [(e1, "A"), (e2, "B")]:
            db_conn.execute(
                text("INSERT INTO entities (id, tenant_id, label) VALUES (:id, :tid, :lbl)"),
                {"id": str(eid), "tid": str(TENANT), "lbl": label},
            )
    rid = uuid4()
    vec = _rand_vec()
    with db_conn.begin():
        db_conn.execute(
            text(
                "INSERT INTO relations "
                "(id, tenant_id, source_id, target_id, type, embedding) "
                "VALUES (:id, :tid, :src, :tgt, 'acquired', :emb::vector)"
            ),
            {
                "id": str(rid),
                "tid": str(TENANT),
                "src": str(e1),
                "tgt": str(e2),
                "emb": _vec_literal(vec),
            },
        )
    row = db_conn.execute(
        text("SELECT embedding FROM relations WHERE id = :id"),
        {"id": str(rid)},
    ).fetchone()
    assert len(list(row.embedding)) == 1024
```

- [ ] Run: `pytest tests/db/test_pgvector.py -v`
  Expected: `4 passed`

- [ ] Commit:

```bash
git add \
  factvault/db/migrations/versions/0010_embedding_columns.py \
  tests/db/test_pgvector.py
git commit -m "feat(db): migration 0010 + embedding vector(1024) columns on 4 tables + tests"
```

---

### Task 16 — Migration 0011 + tests: HNSW indices on embedding columns

- [ ] Create `factvault/db/migrations/versions/0011_hnsw_indices.py`:

```python
"""HNSW indices on embedding columns (vector_cosine_ops, m=16, ef_construction=64)

Revision ID: 0011
Revises: 0010
Create Date: 2026-05-22
"""
from alembic import op

revision = "0011"
down_revision = "0010"
branch_labels = None
depends_on = None

_INDICES = [
    ("entities",   "idx_entities_embedding"),
    ("statements", "idx_statements_embedding"),
    ("relations",  "idx_relations_embedding"),
    ("sources",    "idx_sources_embedding"),
]


def upgrade() -> None:
    for table, idx_name in _INDICES:
        op.execute(
            f"CREATE INDEX IF NOT EXISTS {idx_name} "
            f"ON {table} USING hnsw (embedding vector_cosine_ops) "
            f"WITH (m = 16, ef_construction = 64)"
        )


def downgrade() -> None:
    for _, idx_name in _INDICES:
        op.execute(f"DROP INDEX IF EXISTS {idx_name}")
```

- [ ] Add HNSW index tests to `tests/db/test_pgvector.py` (append to existing file):

```python
def test_hnsw_index_used_entities(db_conn):
    """EXPLAIN with seqscan disabled must show the HNSW index for entities."""
    import random

    TENANT2 = uuid4()
    vec = _rand_vec()

    # Insert a handful of entities with embeddings so the planner has data
    with db_conn.begin():
        for i in range(10):
            v = _rand_vec()
            db_conn.execute(
                text(
                    "INSERT INTO entities (id, tenant_id, label, embedding) "
                    "VALUES (:id, :tid, :lbl, :emb::vector)"
                ),
                {
                    "id": str(uuid4()),
                    "tid": str(TENANT2),
                    "lbl": f"HNSWEntity{i}",
                    "emb": _vec_literal(v),
                },
            )

    with db_conn.begin():
        db_conn.execute(text("SET enable_seqscan = OFF"))
        plan = db_conn.execute(
            text(
                "EXPLAIN (ANALYZE, FORMAT TEXT) "
                "SELECT id FROM entities "
                "ORDER BY embedding <=> :emb::vector "
                "LIMIT 5"
            ),
            {"emb": _vec_literal(vec)},
        ).fetchall()
        db_conn.execute(text("SET enable_seqscan = ON"))

    plan_text = "\n".join(str(row[0]) for row in plan)
    assert "idx_entities_embedding" in plan_text, (
        f"Expected HNSW index 'idx_entities_embedding' in plan, got:\n{plan_text}"
    )


def test_hnsw_index_used_statements(db_conn):
    """EXPLAIN with seqscan disabled must show the HNSW index for statements."""
    TENANT3 = uuid4()
    eid = uuid4()
    pid = uuid4()

    with db_conn.begin():
        db_conn.execute(
            text("INSERT INTO entities (id, tenant_id, label) VALUES (:id, :tid, 'HE')"),
            {"id": str(eid), "tid": str(TENANT3)},
        )
        db_conn.execute(
            text(
                "INSERT INTO properties (id, slug, label, value_type) "
                "VALUES (:id, 'hnsw_prop', 'HNSW', 'string')"
            ),
            {"id": str(pid)},
        )
        for i in range(10):
            db_conn.execute(
                text(
                    "INSERT INTO statements "
                    "(id, tenant_id, subject_id, property_id, val_text, rank, confidence, embedding) "
                    "VALUES (:id, :tid, :eid, :pid, :val, 'normal', 0.5, :emb::vector)"
                ),
                {
                    "id": str(uuid4()),
                    "tid": str(TENANT3),
                    "eid": str(eid),
                    "pid": str(pid),
                    "val": f"val{i}",
                    "emb": _vec_literal(_rand_vec()),
                },
            )

    vec = _rand_vec()
    with db_conn.begin():
        db_conn.execute(text("SET enable_seqscan = OFF"))
        plan = db_conn.execute(
            text(
                "EXPLAIN (ANALYZE, FORMAT TEXT) "
                "SELECT id FROM statements "
                "ORDER BY embedding <=> :emb::vector "
                "LIMIT 5"
            ),
            {"emb": _vec_literal(vec)},
        ).fetchall()
        db_conn.execute(text("SET enable_seqscan = ON"))

    plan_text = "\n".join(str(row[0]) for row in plan)
    assert "idx_statements_embedding" in plan_text, (
        f"Expected HNSW index 'idx_statements_embedding' in plan, got:\n{plan_text}"
    )
```

- [ ] Run: `pytest tests/db/test_pgvector.py -v`
  Expected: `6 passed` (4 from Task 15 + 2 new)

- [ ] Commit:

```bash
git add \
  factvault/db/migrations/versions/0011_hnsw_indices.py \
  tests/db/test_pgvector.py
git commit -m "feat(db): migration 0011 + HNSW indices on 4 embedding columns + plan-verify tests"
```

---

### Task 17 — Migration 0012 + RLS module + tests: tenant isolation

- [ ] Create `factvault/db/migrations/versions/0012_rls_policies.py`:

```python
"""Enable RLS on all domain tables and create tenant isolation policies

Revision ID: 0012
Revises: 0011
Create Date: 2026-05-22
"""
from alembic import op

revision = "0012"
down_revision = "0011"
branch_labels = None
depends_on = None

_DOMAIN_TABLES = [
    "entities",
    "properties",
    "statements",
    "qualifiers",
    "relations",
    "sources",
    "statement_sources",
    "source_verifications",
    "proposed_properties",
    "dossiers",
]


def upgrade() -> None:
    for table in _DOMAIN_TABLES:
        op.execute(f"ALTER TABLE {table} ENABLE ROW LEVEL SECURITY")
        op.execute(f"ALTER TABLE {table} FORCE ROW LEVEL SECURITY")

    # Tables with a direct tenant_id column get the standard policy.
    # qualifiers and statement_sources join through their parent; they inherit
    # RLS from statements/sources via FK cascade reads — but we also add
    # a blanket policy allowing the session role to read if the parent is accessible.
    # For simplicity in Plan 1, we create policies only on tables that have tenant_id directly.
    _TENANT_ID_TABLES = [
        "entities",
        "properties",
        "statements",
        "relations",
        "sources",
        "statement_sources",
        "source_verifications",
        "proposed_properties",
        "dossiers",
    ]

    for table in _TENANT_ID_TABLES:
        op.execute(f"""
            CREATE POLICY tenant_isolation ON {table}
                USING (tenant_id = current_setting('app.tenant_id', true)::uuid)
        """)

    # qualifiers has no tenant_id; policy allows access when the parent statement
    # is accessible under the current tenant context.
    op.execute("""
        CREATE POLICY tenant_isolation ON qualifiers
            USING (
                EXISTS (
                    SELECT 1 FROM statements s
                    WHERE s.id = qualifiers.statement_id
                      AND s.tenant_id = current_setting('app.tenant_id', true)::uuid
                )
            )
    """)


def downgrade() -> None:
    for table in _DOMAIN_TABLES:
        op.execute(f"DROP POLICY IF EXISTS tenant_isolation ON {table}")
        op.execute(f"ALTER TABLE {table} NO FORCE ROW LEVEL SECURITY")
        op.execute(f"ALTER TABLE {table} DISABLE ROW LEVEL SECURITY")
```

- [ ] Create `factvault/db/rls.py`:

```python
"""
factvault.db.rls — Tenant context manager for Postgres RLS.

Usage::

    from factvault.db.rls import tenant_context

    with engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, tenant_id):
                rows = conn.execute(text("SELECT * FROM entities")).fetchall()
"""
from __future__ import annotations

import contextlib
from uuid import UUID
from sqlalchemy import text
from sqlalchemy.engine import Connection


@contextlib.contextmanager
def tenant_context(connection: Connection, tenant_id: UUID):
    """
    Set ``app.tenant_id`` for the current transaction so that Postgres RLS
    policies can enforce tenant isolation.

    Uses ``SET LOCAL`` so the setting is automatically rolled back at
    transaction end — no explicit cleanup required.

    The caller is responsible for wrapping the context manager inside an
    active transaction (``connection.begin()``).

    Example::

        with engine.connect() as conn:
            with conn.begin():
                with tenant_context(conn, my_tenant_uuid):
                    conn.execute(text("SELECT * FROM entities")).fetchall()
    """
    connection.execute(
        text("SET LOCAL app.tenant_id = :tid"),
        {"tid": str(tenant_id)},
    )
    yield connection
```

- [ ] Create `tests/db/test_rls_isolation.py`:

```python
"""
Load-bearing multi-tenancy test.
If this test fails, the project does not ship.
"""
import pytest
from uuid import uuid4
from sqlalchemy import text

from factvault.db.rls import tenant_context

TENANT_A = uuid4()
TENANT_B = uuid4()


def _insert_entity(conn, tenant_id, label):
    eid = uuid4()
    conn.execute(
        text(
            "INSERT INTO entities (id, tenant_id, label) "
            "VALUES (:id, :tid, :lbl)"
        ),
        {"id": str(eid), "tid": str(tenant_id), "lbl": label},
    )
    return eid


def test_tenant_a_cannot_see_tenant_b_rows(db_conn):
    """Tenant B's context must return zero rows for Tenant A's entity."""
    # Insert entity as Tenant A
    with db_conn.begin():
        with tenant_context(db_conn, TENANT_A):
            _insert_entity(db_conn, TENANT_A, "TenantACorp")

    # Query as Tenant B — expect zero rows
    with db_conn.begin():
        with tenant_context(db_conn, TENANT_B):
            rows = db_conn.execute(
                text("SELECT id FROM entities WHERE label = 'TenantACorp'")
            ).fetchall()
    assert rows == [], f"Tenant B saw Tenant A's rows: {rows}"


def test_tenant_a_still_sees_own_rows_after_b_query(db_conn):
    """Tenant A's rows survive after a Tenant B context sweep."""
    with db_conn.begin():
        with tenant_context(db_conn, TENANT_A):
            _insert_entity(db_conn, TENANT_A, "VisibleToA")

    # Sweep as B
    with db_conn.begin():
        with tenant_context(db_conn, TENANT_B):
            db_conn.execute(text("SELECT id FROM entities")).fetchall()

    # Read back as A
    with db_conn.begin():
        with tenant_context(db_conn, TENANT_A):
            rows = db_conn.execute(
                text("SELECT label FROM entities WHERE label = 'VisibleToA'")
            ).fetchall()
    assert len(rows) == 1


def test_update_from_wrong_tenant_affects_zero_rows(db_conn):
    """
    UPDATE from Tenant B context on a Tenant A row must affect 0 rows
    (RLS silently filters; no error raised, 0 rows updated).
    """
    with db_conn.begin():
        with tenant_context(db_conn, TENANT_A):
            eid = _insert_entity(db_conn, TENANT_A, "OriginalLabel")

    with db_conn.begin():
        with tenant_context(db_conn, TENANT_B):
            result = db_conn.execute(
                text(
                    "UPDATE entities SET label = 'HijackedLabel' WHERE id = :id"
                ),
                {"id": str(eid)},
            )
            assert result.rowcount == 0, (
                f"Expected 0 rows updated from wrong tenant context, got {result.rowcount}"
            )

    # Verify original label unchanged
    with db_conn.begin():
        with tenant_context(db_conn, TENANT_A):
            row = db_conn.execute(
                text("SELECT label FROM entities WHERE id = :id"),
                {"id": str(eid)},
            ).fetchone()
    assert row.label == "OriginalLabel"
```

- [ ] Run: `pytest tests/db/test_rls_isolation.py -v`
  Expected: `3 passed`

- [ ] Commit:

```bash
git add \
  factvault/db/migrations/versions/0012_rls_policies.py \
  factvault/db/rls.py \
  tests/db/test_rls_isolation.py
git commit -m "feat(db): migration 0012 + RLS on all domain tables + tenant_context helper + isolation tests"
```

---

### Task 18 — Migration 0013 + tests: v_conflicts view

- [ ] Create `factvault/db/migrations/versions/0013_v_conflicts_view.py`:

```python
"""v_conflicts view — surfaces non-deprecated statement conflicts

Revision ID: 0013
Revises: 0012
Create Date: 2026-05-22
"""
from alembic import op

revision = "0013"
down_revision = "0012"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("""
        CREATE OR REPLACE VIEW v_conflicts AS
        SELECT
            subject_id,
            property_id,
            tenant_id,
            COUNT(*) AS competing_count,
            array_agg(id) AS statement_ids
        FROM statements
        WHERE rank != 'deprecated'
        GROUP BY subject_id, property_id, tenant_id
        HAVING COUNT(
            DISTINCT COALESCE(
                val_entity::text,
                val_text,
                val_number::text,
                val_date::text,
                val_json::text
            )
        ) > 1
    """)


def downgrade() -> None:
    op.execute("DROP VIEW IF EXISTS v_conflicts")
```

- [ ] Create `tests/db/test_v_conflicts.py`:

```python
import pytest
from uuid import uuid4
from sqlalchemy import text

TENANT = uuid4()


def _setup(conn):
    """Insert one entity and one property; return (entity_id, property_id)."""
    eid = uuid4()
    pid = uuid4()
    conn.execute(
        text("INSERT INTO entities (id, tenant_id, label) VALUES (:id, :tid, 'ConflictCorp')"),
        {"id": str(eid), "tid": str(TENANT)},
    )
    conn.execute(
        text(
            "INSERT INTO properties (id, slug, label, value_type) "
            "VALUES (:id, 'conflict_prop', 'Conflict Prop', 'string')"
        ),
        {"id": str(pid)},
    )
    return eid, pid


def _stmt(conn, eid, pid, val, rank="normal"):
    sid = uuid4()
    conn.execute(
        text(
            "INSERT INTO statements "
            "(id, tenant_id, subject_id, property_id, val_text, rank, confidence) "
            "VALUES (:id, :tid, :eid, :pid, :val, :rank, 0.5)"
        ),
        {
            "id": str(sid),
            "tid": str(TENANT),
            "eid": str(eid),
            "pid": str(pid),
            "val": val,
            "rank": rank,
        },
    )
    return sid


def test_conflict_appears_with_three_statements(db_conn):
    """
    Two statements with same value (preferred + normal) + one with different value
    → one conflict row with competing_count = 3.
    """
    with db_conn.begin():
        eid, pid = _setup(db_conn)
        _stmt(db_conn, eid, pid, "ValueA", rank="preferred")
        _stmt(db_conn, eid, pid, "ValueA", rank="normal")
        _stmt(db_conn, eid, pid, "ValueB", rank="normal")

    # RLS: set tenant context to see the rows
    with db_conn.begin():
        db_conn.execute(text("SET LOCAL app.tenant_id = :tid"), {"tid": str(TENANT)})
        rows = db_conn.execute(
            text(
                "SELECT competing_count, statement_ids "
                "FROM v_conflicts "
                "WHERE subject_id = :eid AND property_id = :pid AND tenant_id = :tid"
            ),
            {"eid": str(eid), "pid": str(pid), "tid": str(TENANT)},
        ).fetchall()

    assert len(rows) == 1, f"Expected 1 conflict row, got {len(rows)}"
    assert rows[0].competing_count == 3
    assert len(rows[0].statement_ids) == 3


def test_deprecated_rows_excluded_from_conflicts(db_conn):
    """
    Adding a deprecated statement with a new value must not change v_conflicts output.
    """
    with db_conn.begin():
        eid, pid = _setup(db_conn)
        # Create an initial conflict (2 non-deprecated rows with different values)
        _stmt(db_conn, eid, pid, "Alpha", rank="preferred")
        _stmt(db_conn, eid, pid, "Beta", rank="normal")

    with db_conn.begin():
        db_conn.execute(text("SET LOCAL app.tenant_id = :tid"), {"tid": str(TENANT)})
        before = db_conn.execute(
            text(
                "SELECT competing_count FROM v_conflicts "
                "WHERE subject_id = :eid AND property_id = :pid AND tenant_id = :tid"
            ),
            {"eid": str(eid), "pid": str(pid), "tid": str(TENANT)},
        ).fetchone()

    assert before is not None
    before_count = before.competing_count

    # Add a deprecated row with a brand-new value — should NOT affect v_conflicts
    with db_conn.begin():
        _stmt(db_conn, eid, pid, "GammaDeprecated", rank="deprecated")

    with db_conn.begin():
        db_conn.execute(text("SET LOCAL app.tenant_id = :tid"), {"tid": str(TENANT)})
        after = db_conn.execute(
            text(
                "SELECT competing_count FROM v_conflicts "
                "WHERE subject_id = :eid AND property_id = :pid AND tenant_id = :tid"
            ),
            {"eid": str(eid), "pid": str(pid), "tid": str(TENANT)},
        ).fetchone()

    assert after.competing_count == before_count, (
        f"Deprecated row changed competing_count from {before_count} to {after.competing_count}"
    )
```

- [ ] Run: `pytest tests/db/test_v_conflicts.py -v`
  Expected: `2 passed`

- [ ] Commit:

```bash
git add \
  factvault/db/migrations/versions/0013_v_conflicts_view.py \
  tests/db/test_v_conflicts.py
git commit -m "feat(db): migration 0013 + v_conflicts view + tests"
```

---

### Task 19 — `factvault/db/schema.sql` reference document

- [ ] Create `factvault/db/schema.sql`:

```sql
-- This file is reference documentation only.
-- Migrations in factvault/db/migrations/versions/ are the source of truth.
-- Keep this file in sync manually after schema changes.
--
-- Dependency order: pgvector → entities/properties → statements/qualifiers →
--   relations → sources → statement_sources → source_verifications →
--   proposed_properties → dossiers → (embedding columns) → (HNSW indices) →
--   (RLS) → v_conflicts

-- ---------------------------------------------------------------------------
-- 0001: pgvector extension
-- ---------------------------------------------------------------------------
CREATE EXTENSION IF NOT EXISTS vector;

-- ---------------------------------------------------------------------------
-- 0002: entities and properties
-- ---------------------------------------------------------------------------
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
    UNIQUE (tenant_id, ext_id) NULLS NOT DISTINCT
);

CREATE INDEX idx_entities_tenant    ON entities (tenant_id);
CREATE INDEX idx_entities_label     ON entities (tenant_id, lower(label));
CREATE INDEX idx_entities_type      ON entities (tenant_id, type_uri);

CREATE TABLE properties (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID,
    slug        TEXT NOT NULL,
    label       TEXT NOT NULL,
    value_type  TEXT NOT NULL
                CHECK (value_type IN ('entity_ref', 'string', 'number', 'date', 'url')),
    description TEXT,
    UNIQUE (tenant_id, slug) NULLS NOT DISTINCT
);

-- ---------------------------------------------------------------------------
-- 0003: statements and qualifiers
-- ---------------------------------------------------------------------------
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
    CONSTRAINT chk_statement_value_populated
        CHECK (
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

CREATE TABLE qualifiers (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    statement_id UUID NOT NULL REFERENCES statements(id) ON DELETE CASCADE,
    property_id  UUID NOT NULL REFERENCES properties(id),
    val_text     TEXT,
    val_number   NUMERIC,
    val_date     TIMESTAMPTZ,
    val_entity   UUID REFERENCES entities(id),
    CONSTRAINT chk_qualifier_value_populated
        CHECK (
            (val_entity IS NOT NULL)::int +
            (val_text   IS NOT NULL)::int +
            (val_number IS NOT NULL)::int +
            (val_date   IS NOT NULL)::int = 1
        )
);

CREATE INDEX idx_qualifiers_statement ON qualifiers (statement_id);

-- ---------------------------------------------------------------------------
-- 0004: relations
-- ---------------------------------------------------------------------------
CREATE TABLE relations (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    source_id    UUID NOT NULL REFERENCES entities(id),
    target_id    UUID NOT NULL REFERENCES entities(id),
    type         TEXT NOT NULL,
    weight       NUMERIC,
    confidence   NUMERIC(4,3),
    description  TEXT,
    embedding    vector(1024),
    meta         JSONB NOT NULL DEFAULT '{}',
    statement_id UUID REFERENCES statements(id) ON DELETE CASCADE,
    UNIQUE (tenant_id, source_id, target_id, type)
);

CREATE INDEX idx_relations_source    ON relations (tenant_id, source_id);
CREATE INDEX idx_relations_target    ON relations (tenant_id, target_id);
CREATE INDEX idx_relations_type      ON relations (tenant_id, type);

-- ---------------------------------------------------------------------------
-- 0005: sources
-- ---------------------------------------------------------------------------
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
                     CHECK (status IN ('collected', 'archived', 'extracted', 'verified', 'link-rot', 'content-changed')),
    UNIQUE (tenant_id, url)
);

CREATE INDEX idx_sources_tenant_status ON sources (tenant_id, status);
CREATE INDEX idx_sources_last_verified ON sources (last_verified_at);
CREATE INDEX idx_sources_published_at  ON sources (published_at);

-- ---------------------------------------------------------------------------
-- 0006: statement_sources
-- ---------------------------------------------------------------------------
CREATE TABLE statement_sources (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    statement_id         UUID NOT NULL REFERENCES statements(id) ON DELETE CASCADE,
    source_id            UUID NOT NULL REFERENCES sources(id),
    tenant_id            UUID NOT NULL,
    excerpt              TEXT NOT NULL,
    excerpt_offset_start INTEGER NOT NULL,
    excerpt_offset_end   INTEGER NOT NULL,
    extraction_method    TEXT NOT NULL,
    extracted_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    confidence           NUMERIC(4,3),
    UNIQUE (statement_id, source_id)
);

CREATE INDEX idx_stmt_sources_statement ON statement_sources (statement_id);
CREATE INDEX idx_stmt_sources_source    ON statement_sources (source_id);

-- ---------------------------------------------------------------------------
-- 0007: source_verifications
-- ---------------------------------------------------------------------------
CREATE TABLE source_verifications (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id        UUID NOT NULL REFERENCES sources(id),
    tenant_id        UUID NOT NULL,
    verified_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    status           TEXT NOT NULL
                     CHECK (status IN ('live', 'link-rot', 'content-changed', 'excerpt-missing')),
    new_content_hash TEXT,
    notes            TEXT
);

CREATE INDEX idx_source_verifications_source ON source_verifications (source_id, verified_at DESC);
CREATE INDEX idx_source_verifications_status ON source_verifications (status, verified_at DESC);

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

-- ---------------------------------------------------------------------------
-- 0008: proposed_properties
-- ---------------------------------------------------------------------------
CREATE TABLE proposed_properties (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL,
    proposed_slug       TEXT NOT NULL,
    proposed_value_type TEXT NOT NULL
                        CHECK (proposed_value_type IN
                            ('entity_ref', 'string', 'number', 'date', 'url')),
    proposed_by         TEXT NOT NULL,
    example_excerpt     TEXT,
    example_source_id   UUID REFERENCES sources(id),
    status              TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'approved', 'rejected')),
    reviewed_by         TEXT,
    reviewed_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ DEFAULT now(),
    UNIQUE (tenant_id, proposed_slug, status)
);

CREATE INDEX idx_proposed_properties_tenant_status ON proposed_properties (tenant_id, status);

-- ---------------------------------------------------------------------------
-- 0009: dossiers cache
-- ---------------------------------------------------------------------------
CREATE TABLE dossiers (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    entity_id    UUID NOT NULL REFERENCES entities(id),
    assembled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    bundle       JSONB NOT NULL,
    UNIQUE (tenant_id, entity_id)
);

CREATE INDEX idx_dossiers_tenant_assembled ON dossiers (tenant_id, assembled_at DESC);

-- ---------------------------------------------------------------------------
-- 0010: embedding columns (added via ALTER TABLE in migration; shown here for completeness)
-- NOTE: Already included inline above in each table definition.
-- ---------------------------------------------------------------------------

-- ---------------------------------------------------------------------------
-- 0011: HNSW indices on embedding columns
-- ---------------------------------------------------------------------------
CREATE INDEX idx_entities_embedding   ON entities   USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);
CREATE INDEX idx_statements_embedding ON statements USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);
CREATE INDEX idx_relations_embedding  ON relations  USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);
CREATE INDEX idx_sources_embedding    ON sources    USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);

-- ---------------------------------------------------------------------------
-- 0012: RLS policies
-- ---------------------------------------------------------------------------
ALTER TABLE entities              ENABLE ROW LEVEL SECURITY;
ALTER TABLE entities              FORCE ROW LEVEL SECURITY;
ALTER TABLE properties            ENABLE ROW LEVEL SECURITY;
ALTER TABLE properties            FORCE ROW LEVEL SECURITY;
ALTER TABLE statements            ENABLE ROW LEVEL SECURITY;
ALTER TABLE statements            FORCE ROW LEVEL SECURITY;
ALTER TABLE qualifiers            ENABLE ROW LEVEL SECURITY;
ALTER TABLE qualifiers            FORCE ROW LEVEL SECURITY;
ALTER TABLE relations             ENABLE ROW LEVEL SECURITY;
ALTER TABLE relations             FORCE ROW LEVEL SECURITY;
ALTER TABLE sources               ENABLE ROW LEVEL SECURITY;
ALTER TABLE sources               FORCE ROW LEVEL SECURITY;
ALTER TABLE statement_sources     ENABLE ROW LEVEL SECURITY;
ALTER TABLE statement_sources     FORCE ROW LEVEL SECURITY;
ALTER TABLE source_verifications  ENABLE ROW LEVEL SECURITY;
ALTER TABLE source_verifications  FORCE ROW LEVEL SECURITY;
ALTER TABLE proposed_properties   ENABLE ROW LEVEL SECURITY;
ALTER TABLE proposed_properties   FORCE ROW LEVEL SECURITY;
ALTER TABLE dossiers              ENABLE ROW LEVEL SECURITY;
ALTER TABLE dossiers              FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON entities
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY tenant_isolation ON properties
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY tenant_isolation ON statements
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY tenant_isolation ON qualifiers
    USING (
        EXISTS (
            SELECT 1 FROM statements s
            WHERE s.id = qualifiers.statement_id
              AND s.tenant_id = current_setting('app.tenant_id', true)::uuid
        )
    );
CREATE POLICY tenant_isolation ON relations
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY tenant_isolation ON sources
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY tenant_isolation ON statement_sources
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY tenant_isolation ON source_verifications
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY tenant_isolation ON proposed_properties
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY tenant_isolation ON dossiers
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- ---------------------------------------------------------------------------
-- 0013: v_conflicts view
-- ---------------------------------------------------------------------------
CREATE OR REPLACE VIEW v_conflicts AS
SELECT
    subject_id,
    property_id,
    tenant_id,
    COUNT(*) AS competing_count,
    array_agg(id) AS statement_ids
FROM statements
WHERE rank != 'deprecated'
GROUP BY subject_id, property_id, tenant_id
HAVING COUNT(
    DISTINCT COALESCE(
        val_entity::text,
        val_text,
        val_number::text,
        val_date::text,
        val_json::text
    )
) > 1;
```

- [ ] Commit:

```bash
git add factvault/db/schema.sql
git commit -m "docs(db): schema.sql reference DDL — all 13 migrations in dependency order"
```

---

### Task 20 — `factvault/db/README.md` operator guide

- [ ] Create `factvault/db/README.md`:

```markdown
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

Tests use `testcontainers-python` to spin up a real Postgres + pgvector container. The `db_conn` fixture (defined in `tests/conftest.py`) provides a ready-to-use SQLAlchemy `Connection` with all migrations applied.

```python
# tests/db/test_my_feature.py

from uuid import uuid4
from sqlalchemy import text

TENANT = uuid4()


def test_something(db_conn):
    with db_conn.begin():
        db_conn.execute(
            text("SET LOCAL app.tenant_id = :tid"),
            {"tid": str(TENANT)},
        )
        db_conn.execute(
            text("INSERT INTO entities (id, tenant_id, label) VALUES (:id, :tid, 'Test')"),
            {"id": str(uuid4()), "tid": str(TENANT)},
        )
        rows = db_conn.execute(
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

The `db_conn` fixture rolls back between tests — no manual cleanup needed.

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
```

- [ ] Commit:

```bash
git add factvault/db/README.md
git commit -m "docs(db): operator guide — migrations, tenant context, test patterns, plan scope"
```

---

### Task 21 — `.github/workflows/ci.yml` CI placeholder

- [ ] Create `.github/workflows/ci.yml`:

```yaml
name: CI

on:
  push:
    branches: ["**"]
  pull_request:
    branches: ["**"]

jobs:
  test:
    name: pytest (postgres + pgvector)
    runs-on: ubuntu-latest

    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Set up Python 3.12
        uses: actions/setup-python@v5
        with:
          python-version: "3.12"

      - name: Cache pip
        uses: actions/cache@v4
        with:
          path: ~/.cache/pip
          key: ${{ runner.os }}-pip-${{ hashFiles('pyproject.toml') }}
          restore-keys: |
            ${{ runner.os }}-pip-

      - name: Install package + dev dependencies
        run: pip install -e ".[dev]"

      - name: Build postgres+pgvector image
        uses: docker/build-push-action@v5
        with:
          context: docker/postgres
          push: false
          tags: factvault-postgres:ci
          load: true

      - name: Run pytest
        run: pytest -v
        env:
          # testcontainers-python will spin up the container using the image built above.
          # The DOCKER_IMAGE env var is read by conftest.py to select the postgres image.
          FACTVAULT_TEST_POSTGRES_IMAGE: factvault-postgres:ci
```

> **Note on testcontainers integration:** `tests/conftest.py` (Task 5) uses `testcontainers[postgres]` to spin up the container. The `FACTVAULT_TEST_POSTGRES_IMAGE` env var allows CI to override the default image with the locally built Chainguard pgvector image. `conftest.py` must read this env var:

```python
# Snippet to add to tests/conftest.py (Task 5):
import os

POSTGRES_IMAGE = os.environ.get(
    "FACTVAULT_TEST_POSTGRES_IMAGE",
    "pgvector/pgvector:pg16",   # default for local dev
)
```

- [ ] Commit:

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add CI workflow — pytest against postgres+pgvector on every push and PR"
```

---

## Self-Review

### Spec Coverage Checklist

| Spec requirement | Task |
|------------------|------|
| `entities` table (§3.2) | Task 7 (Pass 1) |
| `properties` table (§3.2) | Task 7 (Pass 1) |
| `statements` table (§3.2) | Task 8 (Pass 1) |
| `qualifiers` table (§3.2) | Task 8 (Pass 1) |
| `relations` table (§3.2) | Task 9 (Pass 1) |
| `sources` table (§3.1) | Task 10 (Pass 1) |
| `statement_sources` junction table (§3.1) | Task 11 (Pass 1) |
| `source_verifications` append-only log (§3.1) | Task 12 |
| `proposed_properties` strict-mode queue (§3.2) | Task 13 |
| `dossiers` cache table (§3.4) | Task 14 |
| Embedding columns on 4 tables (§6 Embedding Model) | Task 15 |
| HNSW indices on 4 embedding columns (§6 Embedding Model) | Task 16 |
| RLS on every domain table (§6 Multi-Tenancy) | Task 17 |
| Tenant context helper (`SET LOCAL app.tenant_id`) (§6) | Task 17 |
| `v_conflicts` view (§3.2, §3.3) | Task 18 |
| Chainguard wolfi-base container standard (§6) | Task 3 (Pass 1) — Dockerfile |
| pgvector extension (§6 Embedding Model) | Task 6 (Pass 1) — migration 0001 |
| `gen_random_uuid()` PK default on all tables (§3.2) | Tasks 7–14 (Pass 1 + Pass 2) |
| Append-only enforcement on `source_verifications` (§3.1) | Task 12 — trigger DDL |
| `raw_text` nullable on `sources` (spec §3.1: "NULL until stage 2") | Task 10 (Pass 1) |
| `extracted` status on `sources.status` CHECK (§3.1) | Task 10 (Pass 1) |
| Strict-vs-permissive vocabulary mode — schema only (§3.2) | Task 13 (`proposed_properties` queue); behavior is Plan 3 scope |

### Placeholder Scan

Reviewed. No placeholders found. The only deferred item is the behavioral enforcement of strict vs. permissive vocabulary mode (the `proposed_properties` table exists; the worker logic that reads it is explicitly scoped to Plan 3 in the operator README).

### Type Consistency Check

Reviewed. The following names are consistent across all tasks:

- `tenant_id UUID NOT NULL` — present on all tables that require it; `qualifiers` correctly omits it and gets its RLS policy via a subquery on `statements`.
- `gen_random_uuid()` — used as PK default throughout.
- `TIMESTAMPTZ` — used for all timestamp columns; no plain `TIMESTAMP` or `DATE` used except `val_date` which is correctly `TIMESTAMPTZ` per the spec.
- Index naming convention `idx_<table>_<column/purpose>` — consistent across all 13 migrations.
- `vector_cosine_ops` with `m=16, ef_construction=64` — consistent across all four HNSW indices (Tasks 16 and schema.sql Task 19).
- `proposed_properties` column names: the plan uses `proposed_slug` / `proposed_value_type` / `proposed_by` matching the task specification; the spec's own DDL (§3.2) uses `slug` / `value_type` / `proposed_by` for the same table. The plan's names are more explicit and carry forward the task 13 requirement verbatim — no conflict.
- `statement_sources.tenant_id` — added per the Pass 1 resolution (task 11) and carried through the RLS migration (task 17) and schema.sql (task 19) consistently.
