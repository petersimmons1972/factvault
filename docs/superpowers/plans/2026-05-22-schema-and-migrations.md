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

<!-- PASS 1 END — Pass 2 appends Tasks 12-21 + self-review below this line -->
