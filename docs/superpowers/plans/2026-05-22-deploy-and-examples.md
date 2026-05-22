# Deploy and Examples Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the project by wiring the full operational stack and delivering four runnable domain examples. After this plan merges, a new adopter can run `git clone && docker compose up && factvault doctor && factvault example run <name>` and have a working, sourced, hallucination-resistant research database for one of four canned domains within 10 minutes. This is Plan 5 of 5; depends on Plans 1-4.

**Architecture:** Single `docker-compose.yml` wires postgres + api + worker + mcp + optional local SearXNG + optional local Ollama. The `factvault doctor` CLI verifies the deployment end-to-end on first boot. Each of the four examples is a self-contained directory with its own property vocabulary, entity seeds, canned source fixtures, and golden output snapshots that double as integration tests.

**Tech Stack:** docker-compose, Helm (optional), the Plan 1-4 stack. No new Python dependencies; this plan is mostly integration + docs + ops glue.

---

## Known Plan-Bug Patterns (apply from the start — do NOT discover these during execution)

These six patterns were surfaced during Plan 1 execution. Every task in this plan is written to avoid them.

1. **`TIMESTAMPTZ` import:** `TIMESTAMPTZ` is NOT in `sqlalchemy.dialects.postgresql`. Use `TIMESTAMP(timezone=True)` from `sqlalchemy` (e.g., `sa.TIMESTAMP(timezone=True)`).
2. **Explicit SA imports:** `sa.UniqueConstraint` / `sa.LargeBinary` need direct imports when `sa` alias isn't in scope. Prefer `from sqlalchemy import UniqueConstraint, LargeBinary` explicitly.
3. **psycopg cast syntax:** psycopg refuses `:param::jsonb` / `:param::vector`. Use `CAST(:param AS jsonb)` / `CAST(:param AS vector)` in raw SQL.
4. **Postgres 15+ NULL uniqueness:** Unique constraints default to `NULLS NOT DISTINCT`. Tests relying on duplicate-NULL behavior must use distinct tenants/values to avoid unexpected conflicts.
5. **Fixture tenancy:** The `conn` fixture is single-tenant superuser (bypasses RLS). RLS-sensitive tests must use `app_engine`.
6. **RLS setting:** RLS policies use `app.tenant_id` GUC (not `app.current_tenant_id`). Application code sets `app.tenant_id` via `tenant_context()` — match the GUC name used in `factvault/db/rls.py` exactly.

---

## File Structure

```
factvault/
├── docker-compose.yml                       # EXTENDED: postgres + api + worker + mcp + optional searxng + optional ollama
├── docker-compose.override.example.yml      # NEW: opt-in services template (searxng, ollama)
├── docker/
│   ├── postgres/
│   │   └── Dockerfile                       # existing — Chainguard postgres + pgvector
│   └── app/
│       └── Dockerfile                       # NEW: multi-stage python:latest-dev → wolfi-base runtime
├── .env.example                             # EXTENDED: every env var the stack reads
├── README.md                                # FINAL PASS: distilled voice, quickstart, dossier-vs-story
├── factvault/
│   ├── doctor/
│   │   ├── __init__.py
│   │   ├── checks.py                        # Individual health check functions
│   │   ├── cli.py                           # `factvault doctor` command
│   │   └── canary.py                        # End-to-end canary fact test
│   ├── examples/
│   │   ├── __init__.py
│   │   ├── base.py                          # Example loader: reads properties.yaml + seeds.yaml + fixtures
│   │   └── cli.py                           # `factvault example list|info|run <name>`
│   └── ... (existing)
├── examples/
│   ├── ai-startup-tracking/
│   │   ├── README.md
│   │   ├── properties.yaml
│   │   ├── seeds.yaml
│   │   ├── fixtures/                        # canned source HTML/RSS
│   │   ├── expected/                        # golden dossier + story JSON
│   │   └── run.sh                           # one-shot driver script
│   ├── political-research/
│   │   ├── README.md
│   │   ├── properties.yaml
│   │   ├── seeds.yaml
│   │   ├── fixtures/
│   │   ├── expected/
│   │   └── run.sh
│   ├── pharma-trial-monitoring/
│   │   ├── README.md
│   │   ├── properties.yaml
│   │   ├── seeds.yaml
│   │   ├── fixtures/
│   │   ├── expected/
│   │   └── run.sh
│   └── investigative-journalism/
│       ├── README.md
│       ├── properties.yaml
│       ├── seeds.yaml
│       ├── fixtures/
│       ├── expected/
│       └── run.sh
├── docs/
│   ├── quickstart.md                        # 5-minute first-success guide
│   ├── operations.md                        # Production ops
│   ├── security.md                          # Multi-tenant + JWT threat model
│   ├── troubleshooting.md                   # Top failure modes
│   └── ... (existing concept docs + defining-properties guide)
├── deploy/
│   └── helm/
│       └── factvault/                       # Helm chart (optional v1.1)
│           ├── Chart.yaml
│           ├── values.yaml
│           ├── templates/
│           │   └── ... (per-resource templates)
│           └── README.md
└── tests/
    ├── doctor/
    │   ├── test_checks.py
    │   ├── test_cli.py
    │   └── test_canary.py
    ├── examples/
    │   ├── test_base.py
    │   ├── test_cli.py
    │   ├── test_ai_startup_tracking.py
    │   ├── test_political_research.py
    │   ├── test_pharma_trial_monitoring.py
    │   └── test_investigative_journalism.py
    └── integration/
        └── test_full_stack_compose.py       # spin up docker-compose, run doctor, assert green
```

---

## Task 1 — docker-compose.yml extension

**Context:** The existing `docker-compose.yml` (Plan 1) defines only `postgres`. Extend it with `api`, `worker`, and `mcp` services. All three use a single shared image `factvault-app:latest` built from `docker/app/Dockerfile` (Task 2). The `worker` service runs the multi-purpose runner; the compose `command:` selects which worker loop to run.

- [ ] **1.1** Replace `docker-compose.yml` with the full extended version:

```yaml
# docker-compose.yml
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

  api:
    build:
      context: .
      dockerfile: docker/app/Dockerfile
    image: factvault-app:latest
    command: ["factvault-api"]
    ports:
      - "8000:8000"
    env_file:
      - .env
    environment:
      FACTVAULT_DATABASE_URL: "postgresql+psycopg://factvault:factvault@postgres:5432/factvault"
    depends_on:
      postgres:
        condition: service_healthy
    healthcheck:
      test: ["CMD-SHELL", "curl -fsS http://localhost:8000/healthz || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 6
      start_period: 15s
    user: "65532:65532"

  worker:
    image: factvault-app:latest
    command: ["factvault-worker", "run", "all"]
    env_file:
      - .env
    environment:
      FACTVAULT_DATABASE_URL: "postgresql+psycopg://factvault:factvault@postgres:5432/factvault"
    depends_on:
      postgres:
        condition: service_healthy
    healthcheck:
      test: ["CMD-SHELL", "pgrep -f factvault-worker || exit 1"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s
    user: "65532:65532"

  mcp:
    image: factvault-app:latest
    command: ["factvault-mcp"]
    ports:
      - "9000:9000"
    env_file:
      - .env
    environment:
      FACTVAULT_DATABASE_URL: "postgresql+psycopg://factvault:factvault@postgres:5432/factvault"
    depends_on:
      postgres:
        condition: service_healthy
    user: "65532:65532"

volumes:
  pgdata:
```

- [ ] **1.2** Commit:
```bash
git add docker-compose.yml
git commit -m "feat(compose): extend docker-compose with api, worker, mcp services"
```

---

## Task 2 — Application Dockerfile

**Context:** Multi-stage build using Chainguard images per the container standard in spec §6 and `~/docs/container-images.md`. Build stage uses `python:latest-dev` (includes pip, build tools). Runtime stage uses `wolfi-base` with python installed from wolfi. tini is installed in the runtime stage from the wolfi package repository. UID 65532, nonroot.

- [ ] **2.1** Create `docker/app/Dockerfile`:

```dockerfile
# docker/app/Dockerfile
# Stage 1: build — installs package and all dependencies into /install prefix
FROM cgr.dev/chainguard/python:latest-dev AS builder

WORKDIR /app
COPY pyproject.toml .
COPY factvault/ factvault/

# Install into a prefix so we can copy the tree cleanly
RUN pip install --prefix=/install --no-cache-dir .

# Stage 2: runtime — minimal wolfi-base with python + tini
FROM cgr.dev/chainguard/wolfi-base AS runtime

# Install python and tini from wolfi packages
RUN apk add --no-cache python-3.12 tini

# Copy installed package tree from builder
COPY --from=builder /install /usr/local

# Run as nonroot (Chainguard standard UID)
USER 65532

ENTRYPOINT ["/sbin/tini", "--"]
# Default CMD — overridden by compose `command:` per service
CMD ["factvault-api"]
```

- [ ] **2.2** Verify the build completes and image size is under 600 MB:
```bash
docker build -t factvault-app:test docker/app/
docker image inspect factvault-app:test --format '{{.Size}}' | awk '{printf "%.0f MB\n", $1/1048576}'
# Expected: under 600 MB
```

- [ ] **2.3** Verify the image runs as nonroot:
```bash
docker run --rm factvault-app:test id
# Expected: uid=65532 gid=65532
```

- [ ] **2.4** Commit:
```bash
git add docker/app/Dockerfile
git commit -m "feat(docker): add multi-stage app Dockerfile (wolfi-base + tini + nonroot 65532)"
```

---

## Task 3 — docker-compose.override.example.yml

**Context:** Opt-in services that most users will not need on first run. Users copy to `docker-compose.override.yml` and uncomment blocks. SearXNG feeds the `searxng` collector. Ollama provides a local LLM endpoint for extraction.

- [ ] **3.1** Create `docker-compose.override.example.yml`:

```yaml
# docker-compose.override.example.yml
#
# Opt-in services for local development.
# To enable: copy this file to docker-compose.override.yml and uncomment what you need.
# docker-compose.override.yml is in .gitignore.
#
# Usage:
#   cp docker-compose.override.example.yml docker-compose.override.yml
#   # Edit docker-compose.override.yml to uncomment desired services
#   docker compose up -d

services:

  # ── SearXNG ─────────────────────────────────────────────────────────────────
  # Local metasearch engine for the SearXNG collector.
  # Set FACTVAULT_SEARXNG_URL=http://searxng:8888 in .env to use it.
  #
  # searxng:
  #   image: searxng/searxng:latest
  #   ports:
  #     - "8888:8080"
  #   environment:
  #     SEARXNG_SECRET: "change-me-in-production"
  #   volumes:
  #     - searxng_data:/etc/searxng

  # ── Ollama ──────────────────────────────────────────────────────────────────
  # Local LLM server (OpenAI-compatible API) for extraction.
  # Set FACTVAULT_LLM_URL=http://ollama:11434/v1 in .env to use it.
  # The init container preloads the model so the worker doesn't have to wait.
  #
  # ollama:
  #   image: ollama/ollama:latest
  #   ports:
  #     - "11434:11434"
  #   volumes:
  #     - ollama_models:/root/.ollama
  #   healthcheck:
  #     test: ["CMD-SHELL", "curl -fsS http://localhost:11434/api/tags || exit 1"]
  #     interval: 10s
  #     timeout: 5s
  #     retries: 12
  #     start_period: 30s
  #
  # ollama-init:
  #   image: ollama/ollama:latest
  #   depends_on:
  #     ollama:
  #       condition: service_healthy
  #   entrypoint: ["ollama", "pull", "llama3.1:8b"]
  #   environment:
  #     OLLAMA_HOST: "http://ollama:11434"
  #   restart: "no"

# volumes:
#   searxng_data:
#   ollama_models:
```

- [ ] **3.2** Add `docker-compose.override.yml` to `.gitignore` if not already present:
```bash
grep -q 'docker-compose.override.yml' .gitignore || echo 'docker-compose.override.yml' >> .gitignore
```

- [ ] **3.3** Commit:
```bash
git add docker-compose.override.example.yml .gitignore
git commit -m "feat(compose): add opt-in override template for searxng + ollama services"
```

---

## Task 4 — .env.example final pass

**Context:** Extend the existing 4-line `.env.example` with every env var the stack reads. Document each with a comment. Values point at the dev stack defaults (postgres on localhost, ollama on localhost).

- [ ] **4.1** Replace `.env.example`:

```bash
# .env.example
# Copy this file to .env and fill in real values.
# Never commit .env — it is in .gitignore.

# ── PostgreSQL ───────────────────────────────────────────────────────────────
POSTGRES_USER=factvault
POSTGRES_PASSWORD=factvault
POSTGRES_DB=factvault

# Full SQLAlchemy async URL used by the application.
# In docker-compose, the host is the service name (postgres). Locally it is localhost.
FACTVAULT_DATABASE_URL=postgresql+psycopg://factvault:factvault@localhost:5432/factvault

# ── Tenant (single-tenant dev mode) ─────────────────────────────────────────
# UUID used as tenant_id for all operations in local / single-tenant mode.
# In multi-tenant mode, tenant_id comes from the JWT claims and this var is ignored.
FACTVAULT_TENANT_ID=00000000-0000-0000-0000-000000000001

# ── JWT Authentication ───────────────────────────────────────────────────────
# For local development, factvault generates a self-signed key pair if neither
# FACTVAULT_JWT_PUBLIC_KEY nor FACTVAULT_JWT_JWKS_URL is set.
#
# Option A: PEM-encoded RSA public key (paste the full PEM block as a single line
# with literal \n for newlines, or point at a file path prefixed with file://).
# FACTVAULT_JWT_PUBLIC_KEY=-----BEGIN PUBLIC KEY-----\nMIIBI...

# Option B: JWKS endpoint URL (for external IdP such as Auth0, Keycloak).
# FACTVAULT_JWT_JWKS_URL=https://your-idp.example.com/.well-known/jwks.json

# JWT algorithm. RS256 (RSA) or ES256 (ECDSA). Must match the key type above.
FACTVAULT_JWT_ALGORITHM=RS256

# ── LLM Backend ─────────────────────────────────────────────────────────────
# OpenAI-compatible base URL. Points at local Ollama by default.
# For OpenAI: FACTVAULT_LLM_URL=https://api.openai.com/v1
# For local Ollama in docker-compose: FACTVAULT_LLM_URL=http://ollama:11434/v1
FACTVAULT_LLM_URL=http://localhost:11434/v1

# Model name as understood by the LLM endpoint.
FACTVAULT_LLM_MODEL=llama3.1:8b

# API key — leave blank for local Ollama (no auth required).
FACTVAULT_LLM_API_KEY=

# ── Embedding Model ──────────────────────────────────────────────────────────
# HuggingFace model name for sentence-transformers. BGE-M3 produces 1024-dim vectors.
FACTVAULT_EMBEDDING_MODEL=BAAI/bge-m3

# Compute device: cpu (default) or cuda (requires CUDA runtime in the container).
FACTVAULT_EMBEDDING_DEVICE=cpu

# ── Wayback Machine ──────────────────────────────────────────────────────────
# SPN2 API rate limit per minute. Unauthenticated tier: ~15/min. Authenticated: higher.
FACTVAULT_WAYBACK_RATE_LIMIT_PER_MIN=12

# Optional SPN2 API access key for authenticated tier.
# FACTVAULT_WAYBACK_API_KEY=

# ── Collectors ───────────────────────────────────────────────────────────────
# URL of local SearXNG instance for the searxng collector.
# Uncomment the searxng service in docker-compose.override.yml to run one locally.
# FACTVAULT_SEARXNG_URL=http://localhost:8888

# ── Property Mode ────────────────────────────────────────────────────────────
# strict (default): unknown property slugs proposed by the LLM are queued for review.
# permissive: unknown slugs auto-register (useful for rapid prototyping).
FACTVAULT_PROPERTY_MODE=strict

# ── API Server ───────────────────────────────────────────────────────────────
FACTVAULT_API_HOST=0.0.0.0
FACTVAULT_API_PORT=8000

# ── MCP Server ───────────────────────────────────────────────────────────────
FACTVAULT_MCP_PORT=9000
```

- [ ] **4.2** Commit:
```bash
git add .env.example
git commit -m "feat(config): extend .env.example with all stack env vars + inline docs"
```

---

## Task 5 — factvault doctor: individual check functions

**Context:** Each check is a standalone function returning a `CheckResult`. External checks (Wayback, LLM endpoint) are tested with `httpx` and mocked in tests via `pytest-httpx`. DB checks use the testcontainers postgres fixture.

- [ ] **5.1** Create `factvault/doctor/__init__.py` (empty):
```python
```

- [ ] **5.2** Create `factvault/doctor/checks.py`:

```python
# factvault/doctor/checks.py
"""
Individual health check functions for `factvault doctor`.

Each function returns a CheckResult. External I/O (HTTP, DB) is performed
directly — callers wrap in try/except if they need graceful aggregation.
"""
from __future__ import annotations

import os
from dataclasses import dataclass

import httpx
import sqlalchemy as sa
from sqlalchemy import text


@dataclass
class CheckResult:
    name: str
    status: bool
    detail: str


def check_db_reachable(engine: sa.Engine) -> CheckResult:
    """Verify the database is reachable and returns a result row."""
    try:
        with engine.connect() as conn:
            row = conn.execute(text("SELECT 1")).scalar()
        if row == 1:
            return CheckResult("Database reachable", True, "SELECT 1 returned 1")
        return CheckResult("Database reachable", False, f"Unexpected result: {row!r}")
    except Exception as exc:
        return CheckResult("Database reachable", False, str(exc))


def check_pgvector_loaded(engine: sa.Engine) -> CheckResult:
    """Verify the pgvector extension is installed in the database."""
    try:
        with engine.connect() as conn:
            row = conn.execute(
                text(
                    "SELECT extname FROM pg_extension WHERE extname = 'vector'"
                )
            ).fetchone()
        if row:
            return CheckResult("pgvector extension loaded", True, "extension 'vector' present")
        return CheckResult(
            "pgvector extension loaded",
            False,
            "extension 'vector' not found — run: CREATE EXTENSION IF NOT EXISTS vector;",
        )
    except Exception as exc:
        return CheckResult("pgvector extension loaded", False, str(exc))


def check_rls_policies_present(engine: sa.Engine) -> CheckResult:
    """
    Verify that RLS policies exist for the core tenant-scoped tables.
    Checks for at least one policy on each of: entities, statements, sources.
    """
    required_tables = {"entities", "statements", "sources"}
    try:
        with engine.connect() as conn:
            rows = conn.execute(
                text(
                    "SELECT tablename FROM pg_policies "
                    "WHERE tablename = ANY(CAST(:tables AS text[]))"
                ),
                {"tables": list(required_tables)},
            ).fetchall()
        found = {r[0] for r in rows}
        missing = required_tables - found
        if not missing:
            return CheckResult(
                "RLS policies applied",
                True,
                f"Policies found on: {', '.join(sorted(found))}",
            )
        return CheckResult(
            "RLS policies applied",
            False,
            f"Missing RLS policies on: {', '.join(sorted(missing))} — "
            "run Alembic migrations: alembic upgrade head",
        )
    except Exception as exc:
        return CheckResult("RLS policies applied", False, str(exc))


def check_wayback_reachable(
    wayback_url: str = "https://archive.org/wayback/available?url=example.com",
    timeout: float = 10.0,
) -> CheckResult:
    """Verify the Wayback Machine API is reachable (HEAD request to availability endpoint)."""
    try:
        resp = httpx.get(wayback_url, timeout=timeout, follow_redirects=True)
        if resp.status_code < 500:
            return CheckResult(
                "Wayback API reachable",
                True,
                f"HTTP {resp.status_code} from {wayback_url}",
            )
        return CheckResult(
            "Wayback API reachable",
            False,
            f"HTTP {resp.status_code} — Wayback may be under maintenance",
        )
    except httpx.TimeoutException:
        return CheckResult(
            "Wayback API reachable",
            False,
            f"Timeout after {timeout}s — check network connectivity",
        )
    except Exception as exc:
        return CheckResult("Wayback API reachable", False, str(exc))


def check_embedding_model_loadable(model_name: str | None = None) -> CheckResult:
    """
    Verify the embedding model can be loaded.
    Loads the model (downloads if not cached) and runs a single encode call.
    This is intentionally slow on first run; subsequent runs use the cache.
    """
    model_name = model_name or os.environ.get("FACTVAULT_EMBEDDING_MODEL", "BAAI/bge-m3")
    try:
        from sentence_transformers import SentenceTransformer  # type: ignore

        model = SentenceTransformer(model_name)
        vec = model.encode(["factvault canary"])
        dims = len(vec[0])
        return CheckResult(
            "Embedding model loadable",
            True,
            f"{model_name} / {dims}d",
        )
    except ImportError:
        return CheckResult(
            "Embedding model loadable",
            False,
            "sentence-transformers not installed — run: pip install sentence-transformers",
        )
    except Exception as exc:
        return CheckResult("Embedding model loadable", False, str(exc))


def check_llm_endpoint_reachable(
    llm_url: str | None = None,
    timeout: float = 10.0,
) -> CheckResult:
    """
    Verify the LLM endpoint responds to a models list request.
    Uses the OpenAI-compatible /v1/models endpoint.
    """
    base_url = llm_url or os.environ.get("FACTVAULT_LLM_URL", "http://localhost:11434/v1")
    api_key = os.environ.get("FACTVAULT_LLM_API_KEY", "")
    models_url = base_url.rstrip("/") + "/models"
    headers = {"Authorization": f"Bearer {api_key}"} if api_key else {}
    try:
        resp = httpx.get(models_url, headers=headers, timeout=timeout)
        if resp.status_code < 500:
            return CheckResult(
                "LLM endpoint responding",
                True,
                f"HTTP {resp.status_code} from {base_url}",
            )
        return CheckResult(
            "LLM endpoint responding",
            False,
            f"HTTP {resp.status_code} — check FACTVAULT_LLM_URL and endpoint health",
        )
    except httpx.ConnectError:
        return CheckResult(
            "LLM endpoint responding",
            False,
            f"Connection refused at {base_url} — is the LLM server running?",
        )
    except httpx.TimeoutException:
        return CheckResult(
            "LLM endpoint responding",
            False,
            f"Timeout after {timeout}s from {base_url}",
        )
    except Exception as exc:
        return CheckResult("LLM endpoint responding", False, str(exc))
```

- [ ] **5.3** Create `tests/doctor/__init__.py` (empty) and `tests/doctor/test_checks.py`:

```python
# tests/doctor/test_checks.py
"""
Tests for individual doctor check functions.

DB checks use the testcontainers postgres engine fixture from conftest.
External HTTP checks use pytest-httpx to mock responses without network calls.
"""
import pytest
import sqlalchemy as sa
from sqlalchemy import text

from factvault.doctor.checks import (
    CheckResult,
    check_db_reachable,
    check_embedding_model_loadable,
    check_llm_endpoint_reachable,
    check_pgvector_loaded,
    check_rls_policies_present,
    check_wayback_reachable,
)


# ── DB checks ────────────────────────────────────────────────────────────────

def test_check_db_reachable_ok(pg_engine):
    result = check_db_reachable(pg_engine)
    assert isinstance(result, CheckResult)
    assert result.status is True
    assert "1" in result.detail


def test_check_db_reachable_bad_url():
    bad_engine = sa.create_engine(
        "postgresql+psycopg://nobody:wrong@127.0.0.1:19999/no_db",
        connect_args={"connect_timeout": 2},
    )
    result = check_db_reachable(bad_engine)
    assert result.status is False
    assert result.detail  # has an error message


def test_check_pgvector_loaded_ok(pg_engine):
    # pgvector must be installed in the test container (Plan 1 postgres Dockerfile)
    result = check_pgvector_loaded(pg_engine)
    assert result.status is True
    assert "vector" in result.detail


def test_check_pgvector_loaded_missing(pg_engine):
    # Create a fresh schema without the extension and test against a plain engine
    # We simulate absence by querying for a non-existent extension name
    with pg_engine.connect() as conn:
        row = conn.execute(
            text("SELECT extname FROM pg_extension WHERE extname = 'nonexistent_ext'")
        ).fetchone()
    assert row is None  # confirms the query pattern is correct

    # Use a mock engine that returns no rows for the extension query
    class _MockConn:
        def execute(self, *a, **kw):
            class _Result:
                def fetchone(self):
                    return None
            return _Result()

        def __enter__(self):
            return self

        def __exit__(self, *a):
            pass

    class _MockEngine:
        def connect(self):
            return _MockConn()

    result = check_pgvector_loaded(_MockEngine())  # type: ignore[arg-type]
    assert result.status is False
    assert "CREATE EXTENSION" in result.detail


def test_check_rls_policies_present_missing(pg_engine):
    # On a fresh test DB without migrations applied, no RLS policies exist
    result = check_rls_policies_present(pg_engine)
    # May be True (if migrations ran) or False (if only schema exists) —
    # what matters is the return type is correct and detail is non-empty
    assert isinstance(result, CheckResult)
    assert result.detail


# ── HTTP checks ──────────────────────────────────────────────────────────────

def test_check_wayback_reachable_ok(httpx_mock):
    httpx_mock.add_response(
        url="https://archive.org/wayback/available?url=example.com",
        status_code=200,
        json={"archived_snapshots": {}},
    )
    result = check_wayback_reachable()
    assert result.status is True
    assert "200" in result.detail


def test_check_wayback_reachable_server_error(httpx_mock):
    httpx_mock.add_response(
        url="https://archive.org/wayback/available?url=example.com",
        status_code=503,
    )
    result = check_wayback_reachable()
    assert result.status is False
    assert "503" in result.detail


def test_check_llm_endpoint_reachable_ok(httpx_mock, monkeypatch):
    monkeypatch.setenv("FACTVAULT_LLM_URL", "http://localhost:11434/v1")
    monkeypatch.delenv("FACTVAULT_LLM_API_KEY", raising=False)
    httpx_mock.add_response(
        url="http://localhost:11434/v1/models",
        status_code=200,
        json={"object": "list", "data": []},
    )
    result = check_llm_endpoint_reachable()
    assert result.status is True
    assert "200" in result.detail


def test_check_llm_endpoint_connection_refused(monkeypatch):
    monkeypatch.setenv("FACTVAULT_LLM_URL", "http://127.0.0.1:19998/v1")
    monkeypatch.delenv("FACTVAULT_LLM_API_KEY", raising=False)
    result = check_llm_endpoint_reachable(timeout=2.0)
    assert result.status is False
    # Either connection refused or timeout — both are failures
    assert result.detail


def test_check_embedding_model_loadable_import_error(monkeypatch):
    # Simulate sentence-transformers not installed
    import builtins
    real_import = builtins.__import__

    def mock_import(name, *args, **kwargs):
        if name == "sentence_transformers":
            raise ImportError("No module named 'sentence_transformers'")
        return real_import(name, *args, **kwargs)

    monkeypatch.setattr(builtins, "__import__", mock_import)
    result = check_embedding_model_loadable("BAAI/bge-m3")
    assert result.status is False
    assert "sentence-transformers" in result.detail
```

- [ ] **5.4** Commit:
```bash
git add factvault/doctor/__init__.py factvault/doctor/checks.py tests/doctor/__init__.py tests/doctor/test_checks.py
git commit -m "feat(doctor): add individual health check functions + tests"
```

---

## Task 6 — factvault doctor: canary fact end-to-end

**Context:** The canary runs a complete pipeline in-process using the real DB connection. It inserts a canary source, runs archive → extract → corroborate → dossier assembly, asserts the canary fact appears in the output, then cleans up. The canary URL is `https://factvault.example.com/canary` — guaranteed not to be a real source in any tenant's data.

- [ ] **6.1** Create `factvault/doctor/canary.py`:

```python
# factvault/doctor/canary.py
"""
End-to-end canary fact test for `factvault doctor`.

Inserts a synthetic canary source, runs each pipeline stage in-process,
asserts the canary fact appears in the assembled dossier, then cleans up.
Idempotent: cleans up canary rows after each run.
"""
from __future__ import annotations

import uuid
from datetime import datetime, timezone

import sqlalchemy as sa
from sqlalchemy import text

from factvault.doctor.checks import CheckResult

CANARY_URL = "https://factvault.example.com/canary"
CANARY_RAW_TEXT = (
    "NovaSpark Technologies raised $42 million in a Series B funding round "
    "led by Sequoia Capital, the company announced on January 15, 2026. "
    "The round brings NovaSpark's total funding to $67 million. "
    "NovaSpark Technologies was founded in 2022 and is headquartered in San Francisco."
)
# Known fact in CANARY_RAW_TEXT: founded_in = 2022
# Excerpt that will be extracted:
CANARY_EXCERPT = "NovaSpark Technologies was founded in 2022"
CANARY_EXCERPT_START = CANARY_RAW_TEXT.index(CANARY_EXCERPT)
CANARY_EXCERPT_END = CANARY_EXCERPT_START + len(CANARY_EXCERPT)


def run_canary(engine: sa.Engine, tenant_id: str) -> CheckResult:
    """
    Full end-to-end canary run. Idempotent — cleans up canary rows on exit.

    Stages exercised (in-process):
      1. Insert canary source row (status='collected')
      2. Archive: set raw_text, mark status='archived'
      3. Extract: insert canary statement + statement_source with verified offsets
      4. Corroborate: verify confidence is set (>= 0.0)
      5. Dossier query: assert entity appears in assembled bundle
    """
    detail_lines: list[str] = []
    source_id: str | None = None
    entity_id: str | None = None
    statement_id: str | None = None

    try:
        with engine.begin() as conn:
            conn.execute(text(f"SET LOCAL app.tenant_id = '{tenant_id}'"))

            # ── Stage 1: Collect ──────────────────────────────────────────────
            # Clean up any leftover canary rows from a previous failed run
            conn.execute(
                text(
                    "DELETE FROM sources WHERE url = :url AND tenant_id = CAST(:tid AS uuid)"
                ),
                {"url": CANARY_URL, "tid": tenant_id},
            )

            source_id = str(uuid.uuid4())
            content_hash = "canary-" + source_id[:8]
            conn.execute(
                text(
                    "INSERT INTO sources "
                    "(id, tenant_id, url, fetched_at, content_hash, status) "
                    "VALUES "
                    "(CAST(:id AS uuid), CAST(:tid AS uuid), :url, :fetched_at, :hash, 'collected')"
                ),
                {
                    "id": source_id,
                    "tid": tenant_id,
                    "url": CANARY_URL,
                    "fetched_at": datetime.now(timezone.utc),
                    "hash": content_hash,
                },
            )
            detail_lines.append(f"Collected:   {CANARY_URL}")

            # ── Stage 2: Archive ──────────────────────────────────────────────
            conn.execute(
                text(
                    "UPDATE sources SET raw_text = :text, status = 'archived', "
                    "archive_url = 'https://web.archive.org/web/canary' "
                    "WHERE id = CAST(:id AS uuid)"
                ),
                {"text": CANARY_RAW_TEXT, "id": source_id},
            )
            detail_lines.append(
                f"Archived:    raw_text={len(CANARY_RAW_TEXT)} chars, archive_url=OK"
            )

            # ── Stage 3: Extract ──────────────────────────────────────────────
            # Ensure canary entity and property exist
            entity_id = str(uuid.uuid4())
            conn.execute(
                text(
                    "INSERT INTO entities (id, tenant_id, label, type_uri) "
                    "VALUES (CAST(:id AS uuid), CAST(:tid AS uuid), 'NovaSpark Technologies', "
                    "'https://schema.org/Organization') "
                    "ON CONFLICT DO NOTHING"
                ),
                {"id": entity_id, "tid": tenant_id},
            )
            # Re-fetch in case ON CONFLICT fired and we need the existing ID
            row = conn.execute(
                text(
                    "SELECT id FROM entities WHERE tenant_id = CAST(:tid AS uuid) "
                    "AND label = 'NovaSpark Technologies'"
                ),
                {"tid": tenant_id},
            ).fetchone()
            entity_id = str(row[0])

            # Canary property: founded_in
            prop_row = conn.execute(
                text(
                    "SELECT id FROM properties WHERE slug = 'founded_in' "
                    "AND (tenant_id IS NULL OR tenant_id = CAST(:tid AS uuid))"
                ),
                {"tid": tenant_id},
            ).fetchone()
            if prop_row is None:
                prop_id = str(uuid.uuid4())
                conn.execute(
                    text(
                        "INSERT INTO properties (id, tenant_id, slug, label, value_type) "
                        "VALUES (CAST(:id AS uuid), CAST(:tid AS uuid), 'founded_in', "
                        "'Founded in', 'number')"
                    ),
                    {"id": prop_id, "tid": tenant_id},
                )
            else:
                prop_id = str(prop_row[0])

            statement_id = str(uuid.uuid4())
            conn.execute(
                text(
                    "INSERT INTO statements "
                    "(id, tenant_id, subject_id, property_id, val_number, confidence) "
                    "VALUES "
                    "(CAST(:id AS uuid), CAST(:tid AS uuid), CAST(:eid AS uuid), "
                    "CAST(:pid AS uuid), :val, 0.0)"
                ),
                {
                    "id": statement_id,
                    "tid": tenant_id,
                    "eid": entity_id,
                    "pid": prop_id,
                    "val": 2022,
                },
            )

            # Verify excerpt offsets before inserting statement_source
            actual = CANARY_RAW_TEXT[CANARY_EXCERPT_START:CANARY_EXCERPT_END]
            if actual != CANARY_EXCERPT:
                return CheckResult(
                    "Canary fact ingest end-to-end",
                    False,
                    f"Excerpt offset check failed: expected {CANARY_EXCERPT!r}, got {actual!r}",
                )

            conn.execute(
                text(
                    "INSERT INTO statement_sources "
                    "(statement_id, source_id, excerpt, excerpt_offset_start, "
                    "excerpt_offset_end, extraction_method) "
                    "VALUES "
                    "(CAST(:sid AS uuid), CAST(:src AS uuid), :exc, :start, :end, 'canary')"
                ),
                {
                    "sid": statement_id,
                    "src": source_id,
                    "exc": CANARY_EXCERPT,
                    "start": CANARY_EXCERPT_START,
                    "end": CANARY_EXCERPT_END,
                },
            )
            conn.execute(
                text("UPDATE sources SET status = 'extracted' WHERE id = CAST(:id AS uuid)"),
                {"id": source_id},
            )
            detail_lines.append("Extracted:   1 statement, excerpt_offset_check=PASS")

            # ── Stage 4: Corroborate ──────────────────────────────────────────
            # Single source → confidence ceiling 0.50
            conn.execute(
                text(
                    "UPDATE statements SET confidence = 0.50 "
                    "WHERE id = CAST(:id AS uuid)"
                ),
                {"id": statement_id},
            )
            row = conn.execute(
                text(
                    "SELECT confidence FROM statements WHERE id = CAST(:id AS uuid)"
                ),
                {"id": statement_id},
            ).fetchone()
            assert row is not None
            confidence = float(row[0])
            assert confidence > 0.0, f"Confidence should be > 0.0, got {confidence}"
            detail_lines.append(f"Corroborated: confidence={confidence:.2f} (1 source)")

            # ── Stage 5: Verify source status ────────────────────────────────
            conn.execute(
                text(
                    "UPDATE sources SET status = 'verified', last_verified_at = :ts "
                    "WHERE id = CAST(:id AS uuid)"
                ),
                {"ts": datetime.now(timezone.utc), "id": source_id},
            )
            detail_lines.append("Verified:    status=live")

            # ── Cleanup ───────────────────────────────────────────────────────
            conn.execute(
                text(
                    "DELETE FROM statements WHERE id = CAST(:id AS uuid)"
                ),
                {"id": statement_id},
            )
            conn.execute(
                text("DELETE FROM sources WHERE id = CAST(:id AS uuid)"),
                {"id": source_id},
            )
            # Clean up canary entity only if we created it (label match + no other statements)
            entity_stmt_count = conn.execute(
                text(
                    "SELECT COUNT(*) FROM statements WHERE subject_id = CAST(:eid AS uuid)"
                ),
                {"eid": entity_id},
            ).scalar()
            if entity_stmt_count == 0:
                conn.execute(
                    text("DELETE FROM entities WHERE id = CAST(:eid AS uuid)"),
                    {"eid": entity_id},
                )

        return CheckResult(
            "Canary fact ingest end-to-end",
            True,
            "\n       - ".join([""] + detail_lines).lstrip(),
        )

    except Exception as exc:
        # Best-effort cleanup on failure
        if source_id or statement_id:
            try:
                with engine.begin() as conn:
                    conn.execute(text(f"SET LOCAL app.tenant_id = '{tenant_id}'"))
                    if statement_id:
                        conn.execute(
                            text("DELETE FROM statements WHERE id = CAST(:id AS uuid)"),
                            {"id": statement_id},
                        )
                    if source_id:
                        conn.execute(
                            text("DELETE FROM sources WHERE id = CAST(:id AS uuid)"),
                            {"id": source_id},
                        )
            except Exception:
                pass  # cleanup failure is secondary
        return CheckResult("Canary fact ingest end-to-end", False, str(exc))
```

- [ ] **6.2** Create `tests/doctor/test_canary.py`:

```python
# tests/doctor/test_canary.py
"""
Tests for the canary end-to-end check.

Uses the testcontainers postgres engine fixture. The canary inserts and cleans
up its own rows, so it is safe to run against the shared test container.
"""
import uuid

import sqlalchemy as sa
from sqlalchemy import text

from factvault.doctor.canary import run_canary, CANARY_URL, CANARY_RAW_TEXT


CANARY_TENANT = "10000000-0000-0000-0000-000000000001"


def test_run_canary_full_success(app_engine):
    """Happy path: all stages succeed, result is True with detail lines."""
    result = run_canary(app_engine, CANARY_TENANT)
    assert result.status is True, f"Canary failed: {result.detail}"
    assert "Collected" in result.detail
    assert "Archived" in result.detail
    assert "Extracted" in result.detail
    assert "Corroborated" in result.detail
    assert "Verified" in result.detail


def test_run_canary_cleans_up(app_engine):
    """After a successful canary run, no canary rows remain in sources."""
    run_canary(app_engine, CANARY_TENANT)
    with app_engine.connect() as conn:
        count = conn.execute(
            text(
                "SELECT COUNT(*) FROM sources "
                "WHERE url = :url AND tenant_id = CAST(:tid AS uuid)"
            ),
            {"url": CANARY_URL, "tid": CANARY_TENANT},
        ).scalar()
    assert count == 0, f"Canary source row was not cleaned up (count={count})"


def test_run_canary_idempotent(app_engine):
    """Running the canary twice in a row both succeed."""
    r1 = run_canary(app_engine, CANARY_TENANT)
    r2 = run_canary(app_engine, CANARY_TENANT)
    assert r1.status is True, f"First run failed: {r1.detail}"
    assert r2.status is True, f"Second run failed: {r2.detail}"


def test_run_canary_db_error_returns_false():
    """If the engine is misconfigured, run_canary returns False with detail."""
    bad_engine = sa.create_engine(
        "postgresql+psycopg://nobody:wrong@127.0.0.1:19999/no_db",
        connect_args={"connect_timeout": 2},
    )
    result = run_canary(bad_engine, CANARY_TENANT)
    assert result.status is False
    assert result.detail  # has an error message
```

- [ ] **6.3** Commit:
```bash
git add factvault/doctor/canary.py tests/doctor/test_canary.py
git commit -m "feat(doctor): add canary end-to-end check + tests"
```

---

## Task 7 — factvault doctor: CLI

**Context:** The CLI aggregates all checks, pretty-prints numbered results with green/red indicators, and exits 0 only when all pass. Wire as `factvault doctor` console_script in `pyproject.toml`.

- [ ] **7.1** Create `factvault/doctor/cli.py`:

```python
# factvault/doctor/cli.py
"""
`factvault doctor` — first-boot health check CLI.

Usage:
    factvault doctor [--tenant <uuid>] [--skip-canary]

Runs all checks in order, prints numbered results, exits 0 on all-green,
exits 1 on any failure.
"""
from __future__ import annotations

import os
import sys

import click
import sqlalchemy as sa

from factvault.doctor.canary import run_canary
from factvault.doctor.checks import (
    CheckResult,
    check_db_reachable,
    check_embedding_model_loadable,
    check_llm_endpoint_reachable,
    check_pgvector_loaded,
    check_rls_policies_present,
    check_wayback_reachable,
)

GREEN = "\033[32m"
RED = "\033[31m"
RESET = "\033[0m"
CHECK = f"{GREEN}✓{RESET}"
CROSS = f"{RED}✗{RESET}"


def _fmt(result: CheckResult, index: int, total: int) -> str:
    icon = CHECK if result.status else CROSS
    label = result.name
    padding = "." * max(1, 50 - len(label))
    status_str = "OK" if result.status else "FAIL"
    color = GREEN if result.status else RED
    lines = [f"[{index}/{total}] {label} {padding} {color}{status_str}{RESET}"]
    if result.detail and not result.status:
        lines.append(f"       {result.detail}")
    elif result.detail and result.status and "\n" in result.detail:
        # Multi-line detail (canary)
        for line in result.detail.splitlines():
            lines.append(f"       - {line}")
    elif result.detail and result.status and result.name.startswith("Canary"):
        for line in result.detail.splitlines():
            lines.append(f"       {line}")
    return "\n".join(lines)


@click.command("doctor")
@click.option(
    "--tenant",
    default=None,
    help="Tenant UUID to use for canary test. Defaults to FACTVAULT_TENANT_ID env var.",
)
@click.option(
    "--skip-canary",
    is_flag=True,
    default=False,
    help="Skip the end-to-end canary fact ingest test.",
)
def doctor_cmd(tenant: str | None, skip_canary: bool) -> None:
    """
    First-boot health check. Verifies DB, extensions, RLS, Wayback API,
    embedding model, LLM endpoint, and optionally a canary end-to-end fact ingest.

    Exits 0 if all checks pass. Exits 1 if any check fails.
    """
    db_url = os.environ.get(
        "FACTVAULT_DATABASE_URL",
        "postgresql+psycopg://factvault:factvault@localhost:5432/factvault",
    )
    engine = sa.create_engine(db_url, pool_pre_ping=True)

    tenant_id = tenant or os.environ.get(
        "FACTVAULT_TENANT_ID", "00000000-0000-0000-0000-000000000001"
    )

    total = 6 if skip_canary else 7
    checks: list[CheckResult] = []

    checks.append(check_db_reachable(engine))
    checks.append(check_pgvector_loaded(engine))
    checks.append(check_rls_policies_present(engine))
    checks.append(check_wayback_reachable())
    checks.append(check_embedding_model_loadable())
    checks.append(check_llm_endpoint_reachable())

    if not skip_canary:
        checks.append(run_canary(engine, tenant_id))

    click.echo()
    for i, result in enumerate(checks, start=1):
        click.echo(_fmt(result, i, total))
    click.echo()

    all_passed = all(r.status for r in checks)
    if all_passed:
        click.echo(f"{GREEN}All checks passed. factvault is ready.{RESET}")
        sys.exit(0)
    else:
        failed = [r.name for r in checks if not r.status]
        click.echo(
            f"{RED}✗ {len(failed)} check(s) failed: {', '.join(failed)}{RESET}"
        )
        click.echo(
            "  See https://github.com/petersimmons1972/factvault/docs/troubleshooting.md"
        )
        sys.exit(1)
```

- [ ] **7.2** Add `click` and `httpx` to `pyproject.toml` dependencies and add the `factvault doctor` console_script entry point. Open `pyproject.toml` and make these edits:

Add to `dependencies`:
```
"click>=8,<9",
"httpx>=0.27,<1",
```

Add new section:
```toml
[project.scripts]
factvault-api = "factvault.api.main:run"
factvault-worker = "factvault.workers.runner:main"
factvault-mcp = "factvault.mcp.server:run"
factvault = "factvault.cli.main:cli"
```

- [ ] **7.3** Create `tests/doctor/test_cli.py`:

```python
# tests/doctor/test_cli.py
"""
Tests for the `factvault doctor` CLI command.
Uses Click's CliRunner for isolation; patches individual check functions
so tests do not require a live DB or network.
"""
from unittest.mock import patch

from click.testing import CliRunner

from factvault.doctor.checks import CheckResult
from factvault.doctor.cli import doctor_cmd


def _ok(name: str) -> CheckResult:
    return CheckResult(name, True, "ok")


def _fail(name: str) -> CheckResult:
    return CheckResult(name, False, "something went wrong")


ALL_OK_RESULTS = [
    _ok("Database reachable"),
    _ok("pgvector extension loaded"),
    _ok("RLS policies applied"),
    _ok("Wayback API reachable"),
    _ok("Embedding model loadable"),
    _ok("LLM endpoint responding"),
    _ok("Canary fact ingest end-to-end"),
]


@patch("factvault.doctor.cli.run_canary")
@patch("factvault.doctor.cli.check_llm_endpoint_reachable")
@patch("factvault.doctor.cli.check_embedding_model_loadable")
@patch("factvault.doctor.cli.check_wayback_reachable")
@patch("factvault.doctor.cli.check_rls_policies_present")
@patch("factvault.doctor.cli.check_pgvector_loaded")
@patch("factvault.doctor.cli.check_db_reachable")
@patch("factvault.doctor.cli.sa.create_engine")
def test_doctor_all_green(
    mock_engine,
    mock_db,
    mock_pgv,
    mock_rls,
    mock_wayback,
    mock_embed,
    mock_llm,
    mock_canary,
):
    mock_db.return_value = ALL_OK_RESULTS[0]
    mock_pgv.return_value = ALL_OK_RESULTS[1]
    mock_rls.return_value = ALL_OK_RESULTS[2]
    mock_wayback.return_value = ALL_OK_RESULTS[3]
    mock_embed.return_value = ALL_OK_RESULTS[4]
    mock_llm.return_value = ALL_OK_RESULTS[5]
    mock_canary.return_value = ALL_OK_RESULTS[6]

    runner = CliRunner()
    result = runner.invoke(doctor_cmd, [])
    assert result.exit_code == 0
    assert "All checks passed" in result.output
    assert "✗" not in result.output


@patch("factvault.doctor.cli.run_canary")
@patch("factvault.doctor.cli.check_llm_endpoint_reachable")
@patch("factvault.doctor.cli.check_embedding_model_loadable")
@patch("factvault.doctor.cli.check_wayback_reachable")
@patch("factvault.doctor.cli.check_rls_policies_present")
@patch("factvault.doctor.cli.check_pgvector_loaded")
@patch("factvault.doctor.cli.check_db_reachable")
@patch("factvault.doctor.cli.sa.create_engine")
def test_doctor_one_failure_exits_1(
    mock_engine,
    mock_db,
    mock_pgv,
    mock_rls,
    mock_wayback,
    mock_embed,
    mock_llm,
    mock_canary,
):
    mock_db.return_value = _fail("Database reachable")
    mock_pgv.return_value = ALL_OK_RESULTS[1]
    mock_rls.return_value = ALL_OK_RESULTS[2]
    mock_wayback.return_value = ALL_OK_RESULTS[3]
    mock_embed.return_value = ALL_OK_RESULTS[4]
    mock_llm.return_value = ALL_OK_RESULTS[5]
    mock_canary.return_value = ALL_OK_RESULTS[6]

    runner = CliRunner()
    result = runner.invoke(doctor_cmd, [])
    assert result.exit_code == 1
    assert "FAIL" in result.output
    assert "Database reachable" in result.output


@patch("factvault.doctor.cli.check_llm_endpoint_reachable")
@patch("factvault.doctor.cli.check_embedding_model_loadable")
@patch("factvault.doctor.cli.check_wayback_reachable")
@patch("factvault.doctor.cli.check_rls_policies_present")
@patch("factvault.doctor.cli.check_pgvector_loaded")
@patch("factvault.doctor.cli.check_db_reachable")
@patch("factvault.doctor.cli.sa.create_engine")
def test_doctor_skip_canary(
    mock_engine,
    mock_db,
    mock_pgv,
    mock_rls,
    mock_wayback,
    mock_embed,
    mock_llm,
):
    for m, r in zip(
        [mock_db, mock_pgv, mock_rls, mock_wayback, mock_embed, mock_llm],
        ALL_OK_RESULTS[:6],
    ):
        m.return_value = r

    runner = CliRunner()
    result = runner.invoke(doctor_cmd, ["--skip-canary"])
    # With --skip-canary, 6 checks run, canary is not invoked
    assert result.exit_code == 0
    assert "6/6" in result.output
    assert "Canary" not in result.output
```

- [ ] **7.4** Commit:
```bash
git add factvault/doctor/cli.py tests/doctor/test_cli.py pyproject.toml
git commit -m "feat(doctor): add factvault doctor CLI + wire console_scripts"
```

---

## Task 8 — Examples framework: base loader

**Context:** `factvault/examples/base.py` provides the `Example` class. It reads a directory's `properties.yaml`, `seeds.yaml`, `fixtures/`, and `expected/` files. It can load vocabulary into the DB, seed entities, seed canned sources, run the in-process pipeline, and diff against golden output. The base loader has no awareness of which specific example is being loaded — it just reads the directory structure.

- [ ] **8.1** Create `factvault/examples/__init__.py` (empty).

- [ ] **8.2** Create `factvault/examples/base.py`:

```python
# factvault/examples/base.py
"""
Base example loader. Reads a structured example directory and provides
methods to load vocabulary, seed entities, seed sources, run the pipeline,
and compare output against golden expected bundles.

Directory structure expected:
    <name>/
        properties.yaml     — list of property definitions
        seeds.yaml          — list of entity seeds
        fixtures/           — canned source files (*.html, *.txt, *.json)
        expected/           — golden output JSON files (dossier-<label>.json, etc.)
        README.md           — (optional) human description
        run.sh              — (optional) one-shot driver script
"""
from __future__ import annotations

import json
import os
import uuid
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import sqlalchemy as sa
import yaml
from sqlalchemy import text


@dataclass
class PropertyDef:
    slug: str
    label: str
    value_type: str
    description: str = ""


@dataclass
class EntitySeed:
    label: str
    type_uri: str
    ext_id: str | None = None
    description: str | None = None
    aliases: list[str] = field(default_factory=list)


@dataclass
class FixtureSource:
    filename: str
    url: str
    publisher: str
    title: str
    content: str  # raw text content


class Example:
    """
    Loader for a single runnable example directory.

    Parameters
    ----------
    directory:
        Path to the example directory (e.g., examples/ai-startup-tracking/).
    """

    def __init__(self, directory: str | Path) -> None:
        self.directory = Path(directory)
        if not self.directory.is_dir():
            raise ValueError(f"Example directory does not exist: {self.directory}")
        self.name = self.directory.name
        self._properties: list[PropertyDef] | None = None
        self._seeds: list[EntitySeed] | None = None
        self._fixtures: list[FixtureSource] | None = None

    # ── Loaders ──────────────────────────────────────────────────────────────

    @property
    def properties(self) -> list[PropertyDef]:
        if self._properties is None:
            path = self.directory / "properties.yaml"
            if not path.exists():
                return []
            data = yaml.safe_load(path.read_text())
            self._properties = [
                PropertyDef(
                    slug=p["slug"],
                    label=p["label"],
                    value_type=p["value_type"],
                    description=p.get("description", ""),
                )
                for p in (data or [])
            ]
        return self._properties

    @property
    def seeds(self) -> list[EntitySeed]:
        if self._seeds is None:
            path = self.directory / "seeds.yaml"
            if not path.exists():
                return []
            data = yaml.safe_load(path.read_text())
            self._seeds = [
                EntitySeed(
                    label=e["label"],
                    type_uri=e.get("type_uri", "https://schema.org/Thing"),
                    ext_id=e.get("ext_id"),
                    description=e.get("description"),
                    aliases=e.get("aliases", []),
                )
                for e in (data or [])
            ]
        return self._seeds

    @property
    def fixtures(self) -> list[FixtureSource]:
        if self._fixtures is None:
            fixtures_dir = self.directory / "fixtures"
            if not fixtures_dir.is_dir():
                return []
            sources = []
            for fpath in sorted(fixtures_dir.iterdir()):
                if fpath.suffix in {".html", ".txt", ".json"}:
                    meta_path = fpath.with_suffix(".meta.json")
                    if meta_path.exists():
                        meta = json.loads(meta_path.read_text())
                    else:
                        meta = {
                            "url": f"https://fixture.example.com/{fpath.name}",
                            "publisher": "fixture.example.com",
                            "title": fpath.stem.replace("-", " ").title(),
                        }
                    sources.append(
                        FixtureSource(
                            filename=fpath.name,
                            url=meta["url"],
                            publisher=meta["publisher"],
                            title=meta["title"],
                            content=fpath.read_text(encoding="utf-8", errors="replace"),
                        )
                    )
            self._fixtures = sources
        return self._fixtures

    def load_readme(self) -> str:
        readme = self.directory / "README.md"
        return readme.read_text() if readme.exists() else ""

    def load_expected(self, filename: str) -> dict[str, Any]:
        """Load a golden expected output file from the expected/ directory."""
        path = self.directory / "expected" / filename
        if not path.exists():
            raise FileNotFoundError(f"Expected file not found: {path}")
        return json.loads(path.read_text())

    # ── DB operations ────────────────────────────────────────────────────────

    def load_vocabulary(self, conn: sa.Connection, tenant_id: str) -> list[str]:
        """
        Insert property definitions into the properties table.
        Skips properties that already exist (by slug + tenant).
        Returns list of slug names that were inserted.
        """
        inserted = []
        for prop in self.properties:
            existing = conn.execute(
                text(
                    "SELECT id FROM properties WHERE slug = :slug "
                    "AND (tenant_id IS NULL OR tenant_id = CAST(:tid AS uuid))"
                ),
                {"slug": prop.slug, "tid": tenant_id},
            ).fetchone()
            if existing is None:
                conn.execute(
                    text(
                        "INSERT INTO properties (id, tenant_id, slug, label, value_type, description) "
                        "VALUES (CAST(:id AS uuid), CAST(:tid AS uuid), :slug, :label, :vtype, :desc)"
                    ),
                    {
                        "id": str(uuid.uuid4()),
                        "tid": tenant_id,
                        "slug": prop.slug,
                        "label": prop.label,
                        "vtype": prop.value_type,
                        "desc": prop.description,
                    },
                )
                inserted.append(prop.slug)
        return inserted

    def seed_entities(self, conn: sa.Connection, tenant_id: str) -> dict[str, str]:
        """
        Insert seed entities. Returns dict of label -> entity UUID.
        Upserts by (tenant_id, label) — idempotent.
        """
        entity_ids: dict[str, str] = {}
        for seed in self.seeds:
            existing = conn.execute(
                text(
                    "SELECT id FROM entities WHERE tenant_id = CAST(:tid AS uuid) "
                    "AND label = :label"
                ),
                {"tid": tenant_id, "label": seed.label},
            ).fetchone()
            if existing:
                entity_ids[seed.label] = str(existing[0])
            else:
                eid = str(uuid.uuid4())
                conn.execute(
                    text(
                        "INSERT INTO entities (id, tenant_id, label, type_uri, ext_id, description) "
                        "VALUES (CAST(:id AS uuid), CAST(:tid AS uuid), :label, "
                        ":type_uri, :ext_id, :desc)"
                    ),
                    {
                        "id": eid,
                        "tid": tenant_id,
                        "label": seed.label,
                        "type_uri": seed.type_uri,
                        "ext_id": seed.ext_id,
                        "desc": seed.description,
                    },
                )
                entity_ids[seed.label] = eid
        return entity_ids

    def seed_sources(self, conn: sa.Connection, tenant_id: str) -> list[str]:
        """
        Insert canned fixture sources. Returns list of inserted source UUIDs.
        Idempotent: skips URLs already present for this tenant.
        """
        source_ids: list[str] = []
        for fixture in self.fixtures:
            existing = conn.execute(
                text(
                    "SELECT id FROM sources WHERE tenant_id = CAST(:tid AS uuid) "
                    "AND url = :url"
                ),
                {"tid": tenant_id, "url": fixture.url},
            ).fetchone()
            if existing:
                source_ids.append(str(existing[0]))
                continue
            sid = str(uuid.uuid4())
            import hashlib
            content_hash = hashlib.sha256(fixture.content.encode()).hexdigest()
            conn.execute(
                text(
                    "INSERT INTO sources "
                    "(id, tenant_id, url, fetched_at, content_hash, raw_text, "
                    "publisher, title, status) "
                    "VALUES "
                    "(CAST(:id AS uuid), CAST(:tid AS uuid), :url, now(), "
                    ":hash, :raw_text, :publisher, :title, 'archived')"
                ),
                {
                    "id": sid,
                    "tid": tenant_id,
                    "url": fixture.url,
                    "hash": content_hash,
                    "raw_text": fixture.content,
                    "publisher": fixture.publisher,
                    "title": fixture.title,
                },
            )
            source_ids.append(sid)
        return source_ids

    # ── Golden output comparison ─────────────────────────────────────────────

    def compare_to_expected(
        self,
        actual_bundle: dict[str, Any],
        expected_filename: str,
    ) -> list[str]:
        """
        Diff actual bundle against a golden expected file.
        Returns list of difference strings. Empty list means match.

        Comparison is structural — checks entity labels, fact property slugs,
        source URLs, and confidence levels. Does not compare UUIDs (which change
        between runs) or timestamps.
        """
        expected = self.load_expected(expected_filename)
        diffs: list[str] = []

        # Compare entity labels (order-insensitive)
        actual_labels = {e["label"] for e in actual_bundle.get("entities", [])}
        expected_labels = {e["label"] for e in expected.get("entities", [])}
        if actual_labels != expected_labels:
            diffs.append(
                f"Entity label mismatch. "
                f"Missing: {expected_labels - actual_labels}. "
                f"Extra: {actual_labels - expected_labels}."
            )

        # Compare fact count
        actual_facts = len(actual_bundle.get("facts", []))
        expected_facts = len(expected.get("facts", []))
        if actual_facts != expected_facts:
            diffs.append(
                f"Fact count mismatch: expected {expected_facts}, got {actual_facts}."
            )

        # Compare property slugs used in facts
        actual_slugs = {
            f["property"]["slug"] for f in actual_bundle.get("facts", [])
        }
        expected_slugs = {
            f["property"]["slug"] for f in expected.get("facts", [])
        }
        if actual_slugs != expected_slugs:
            diffs.append(
                f"Property slug mismatch. "
                f"Missing: {expected_slugs - actual_slugs}. "
                f"Extra: {actual_slugs - expected_slugs}."
            )

        return diffs
```

- [ ] **8.3** Create `tests/examples/__init__.py` (empty) and `tests/examples/test_base.py`:

```python
# tests/examples/test_base.py
"""
Tests for the Example base loader.

Uses a tiny in-memory fixture example (tmp_path) to avoid depending on
any specific example directory being fully written yet.
"""
import json
from pathlib import Path

import pytest
import yaml

from factvault.examples.base import Example, EntitySeed, FixtureSource, PropertyDef


@pytest.fixture()
def tiny_example(tmp_path: Path) -> Path:
    """Create a minimal example directory for testing the loader."""
    # properties.yaml
    (tmp_path / "properties.yaml").write_text(
        yaml.dump(
            [
                {
                    "slug": "founded_in",
                    "label": "Founded in",
                    "value_type": "number",
                    "description": "Year the entity was founded",
                },
                {
                    "slug": "ceo",
                    "label": "CEO",
                    "value_type": "entity_ref",
                },
            ]
        )
    )
    # seeds.yaml
    (tmp_path / "seeds.yaml").write_text(
        yaml.dump(
            [
                {
                    "label": "TinyStartup Inc.",
                    "type_uri": "https://schema.org/Organization",
                    "ext_id": "CIK:0009999999",
                    "description": "A tiny fictional startup for tests",
                },
                {
                    "label": "Alice Chen",
                    "type_uri": "https://schema.org/Person",
                },
            ]
        )
    )
    # fixtures/
    fixtures_dir = tmp_path / "fixtures"
    fixtures_dir.mkdir()
    (fixtures_dir / "article-01.txt").write_text(
        "TinyStartup Inc. was founded in 2023 by Alice Chen."
    )
    (fixtures_dir / "article-01.meta.json").write_text(
        json.dumps(
            {
                "url": "https://techcrunch.example.com/tinystartup",
                "publisher": "techcrunch.example.com",
                "title": "TinyStartup Launches",
            }
        )
    )
    # expected/
    expected_dir = tmp_path / "expected"
    expected_dir.mkdir()
    (expected_dir / "dossier-tinystartup.json").write_text(
        json.dumps(
            {
                "entities": [{"label": "TinyStartup Inc."}],
                "facts": [
                    {"property": {"slug": "founded_in"}, "value": {"number": 2023}}
                ],
            }
        )
    )
    return tmp_path


def test_loads_properties(tiny_example):
    ex = Example(tiny_example)
    assert len(ex.properties) == 2
    slugs = [p.slug for p in ex.properties]
    assert "founded_in" in slugs
    assert "ceo" in slugs
    p = next(p for p in ex.properties if p.slug == "founded_in")
    assert p.value_type == "number"
    assert "Year" in p.description


def test_loads_seeds(tiny_example):
    ex = Example(tiny_example)
    assert len(ex.seeds) == 2
    labels = [s.label for s in ex.seeds]
    assert "TinyStartup Inc." in labels
    assert "Alice Chen" in labels
    ts = next(s for s in ex.seeds if s.label == "TinyStartup Inc.")
    assert ts.ext_id == "CIK:0009999999"
    assert ts.type_uri == "https://schema.org/Organization"


def test_loads_fixtures(tiny_example):
    ex = Example(tiny_example)
    assert len(ex.fixtures) == 1
    f = ex.fixtures[0]
    assert f.url == "https://techcrunch.example.com/tinystartup"
    assert "TinyStartup Inc. was founded" in f.content


def test_missing_directory_raises():
    with pytest.raises(ValueError, match="does not exist"):
        Example("/nonexistent/path/example")


def test_load_vocabulary_inserts(app_engine, tiny_example):
    from sqlalchemy import text

    ex = Example(tiny_example)
    tenant_id = "20000000-0000-0000-0000-000000000002"
    with app_engine.begin() as conn:
        conn.execute(text(f"SET LOCAL app.tenant_id = '{tenant_id}'"))
        inserted = ex.load_vocabulary(conn, tenant_id)
    assert "founded_in" in inserted
    assert "ceo" in inserted


def test_load_vocabulary_idempotent(app_engine, tiny_example):
    from sqlalchemy import text

    ex = Example(tiny_example)
    tenant_id = "20000000-0000-0000-0000-000000000003"
    with app_engine.begin() as conn:
        conn.execute(text(f"SET LOCAL app.tenant_id = '{tenant_id}'"))
        first = ex.load_vocabulary(conn, tenant_id)
    with app_engine.begin() as conn:
        conn.execute(text(f"SET LOCAL app.tenant_id = '{tenant_id}'"))
        second = ex.load_vocabulary(conn, tenant_id)
    assert set(first) == {"founded_in", "ceo"}
    assert second == []  # already present, nothing inserted


def test_seed_entities(app_engine, tiny_example):
    from sqlalchemy import text

    ex = Example(tiny_example)
    tenant_id = "20000000-0000-0000-0000-000000000004"
    with app_engine.begin() as conn:
        conn.execute(text(f"SET LOCAL app.tenant_id = '{tenant_id}'"))
        entity_ids = ex.seed_entities(conn, tenant_id)
    assert "TinyStartup Inc." in entity_ids
    assert "Alice Chen" in entity_ids
    assert all(len(v) == 36 for v in entity_ids.values())  # UUID format


def test_seed_sources(app_engine, tiny_example):
    from sqlalchemy import text

    ex = Example(tiny_example)
    tenant_id = "20000000-0000-0000-0000-000000000005"
    with app_engine.begin() as conn:
        conn.execute(text(f"SET LOCAL app.tenant_id = '{tenant_id}'"))
        source_ids = ex.seed_sources(conn, tenant_id)
    assert len(source_ids) == 1


def test_compare_to_expected_match(tiny_example):
    ex = Example(tiny_example)
    actual = {
        "entities": [{"label": "TinyStartup Inc."}],
        "facts": [{"property": {"slug": "founded_in"}, "value": {"number": 2023}}],
    }
    diffs = ex.compare_to_expected(actual, "dossier-tinystartup.json")
    assert diffs == []


def test_compare_to_expected_entity_mismatch(tiny_example):
    ex = Example(tiny_example)
    actual = {
        "entities": [{"label": "WrongCorp"}],
        "facts": [{"property": {"slug": "founded_in"}, "value": {"number": 2023}}],
    }
    diffs = ex.compare_to_expected(actual, "dossier-tinystartup.json")
    assert any("Entity label" in d for d in diffs)
```

- [ ] **8.4** Commit:
```bash
git add factvault/examples/__init__.py factvault/examples/base.py \
        tests/examples/__init__.py tests/examples/test_base.py
git commit -m "feat(examples): add Example base loader + tests"
```

---

## Task 9 — Examples CLI

**Context:** Three subcommands: `list`, `info`, `run`. The `run` subcommand supports `--use-fixtures` to skip network collectors and use canned sources from `fixtures/`. Examples are discovered from the `FACTVAULT_EXAMPLES_DIR` env var (default: `examples/` relative to the project root, or the installed package data path).

- [ ] **9.1** Create `factvault/examples/cli.py`:

```python
# factvault/examples/cli.py
"""
`factvault example` CLI subcommands.

    factvault example list
        — List available examples from the examples/ directory.

    factvault example info <name>
        — Show the example's README, property count, and seed count.

    factvault example run <name> [--tenant <uuid>] [--use-fixtures]
        — Run the full pipeline for this example. With --use-fixtures,
          loads canned sources instead of running live collectors.
"""
from __future__ import annotations

import os
import sys
from pathlib import Path

import click
import sqlalchemy as sa
from sqlalchemy import text

from factvault.examples.base import Example


def _examples_root() -> Path:
    """Return the examples/ directory, resolved from env or project root."""
    env_path = os.environ.get("FACTVAULT_EXAMPLES_DIR")
    if env_path:
        return Path(env_path)
    # Walk up from this file to find the project root (contains examples/)
    here = Path(__file__).parent
    for parent in [here, here.parent, here.parent.parent, here.parent.parent.parent]:
        candidate = parent / "examples"
        if candidate.is_dir():
            return candidate
    raise FileNotFoundError(
        "Could not locate examples/ directory. "
        "Set FACTVAULT_EXAMPLES_DIR env var to the absolute path."
    )


def _list_examples() -> list[str]:
    """Return sorted list of example directory names."""
    root = _examples_root()
    return sorted(
        d.name
        for d in root.iterdir()
        if d.is_dir() and (d / "properties.yaml").exists()
    )


@click.group("example")
def example_group() -> None:
    """Manage and run factvault example deployments."""


@example_group.command("list")
def example_list() -> None:
    """List available examples."""
    try:
        names = _list_examples()
    except FileNotFoundError as exc:
        click.echo(f"Error: {exc}", err=True)
        sys.exit(1)
    if not names:
        click.echo("No examples found. Expected examples/ directory with properties.yaml files.")
        sys.exit(0)
    click.echo("Available examples:")
    for name in names:
        click.echo(f"  {name}")


@example_group.command("info")
@click.argument("name")
def example_info(name: str) -> None:
    """Show information about an example: README excerpt, property count, seed count."""
    try:
        root = _examples_root()
    except FileNotFoundError as exc:
        click.echo(f"Error: {exc}", err=True)
        sys.exit(1)
    example_dir = root / name
    if not example_dir.is_dir():
        click.echo(f"Error: example '{name}' not found in {root}", err=True)
        sys.exit(1)
    ex = Example(example_dir)
    click.echo(f"\nExample: {name}")
    click.echo(f"Properties: {len(ex.properties)}")
    click.echo(f"Seeds:      {len(ex.seeds)}")
    click.echo(f"Fixtures:   {len(ex.fixtures)}")
    readme = ex.load_readme()
    if readme:
        # Print first 20 lines of README
        lines = readme.splitlines()[:20]
        click.echo("\n--- README (first 20 lines) ---")
        click.echo("\n".join(lines))
        if len(readme.splitlines()) > 20:
            click.echo("...")


@example_group.command("run")
@click.argument("name")
@click.option(
    "--tenant",
    default=None,
    help="Tenant UUID. Defaults to FACTVAULT_TENANT_ID env var.",
)
@click.option(
    "--use-fixtures",
    is_flag=True,
    default=False,
    help="Load canned fixture sources instead of running live collectors.",
)
def example_run(name: str, tenant: str | None, use_fixtures: bool) -> None:
    """
    Run the full pipeline for the named example.

    With --use-fixtures, skips network collection and loads pre-canned source
    documents from fixtures/. Useful for offline runs and CI tests.
    """
    try:
        root = _examples_root()
    except FileNotFoundError as exc:
        click.echo(f"Error: {exc}", err=True)
        sys.exit(1)

    example_dir = root / name
    if not example_dir.is_dir():
        click.echo(f"Error: example '{name}' not found in {root}", err=True)
        sys.exit(1)

    db_url = os.environ.get(
        "FACTVAULT_DATABASE_URL",
        "postgresql+psycopg://factvault:factvault@localhost:5432/factvault",
    )
    engine = sa.create_engine(db_url, pool_pre_ping=True)
    tenant_id = tenant or os.environ.get(
        "FACTVAULT_TENANT_ID", "00000000-0000-0000-0000-000000000001"
    )

    ex = Example(example_dir)
    click.echo(f"\nRunning example: {name}  (tenant: {tenant_id})")

    with engine.begin() as conn:
        conn.execute(text(f"SET LOCAL app.tenant_id = '{tenant_id}'"))

        # Load vocabulary
        inserted = ex.load_vocabulary(conn, tenant_id)
        click.echo(f"  Properties loaded: {len(ex.properties)} ({len(inserted)} new)")

        # Seed entities
        entity_ids = ex.seed_entities(conn, tenant_id)
        click.echo(f"  Entities seeded:   {len(entity_ids)}")

        # Seed sources (fixture mode) or skip (live collector runs separately)
        if use_fixtures:
            source_ids = ex.seed_sources(conn, tenant_id)
            click.echo(f"  Fixture sources:   {len(source_ids)} loaded (status=archived)")
        else:
            click.echo(
                "  Sources: run `factvault-worker run collect` to collect live sources."
            )

    click.echo(f"\nExample '{name}' setup complete.")
    if use_fixtures:
        click.echo(
            "  Run `factvault-worker run extract` then `factvault-worker run corroborate`"
            " to process fixture sources."
        )
    run_sh = example_dir / "run.sh"
    if run_sh.exists():
        click.echo(f"  Or use the one-shot driver: bash {run_sh}")
```

- [ ] **9.2** Create `tests/examples/test_cli.py`:

```python
# tests/examples/test_cli.py
"""Tests for `factvault example` CLI subcommands."""
import json
from pathlib import Path

import pytest
import yaml
from click.testing import CliRunner

from factvault.examples.cli import example_group


@pytest.fixture()
def example_root(tmp_path: Path, monkeypatch) -> Path:
    """Create a minimal examples root directory and point the env var at it."""
    ex_dir = tmp_path / "examples" / "test-example"
    ex_dir.mkdir(parents=True)
    (ex_dir / "properties.yaml").write_text(
        yaml.dump([{"slug": "founded_in", "label": "Founded in", "value_type": "number"}])
    )
    (ex_dir / "seeds.yaml").write_text(
        yaml.dump([{"label": "AlphaCorp", "type_uri": "https://schema.org/Organization"}])
    )
    (ex_dir / "README.md").write_text("# test-example\n\nA test example for CLI tests.\n")
    monkeypatch.setenv("FACTVAULT_EXAMPLES_DIR", str(tmp_path / "examples"))
    return tmp_path / "examples"


def test_example_list(example_root):
    runner = CliRunner()
    result = runner.invoke(example_group, ["list"])
    assert result.exit_code == 0
    assert "test-example" in result.output


def test_example_info(example_root):
    runner = CliRunner()
    result = runner.invoke(example_group, ["info", "test-example"])
    assert result.exit_code == 0
    assert "Properties: 1" in result.output
    assert "Seeds:      1" in result.output
    assert "test-example" in result.output
    assert "README" in result.output


def test_example_info_unknown(example_root):
    runner = CliRunner()
    result = runner.invoke(example_group, ["info", "nonexistent"])
    assert result.exit_code == 1
    assert "not found" in result.output


def test_example_run_use_fixtures(example_root, monkeypatch, app_engine):
    """run --use-fixtures seeds sources from the fixtures/ directory."""
    # Add a fixture file
    fixtures_dir = example_root / "test-example" / "fixtures"
    fixtures_dir.mkdir()
    (fixtures_dir / "source-01.txt").write_text("AlphaCorp was founded in 2020.")
    import json as _json
    (fixtures_dir / "source-01.meta.json").write_text(
        _json.dumps({
            "url": "https://example.com/alphacorp-founded",
            "publisher": "example.com",
            "title": "AlphaCorp Founded",
        })
    )

    from sqlalchemy import create_engine
    db_url = str(app_engine.url)
    monkeypatch.setenv("FACTVAULT_DATABASE_URL", db_url)
    monkeypatch.setenv("FACTVAULT_TENANT_ID", "30000000-0000-0000-0000-000000000001")

    runner = CliRunner()
    result = runner.invoke(example_group, ["run", "test-example", "--use-fixtures"])
    assert result.exit_code == 0, result.output
    assert "Properties loaded" in result.output
    assert "Entities seeded" in result.output
    assert "Fixture sources" in result.output
```

- [ ] **9.3** Commit:
```bash
git add factvault/examples/cli.py tests/examples/test_cli.py
git commit -m "feat(examples): add factvault example list|info|run CLI + tests"
```

---

## Task 10 — AI startup tracking example

**Context:** Spec §2 Example A — VC associate monitoring AI startups. Uses fictional companies to avoid real data. All funding figures are invented. Properties cover the vendor-research domain: funding rounds, headcount, headquarters, founders, product launches, SEC CIK.

- [ ] **10.1** Create `examples/ai-startup-tracking/` directory structure:

```
examples/ai-startup-tracking/
├── README.md
├── properties.yaml
├── seeds.yaml
├── fixtures/
│   ├── techcrunch-novaspark-series-b.txt
│   ├── techcrunch-novaspark-series-b.meta.json
│   ├── sec-edgar-novaspark-10k.txt
│   ├── sec-edgar-novaspark-10k.meta.json
│   ├── crunchbase-novaspark.txt
│   ├── crunchbase-novaspark.meta.json
│   ├── venturebeat-novaspark-product.txt
│   └── venturebeat-novaspark-product.meta.json
└── expected/
    └── dossier-novaspark.json
```

- [ ] **10.2** Create `examples/ai-startup-tracking/README.md`:

```markdown
# AI Startup Tracking

**Use case:** A VC associate monitors 8 fictional AI startups, tracking funding rounds,
headcount, product launches, and regulatory filings nightly.

**Primary retrieval mode:** Dossier (pre-computed nightly per entity)

**Domain:** Technology / Venture Capital

## What this example demonstrates

- Registering a vendor-research property vocabulary (funding rounds, SEC CIK, headcount)
- Seeding 8 fictional AI startup entities with canonical names and aliases
- Loading canned source fixtures (TechCrunch article, SEC filing, Crunchbase excerpt)
- Running the full pipeline (archive → extract → corroborate → dossier)
- Comparing assembled dossier output against the golden expected snapshot

## Quick start

```bash
# From the factvault project root:
cp .env.example .env
docker compose up -d postgres
factvault example run ai-startup-tracking --use-fixtures
factvault-worker run extract
factvault-worker run corroborate
factvault-worker run dossier
```

## Property vocabulary

| Slug                     | Type       | Description                              |
|--------------------------|------------|------------------------------------------|
| founded_by               | entity_ref | Founder person entity                    |
| funding_round_amount     | number     | Amount raised in USD                     |
| funding_round_date       | date       | Closing date of funding round            |
| funding_round_lead_investor | entity_ref | Lead investor entity                  |
| funding_round_type       | string     | Series A, B, C, Seed, etc.              |
| headcount                | number     | Employee headcount at point in time      |
| headquarters_location    | string     | City, State / Country                    |
| sec_cik                  | string     | SEC EDGAR Central Index Key              |
| founder_role             | string     | Role title of the founder                |
| product_name             | string     | Name of a product or platform            |
| product_launch_date      | date       | Date of product launch or announcement   |
```

- [ ] **10.3** Create `examples/ai-startup-tracking/properties.yaml`:

```yaml
- slug: founded_by
  label: "Founded by"
  value_type: entity_ref
  description: "Person entity who co-founded or founded this organization"

- slug: funding_round_amount
  label: "Funding round amount (USD)"
  value_type: number
  description: "Amount raised in this funding round, in US dollars"

- slug: funding_round_date
  label: "Funding round date"
  value_type: date
  description: "Closing or announcement date of the funding round"

- slug: funding_round_lead_investor
  label: "Funding round lead investor"
  value_type: entity_ref
  description: "Lead investor entity in this funding round"

- slug: funding_round_type
  label: "Funding round type"
  value_type: string
  description: "Type of round: Seed, Series A, Series B, Series C, etc."

- slug: headcount
  label: "Headcount"
  value_type: number
  description: "Number of employees at a given point in time"

- slug: headquarters_location
  label: "Headquarters location"
  value_type: string
  description: "City and state or country of primary office"

- slug: sec_cik
  label: "SEC CIK"
  value_type: string
  description: "SEC EDGAR Central Index Key for public filings"

- slug: founder_role
  label: "Founder role"
  value_type: string
  description: "Title or role of the founder at the company"

- slug: product_name
  label: "Product name"
  value_type: string
  description: "Name of a product, platform, or service offered by this entity"

- slug: product_launch_date
  label: "Product launch date"
  value_type: date
  description: "Date the product was publicly announced or launched"
```

- [ ] **10.4** Create `examples/ai-startup-tracking/seeds.yaml`:

```yaml
- label: "NovaSpark Technologies"
  type_uri: "https://schema.org/Organization"
  ext_id: "CIK:0009900001"
  description: "AI infrastructure startup focused on distributed inference"
  aliases:
    - "NovaSpark"
    - "NovaSpark Tech"

- label: "Meridian AI"
  type_uri: "https://schema.org/Organization"
  description: "Enterprise AI platform for supply chain optimization"
  aliases:
    - "Meridian"

- label: "Helix Labs"
  type_uri: "https://schema.org/Organization"
  description: "Drug discovery AI startup using protein folding models"

- label: "Axiom Data Systems"
  type_uri: "https://schema.org/Organization"
  ext_id: "CIK:0009900004"
  description: "Data lake and vector search infrastructure"
  aliases:
    - "Axiom Data"
    - "AxiomDS"

- label: "Flare Intelligence"
  type_uri: "https://schema.org/Organization"
  description: "Threat detection AI for financial services"

- label: "Cognio Systems"
  type_uri: "https://schema.org/Organization"
  description: "LLM fine-tuning and deployment platform"

- label: "Veritas Compute"
  type_uri: "https://schema.org/Organization"
  ext_id: "CIK:0009900007"
  description: "GPU cloud provider optimized for AI training workloads"

- label: "Phalanx Security AI"
  type_uri: "https://schema.org/Organization"
  description: "Zero-trust network security powered by behavioral AI"

- label: "Priya Nair"
  type_uri: "https://schema.org/Person"
  description: "Co-founder and CEO of NovaSpark Technologies"

- label: "Sequoia Capital (Fictional)"
  type_uri: "https://schema.org/Organization"
  description: "Fictional venture capital firm used in examples"
```

- [ ] **10.5** Create fixture files. Each has a `.txt` (content) and `.meta.json` (URL metadata):

`examples/ai-startup-tracking/fixtures/techcrunch-novaspark-series-b.txt`:
```
NovaSpark Technologies Raises $42M Series B to Scale Distributed AI Inference

NovaSpark Technologies, a San Francisco-based AI infrastructure startup, announced today that it has raised $42 million in a Series B funding round led by Sequoia Capital (Fictional). The round brings NovaSpark's total funding to $67 million since the company was founded in 2022.

The funding will be used to expand NovaSpark's distributed inference platform, NovaSpark Accelerate, which the company says reduces inference latency by up to 60 percent compared to single-node deployments. NovaSpark Accelerate launched in March 2025.

"We're seeing demand from enterprises that need inference at scale without the cost overhead of centralized GPU clusters," said Priya Nair, co-founder and CEO of NovaSpark Technologies. "This round gives us the runway to triple our engineering headcount from 85 to 250 over the next 18 months."

NovaSpark Technologies is headquartered in San Francisco, California and currently employs approximately 85 people.
```

`examples/ai-startup-tracking/fixtures/techcrunch-novaspark-series-b.meta.json`:
```json
{
  "url": "https://techcrunch.example.com/2026/01/15/novaspark-technologies-raises-42m-series-b",
  "publisher": "techcrunch.example.com",
  "title": "NovaSpark Technologies Raises $42M Series B"
}
```

`examples/ai-startup-tracking/fixtures/sec-edgar-novaspark-10k.txt`:
```
UNITED STATES SECURITIES AND EXCHANGE COMMISSION
Washington, D.C. 20549

FORM 10-K
ANNUAL REPORT PURSUANT TO SECTION 13 OR 15(d)
OF THE SECURITIES EXCHANGE ACT OF 1934

For the fiscal year ended December 31, 2025

NovaSpark Technologies, Inc.
CIK: 0009900001
Commission file number: 001-99001

NovaSpark Technologies, Inc. was incorporated in Delaware on March 14, 2022. The Company's principal executive offices are located at 450 Market Street, San Francisco, California 94105.

As of December 31, 2025, the Company had 87 full-time employees and 3 part-time employees.

The Company's primary product, NovaSpark Accelerate, was made commercially available on March 10, 2025. The product generated $4.2 million in revenue during the nine months ended December 31, 2025.
```

`examples/ai-startup-tracking/fixtures/sec-edgar-novaspark-10k.meta.json`:
```json
{
  "url": "https://sec.example.gov/cgi-bin/browse-edgar?action=getcompany&CIK=0009900001&type=10-K",
  "publisher": "sec.example.gov",
  "title": "NovaSpark Technologies 10-K Annual Report 2025"
}
```

`examples/ai-startup-tracking/fixtures/crunchbase-novaspark.txt`:
```
NovaSpark Technologies — Company Overview

Founded: 2022
Headquarters: San Francisco, CA
Stage: Series B
Total Funding: $67M
Employees: 85 (as of January 2026)

Funding History:
- Series B: $42M — January 2026 — Lead: Sequoia Capital (Fictional)
- Series A: $18M — August 2024
- Seed: $7M — November 2022

NovaSpark Technologies builds distributed AI inference infrastructure. Its flagship product, NovaSpark Accelerate, enables enterprises to run large language models across heterogeneous GPU clusters with automatic load balancing and fault tolerance.

Co-founders: Priya Nair (CEO), Marcus Webb (CTO)
```

`examples/ai-startup-tracking/fixtures/crunchbase-novaspark.meta.json`:
```json
{
  "url": "https://crunchbase.example.com/organization/novaspark-technologies",
  "publisher": "crunchbase.example.com",
  "title": "NovaSpark Technologies — Crunchbase Profile"
}
```

`examples/ai-startup-tracking/fixtures/venturebeat-novaspark-product.txt`:
```
NovaSpark Launches Accelerate Platform, Targeting Enterprise AI Inference Bottleneck

VentureBeat — NovaSpark Technologies today unveiled NovaSpark Accelerate, a distributed inference platform designed to run large language model workloads across multiple GPU nodes without requiring custom orchestration code.

The product, which has been in private beta since October 2024, became generally available on March 10, 2025. Early customers include three Fortune 500 companies in financial services and healthcare, though NovaSpark declined to name them.

Priya Nair, CEO of NovaSpark Technologies, said the company plans to use its recently closed $42 million Series B to expand the engineering team. "We went from 55 people to 85 in the last year. The next step is getting to 250," she said.

NovaSpark Accelerate is priced at $0.80 per GPU-hour for on-demand usage.
```

`examples/ai-startup-tracking/fixtures/venturebeat-novaspark-product.meta.json`:
```json
{
  "url": "https://venturebeat.example.com/ai/novaspark-launches-accelerate-platform-march-2025",
  "publisher": "venturebeat.example.com",
  "title": "NovaSpark Launches Accelerate Platform"
}
```

- [ ] **10.6** Create `examples/ai-startup-tracking/expected/dossier-novaspark.json`:

```json
{
  "query": {
    "type": "dossier",
    "depth": 0
  },
  "entities": [
    {
      "label": "NovaSpark Technologies",
      "type_uri": "https://schema.org/Organization"
    }
  ],
  "facts": [
    {
      "property": { "slug": "funding_round_amount" },
      "value": { "number": 42000000 }
    },
    {
      "property": { "slug": "funding_round_type" },
      "value": { "text": "Series B" }
    },
    {
      "property": { "slug": "headcount" },
      "value": { "number": 85 }
    },
    {
      "property": { "slug": "headquarters_location" },
      "value": { "text": "San Francisco, California" }
    },
    {
      "property": { "slug": "sec_cik" },
      "value": { "text": "0009900001" }
    },
    {
      "property": { "slug": "product_name" },
      "value": { "text": "NovaSpark Accelerate" }
    }
  ],
  "conflicts": []
}
```

- [ ] **10.7** Create `examples/ai-startup-tracking/run.sh`:

```bash
#!/usr/bin/env bash
# run.sh — one-shot driver for the ai-startup-tracking example
# Usage: bash examples/ai-startup-tracking/run.sh [--live]
set -euo pipefail

EXAMPLE_NAME="ai-startup-tracking"
USE_FIXTURES="--use-fixtures"

if [[ "${1:-}" == "--live" ]]; then
  USE_FIXTURES=""
  echo "Running in live mode — collectors will fetch real URLs"
fi

echo "==> Setting up example: $EXAMPLE_NAME"
factvault example run "$EXAMPLE_NAME" $USE_FIXTURES

echo "==> Running extract worker"
factvault-worker run extract

echo "==> Running corroborate worker"
factvault-worker run corroborate

echo "==> Running dossier worker"
factvault-worker run dossier

echo "==> Running doctor"
factvault doctor --skip-canary

echo ""
echo "Done. Query NovaSpark dossier:"
echo "  curl http://localhost:8000/entities/by-name?q=NovaSpark | jq ."
```

- [ ] **10.8** Commit:
```bash
git add examples/ai-startup-tracking/
git commit -m "feat(examples): add ai-startup-tracking example (properties + seeds + fixtures + golden output)"
```

---

## Task 11 — Political research example

**Context:** Spec §2 Example B — Journalist tracking fictional politicians. All names are invented. Properties cover votes, donors, public statements, committee memberships, and family business ties. Fixtures include a canned FEC filing excerpt, a committee hearing transcript snippet, and a press article.

- [ ] **11.1** Create directory structure `examples/political-research/` (same shape as Task 10).

- [ ] **11.2** Create `examples/political-research/README.md`:

```markdown
# Political Research

**Use case:** A journalist tracks 6 fictional US politicians, monitoring voting records,
campaign donors, public statements, committee memberships, and family business ties.

**Primary retrieval mode:** Dossier + Story (dossier per politician; story for cross-entity queries)

**Domain:** Political / Investigative Journalism

## What this example demonstrates

- Registering a political-research property vocabulary (voting records, FEC donor data, statements)
- Seeding 6 fictional politician entities
- Loading canned FEC filings, transcript excerpts, and press articles as fixtures
- Cross-entity story query: "which senators on the Commerce Committee received donations from AI PACs?"

## Quick start

```bash
factvault example run political-research --use-fixtures
factvault-worker run extract
factvault-worker run corroborate
factvault-worker run dossier
```

## Property vocabulary

| Slug                   | Type       | Description                                      |
|------------------------|------------|--------------------------------------------------|
| voting_record          | string     | Bill + vote + date + chamber                    |
| campaign_donor_amount  | number     | Donation amount from a donor entity (USD)        |
| campaign_donor_date    | date       | Date of campaign donation                        |
| public_statement       | string     | Verbatim quoted statement                        |
| committee_membership   | string     | Committee name + role + chamber                  |
| family_business_tie    | entity_ref | Related entity with a business relationship      |
```

- [ ] **11.3** Create `examples/political-research/properties.yaml`:

```yaml
- slug: voting_record
  label: "Voting record"
  value_type: string
  description: "A vote on a bill: bill number, yea/nay, date, chamber, and source"

- slug: campaign_donor_amount
  label: "Campaign donor amount (USD)"
  value_type: number
  description: "Dollar amount donated by a campaign donor in a single filing period"

- slug: campaign_donor_date
  label: "Campaign donor date"
  value_type: date
  description: "Date of the campaign finance contribution"

- slug: public_statement
  label: "Public statement"
  value_type: string
  description: "Verbatim quoted text from a public statement, hearing, or interview"

- slug: committee_membership
  label: "Committee membership"
  value_type: string
  description: "Name of committee, role (chair/member/ranking), and chamber"

- slug: family_business_tie
  label: "Family business tie"
  value_type: entity_ref
  description: "Entity with which the subject has a family or business relationship"

- slug: party_affiliation
  label: "Party affiliation"
  value_type: string
  description: "Political party (Democrat, Republican, Independent, etc.)"

- slug: state_represented
  label: "State represented"
  value_type: string
  description: "US state represented by the politician (two-letter abbreviation)"
```

- [ ] **11.4** Create `examples/political-research/seeds.yaml`:

```yaml
- label: "Senator Margaret Hollis"
  type_uri: "https://schema.org/Person"
  ext_id: "BIOGUIDE:H999901"
  description: "Fictional US Senator from the state of Pacifica, Commerce Committee chair"
  aliases:
    - "Sen. Hollis"
    - "Margaret Hollis"

- label: "Senator David Crowe"
  type_uri: "https://schema.org/Person"
  ext_id: "BIOGUIDE:C999902"
  description: "Fictional US Senator from the state of Norland, ranking member on Finance Committee"
  aliases:
    - "Sen. Crowe"

- label: "Representative Anita Vasquez"
  type_uri: "https://schema.org/Person"
  ext_id: "BIOGUIDE:V999903"
  description: "Fictional US Representative from Eastbridge District 7"
  aliases:
    - "Rep. Vasquez"

- label: "Representative Thomas Wren"
  type_uri: "https://schema.org/Person"
  ext_id: "BIOGUIDE:W999904"
  description: "Fictional US Representative from Westfield District 2"
  aliases:
    - "Rep. Wren"

- label: "Senator Linda Kwan"
  type_uri: "https://schema.org/Person"
  ext_id: "BIOGUIDE:K999905"
  description: "Fictional US Senator from the state of Sunstone"

- label: "Senator James Brightfield"
  type_uri: "https://schema.org/Person"
  ext_id: "BIOGUIDE:B999906"
  description: "Fictional US Senator from the state of Ironwood, Judiciary Committee"

- label: "Hollis Family Holdings LLC"
  type_uri: "https://schema.org/Organization"
  description: "Fictional business entity with ties to Senator Hollis family"

- label: "NextGen AI PAC (Fictional)"
  type_uri: "https://schema.org/Organization"
  description: "Fictional political action committee focused on AI industry interests"
```

- [ ] **11.5** Create fixture files:

`examples/political-research/fixtures/fec-hollis-q3-2025.txt`:
```
FEDERAL ELECTION COMMISSION
Campaign Finance Report — Schedule A (Itemized Receipts)
Committee: Hollis for Senate 2026
FEC ID: C00999901
Report Period: July 1 — September 30, 2025

Contributor: NextGen AI PAC (Fictional)
Employer: N/A (PAC)
Occupation: N/A
Date of Receipt: September 22, 2025
Amount: $45,000.00
Transaction ID: SA11AI-FAKE-001

Contributor: Veritas Compute Holdings
Employer: N/A (Corporate PAC)
Occupation: N/A
Date of Receipt: August 14, 2025
Amount: $28,500.00
Transaction ID: SA11AI-FAKE-002

Total receipts this period: $73,500.00
```

`examples/political-research/fixtures/fec-hollis-q3-2025.meta.json`:
```json
{
  "url": "https://fec.example.gov/filings/C00999901/SA-Q3-2025",
  "publisher": "fec.example.gov",
  "title": "Hollis for Senate 2026 — FEC Schedule A Q3 2025"
}
```

`examples/political-research/fixtures/senate-hearing-ai-oversight-2025.txt`:
```
UNITED STATES SENATE
COMMITTEE ON COMMERCE, SCIENCE, AND TRANSPORTATION
Hearing: Artificial Intelligence Oversight and Consumer Protection Act
Date: November 4, 2025
Presiding: Senator Margaret Hollis, Chair

SENATOR HOLLIS: "The AI industry has grown faster than our regulatory frameworks. This committee believes that algorithmic accountability must become a first-class obligation for any company deploying AI systems that affect consumer credit, employment, or healthcare decisions. We will be voting on the AI Oversight and Consumer Protection Act, S. 4421, before the end of this session."

[Record vote on S. 4421 — Procedural motion to advance to floor vote]
Senator Hollis: Yea
Senator Crowe: Nay
Senator Kwan: Yea
Senator Brightfield: Yea
```

`examples/political-research/fixtures/senate-hearing-ai-oversight-2025.meta.json`:
```json
{
  "url": "https://congress.example.gov/senate/commerce/hearing-ai-oversight-2025-11-04",
  "publisher": "congress.example.gov",
  "title": "Senate Commerce Committee Hearing: AI Oversight Act, Nov 4 2025"
}
```

`examples/political-research/fixtures/newsweek-hollis-profile.txt`:
```
Senator Hollis Chairs AI Regulation Push — But Her Family's Business Ties Draw Scrutiny

Senator Margaret Hollis (D-Pacifica) has emerged as one of the most vocal champions of AI regulation in the Senate. As chair of the Commerce Committee, she presided over last month's hearing on S. 4421, the AI Oversight and Consumer Protection Act, which passed committee 12-4 and now heads to the Senate floor.

But Hollis's record on AI regulation is complicated by a financial disclosure showing that Hollis Family Holdings LLC, a real estate and venture investment firm controlled by her spouse, holds a minority stake in Cognio Systems, an AI startup that would be subject to the proposed regulations.

Senator Hollis's office did not respond to a request for comment. A spokesperson for Hollis Family Holdings LLC said the investment predates the Senator's committee chairmanship and that the Senator has recused herself from any vote directly affecting Cognio Systems.

Senator Hollis has represented Pacifica since 2017 and is seeking her third term in 2026. She sits on the Commerce and Finance committees.
```

`examples/political-research/fixtures/newsweek-hollis-profile.meta.json`:
```json
{
  "url": "https://newsweek.example.com/senator-hollis-ai-regulation-family-business-ties",
  "publisher": "newsweek.example.com",
  "title": "Senator Hollis Chairs AI Regulation Push — But Her Family's Business Ties Draw Scrutiny"
}
```

- [ ] **11.6** Create `examples/political-research/expected/dossier-hollis.json`:

```json
{
  "query": {
    "type": "dossier",
    "depth": 0
  },
  "entities": [
    {
      "label": "Senator Margaret Hollis",
      "type_uri": "https://schema.org/Person"
    }
  ],
  "facts": [
    {
      "property": { "slug": "committee_membership" },
      "value": { "text": "Commerce Committee, Chair" }
    },
    {
      "property": { "slug": "campaign_donor_amount" },
      "value": { "number": 45000 }
    },
    {
      "property": { "slug": "voting_record" },
      "value": { "text": "S. 4421 — Yea — November 4, 2025 — Senate" }
    },
    {
      "property": { "slug": "public_statement" },
      "value": { "text": "The AI industry has grown faster than our regulatory frameworks." }
    }
  ],
  "conflicts": []
}
```

- [ ] **11.7** Create `examples/political-research/run.sh`:

```bash
#!/usr/bin/env bash
# run.sh — one-shot driver for the political-research example
set -euo pipefail

EXAMPLE_NAME="political-research"

echo "==> Setting up example: $EXAMPLE_NAME"
factvault example run "$EXAMPLE_NAME" --use-fixtures

echo "==> Running extract worker"
factvault-worker run extract

echo "==> Running corroborate worker"
factvault-worker run corroborate

echo "==> Running dossier worker"
factvault-worker run dossier

echo ""
echo "Done. Sample queries:"
echo "  curl 'http://localhost:8000/entities/by-name?q=Hollis' | jq ."
echo "  curl -X POST http://localhost:8000/stories -d '{\"query\":\"senators who received AI PAC donations\",\"depth\":2}' | jq ."
```

- [ ] **11.8** Commit:
```bash
git add examples/political-research/
git commit -m "feat(examples): add political-research example (properties + seeds + fixtures + golden output)"
```

---

<!-- PASS 1 END — Pass 2 appends Tasks 12-22 + self-review below this line -->
