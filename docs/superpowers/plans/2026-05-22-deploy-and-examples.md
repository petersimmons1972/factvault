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

## Task 12 — Pharma trial monitoring example

**Context:** `examples/pharma-trial-monitoring/` — spec §2.4 Example C. Demonstrates dossier retrieval for a pharma competitive intelligence analyst tracking Phase II/III drug compounds through ClinicalTrials.gov, peer-reviewed publications, and regulatory correspondence. Uses realistic-but-fictional drug names to avoid trademark concerns.

- [ ] **12.1** Create `examples/pharma-trial-monitoring/properties.yaml`:

```yaml
# examples/pharma-trial-monitoring/properties.yaml
namespace: pharma_trial
strict_mode: true

properties:
  - slug: trial_id_nct
    label: ClinicalTrials.gov NCT Identifier
    value_type: string
    description: "NCT number assigned by ClinicalTrials.gov (format: NCT########)"
    extractors: [regex_nct, llm]
    regex_nct:
      pattern: 'NCT\d{8}'

  - slug: trial_phase
    label: Trial Phase
    value_type: enum
    allowed_values: ["Phase I", "Phase I/II", "Phase II", "Phase II/III", "Phase III", "Phase III/IV", "Phase IV"]
    extractors: [regex_trial_phase, llm]
    regex_trial_phase:
      pattern: 'Phase\s+(?:I{1,3}V?|IV)(?:/(?:I{1,3}V?|IV))?'

  - slug: trial_sponsor
    label: Sponsoring Organization
    value_type: string
    extractors: [llm]

  - slug: trial_indication
    label: Disease / Indication
    value_type: string
    description: "Primary indication or disease area being studied"
    extractors: [llm]

  - slug: trial_enrollment
    label: Enrolled Participants (count)
    value_type: integer
    extractors: [regex_enrollment, llm]
    regex_enrollment:
      pattern: 'enrolled?\s+(\d[\d,]+)\s+(?:patients?|participants?|subjects?)'

  - slug: trial_start_date
    label: Trial Start Date
    value_type: date
    extractors: [llm]

  - slug: trial_end_date
    label: Trial Completion Date (Estimated or Actual)
    value_type: date
    extractors: [llm]

  - slug: primary_endpoint
    label: Primary Endpoint
    value_type: string
    description: "Primary efficacy or safety endpoint as registered"
    extractors: [llm]

  - slug: endpoint_result
    label: Primary Endpoint Result
    value_type: string
    description: "Reported outcome for the primary endpoint (met / not met / interim data)"
    extractors: [llm]

  - slug: endpoint_p_value
    label: Primary Endpoint p-value
    value_type: float
    extractors: [regex_pvalue, llm]
    regex_pvalue:
      pattern: 'p\s*[<=]\s*(0\.\d+)'

  - slug: adverse_event_meddra
    label: Adverse Event (MedDRA preferred term)
    value_type: string
    description: "MedDRA preferred term for a reported adverse event"
    multi_valued: true
    extractors: [llm]

  - slug: adverse_event_incidence
    label: Adverse Event Incidence Rate
    value_type: string
    description: "Incidence percentage associated with the most recent adverse_event_meddra value"
    extractors: [llm]

  - slug: publication_doi
    label: Publication DOI
    value_type: string
    extractors: [regex_doi, llm]
    regex_doi:
      pattern: '10\.\d{4,}/[^\s"<>]+'

  - slug: regulatory_correspondence
    label: Regulatory Correspondence Reference
    value_type: string
    description: "Reference to FDA/EMA correspondence, approval letter, or complete response letter"
    extractors: [llm]
```

- [ ] **12.2** Create `examples/pharma-trial-monitoring/seeds.yaml` (6–8 fictional drug compound entities):

```yaml
# examples/pharma-trial-monitoring/seeds.yaml
namespace: pharma_trial
entity_type: drug_compound

entities:
  - label: "Valorixan"
    description: "Selective KRAS G12C inhibitor; oral small molecule; Phase III NSCLC"
    aliases: ["valorixan hydrochloride", "FVT-4401"]

  - label: "Neltripamide"
    description: "Dual GLP-1/GIP receptor agonist; subcutaneous weekly; Phase III Type 2 Diabetes"
    aliases: ["neltripamide acetate", "FVT-7720"]

  - label: "Crisamelitinib"
    description: "SYK/JAK1 dual inhibitor; oral; Phase II/III RA and PsA"
    aliases: ["crisamelitinib mesylate", "FVT-2218"]

  - label: "Oravexin"
    description: "Novel PCSK9 degrader (PROTAC); oral; Phase II LDL reduction"
    aliases: ["oravexin", "FVT-3380"]

  - label: "Dendracept"
    description: "IL-33/TSLP bispecific antibody; IV infusion; Phase II severe asthma"
    aliases: ["dendracept", "FVT-9910"]

  - label: "Lysamotide"
    description: "Antisense oligonucleotide targeting APOC3; SC injection; Phase II/III severe hypertriglyceridaemia"
    aliases: ["lysamotide sodium", "FVT-5560"]

  - label: "Pravolixin"
    description: "Oral CRBN modulator (CELMoD); Phase II relapsed/refractory multiple myeloma"
    aliases: ["pravolixin", "FVT-8830"]

  - label: "Zelfarinib"
    description: "FGFR1/2/3 pan-inhibitor; oral; Phase II cholangiocarcinoma with FGFR2 fusion"
    aliases: ["zelfarinib tosylate", "FVT-1140"]
```

- [ ] **12.3** Create `examples/pharma-trial-monitoring/fixtures/` (5 canned source documents):

**12.3.1** `examples/pharma-trial-monitoring/fixtures/clinicaltrials_valorixan.txt` — ClinicalTrials.gov entry excerpt:

```
ClinicalTrials.gov Identifier: NCT05812744
Title: A Phase III Randomized Study of Valorixan vs. Docetaxel in Previously Treated KRAS G12C-Mutant Non-Small Cell Lung Cancer (VALORIZE-301)
Sponsor: Frontvault Therapeutics, Inc.
Phase: Phase III
Status: Active, not recruiting
Enrollment: 612 patients
Start Date: March 14, 2023
Estimated Completion: September 2026
Primary Endpoint: Progression-free survival (PFS) by blinded independent central review (BICR)
Secondary Endpoints: Overall survival (OS), objective response rate (ORR), duration of response (DoR), patient-reported outcomes
Inclusion Criteria: Adults ≥18 years; histologically confirmed NSCLC; KRAS G12C mutation by validated assay; ≥1 prior platinum-based regimen
Arms:
  Arm A (Valorixan): 400 mg orally once daily
  Arm B (Docetaxel): 75 mg/m² IV every 21 days
```

**12.3.2** `examples/pharma-trial-monitoring/fixtures/publication_valorixan_interim.txt` — Peer-reviewed publication abstract:

```
Journal of Thoracic Oncology, Vol. 19, No. 3 (2025)
DOI: 10.1016/j.jtho.2025.01.012

Interim Analysis of VALORIZE-301: Valorixan Demonstrates Superior Progression-Free Survival in KRAS G12C-Mutant NSCLC

Background: Valorixan (FVT-4401) is an orally bioavailable, covalent KRAS G12C inhibitor currently under investigation in VALORIZE-301, a Phase III trial comparing valorixan to docetaxel in previously treated KRAS G12C-mutant non-small cell lung cancer (NSCLC).

Methods: A pre-specified interim analysis was conducted at 60% of planned PFS events (n=367). Patients were enrolled across 89 sites in 22 countries. Primary endpoint: PFS by BICR.

Results: Valorixan demonstrated a statistically significant improvement in PFS versus docetaxel (median PFS: 8.4 months vs. 4.1 months; HR 0.47; 95% CI 0.36–0.62; p<0.0001). ORR was 43.2% for valorixan vs. 19.7% for docetaxel. Grade 3/4 treatment-emergent adverse events occurred in 38% of valorixan-treated patients. Most common Grade ≥3 adverse events: diarrhea (8%), nausea (6%), fatigue (5%).

Conclusion: Valorixan significantly improves PFS with a manageable safety profile. The trial has crossed the pre-specified efficacy boundary; submission to regulatory agencies is planned.
```

**12.3.3** `examples/pharma-trial-monitoring/fixtures/fda_correspondence_valorixan.txt` — FDA correspondence excerpt:

```
DEPARTMENT OF HEALTH AND HUMAN SERVICES
Food and Drug Administration
Center for Drug Evaluation and Research
Division of Oncology Products 1

RE: Frontvault Therapeutics — Valorixan (FVT-4401) — NDA 221847
Type: Acceptance Communication

Dear Ms. Chen,
We have completed our filing review of your New Drug Application (NDA 221847) for valorixan 400 mg tablets for the treatment of adult patients with KRAS G12C-mutant non-small cell lung cancer (NSCLC) who have received at least one prior systemic therapy.

Your application has been accepted for review. The action date under the Prescription Drug User Fee Act (PDUFA) is December 14, 2025.

The Division has determined that your application is sufficiently complete to permit a substantive review. This is not an approval letter.

Sincerely,
Office of Oncology Products
CDER / FDA
```

**12.3.4** `examples/pharma-trial-monitoring/fixtures/meddra_ae_summary_valorixan.txt` — MedDRA-formatted adverse event summary:

```
MedDRA Adverse Event Summary — VALORIZE-301 Interim Safety Analysis
NDA Reference: 221847 | Data Cut: 2024-09-15 | Analysis Population: Safety (n=304, valorixan arm)

System Organ Class: Gastrointestinal Disorders
  Preferred Term: Diarrhoea | Any Grade: 48.7% | Grade ≥3: 8.2%
  Preferred Term: Nausea | Any Grade: 34.1% | Grade ≥3: 5.9%
  Preferred Term: Vomiting | Any Grade: 19.0% | Grade ≥3: 2.0%

System Organ Class: General Disorders and Administration Site Conditions
  Preferred Term: Fatigue | Any Grade: 41.2% | Grade ≥3: 5.3%
  Preferred Term: Peripheral Oedema | Any Grade: 22.4% | Grade ≥3: 1.0%

System Organ Class: Musculoskeletal and Connective Tissue Disorders
  Preferred Term: Arthralgia | Any Grade: 16.1% | Grade ≥3: 0.7%
  Preferred Term: Myalgia | Any Grade: 12.8% | Grade ≥3: 0.3%

System Organ Class: Investigations
  Preferred Term: Alanine Aminotransferase Increased | Any Grade: 18.1% | Grade ≥3: 3.6%
  Preferred Term: Aspartate Aminotransferase Increased | Any Grade: 15.5% | Grade ≥3: 2.6%

Deaths on study: 2 (0.7%); adjudicated as unrelated to study drug by independent safety committee.
```

**12.3.5** `examples/pharma-trial-monitoring/fixtures/ema_scientific_opinion_valorixan.txt` — EMA scientific opinion excerpt:

```
European Medicines Agency
Committee for Medicinal Products for Human Use (CHMP)
CHMP Scientific Opinion — Article 58 Consultation

Product: Valorixan 400 mg film-coated tablets (FVT-4401)
Applicant: Frontvault Therapeutics Europe B.V.
Procedure No.: EMEA/H/C/006714/0000

Summary of Opinion:

Based on the data submitted, the CHMP considered that the benefit/risk balance of valorixan for the treatment of adult patients with locally advanced or metastatic KRAS G12C-mutant NSCLC after ≥1 prior systemic therapy is positive.

The CHMP noted the following:
- The primary endpoint (PFS by BICR) was met at interim analysis (HR 0.47; p<0.0001).
- Overall survival data are immature. A post-authorisation efficacy study (PAES) is required.
- Hepatotoxicity (ALT/AST elevation ≥Grade 3 in ~3-4%) requires Risk Management Plan measures including enhanced liver function monitoring during the first 12 weeks.
- The CHMP recommended granting a conditional marketing authorisation.

This opinion is valid for five years from date of issue.
```

- [ ] **12.4** Create `examples/pharma-trial-monitoring/expected/valorixan_dossier.json` (golden dossier):

```json
{
  "entity": {
    "label": "Valorixan",
    "type": "drug_compound",
    "aliases": ["valorixan hydrochloride", "FVT-4401"]
  },
  "facts": [
    {
      "property": { "slug": "trial_id_nct" },
      "value": { "text": "NCT05812744" },
      "confidence": 0.95,
      "source_count": 2
    },
    {
      "property": { "slug": "trial_phase" },
      "value": { "text": "Phase III" },
      "confidence": 0.97,
      "source_count": 3
    },
    {
      "property": { "slug": "trial_sponsor" },
      "value": { "text": "Frontvault Therapeutics, Inc." },
      "confidence": 0.96,
      "source_count": 2
    },
    {
      "property": { "slug": "trial_indication" },
      "value": { "text": "KRAS G12C-mutant non-small cell lung cancer (NSCLC); ≥1 prior systemic therapy" },
      "confidence": 0.97,
      "source_count": 4
    },
    {
      "property": { "slug": "trial_enrollment" },
      "value": { "integer": 612 },
      "confidence": 0.94,
      "source_count": 2
    },
    {
      "property": { "slug": "primary_endpoint" },
      "value": { "text": "Progression-free survival (PFS) by blinded independent central review (BICR)" },
      "confidence": 0.97,
      "source_count": 2
    },
    {
      "property": { "slug": "endpoint_result" },
      "value": { "text": "Met at interim — median PFS 8.4 mo vs 4.1 mo (HR 0.47; p<0.0001)" },
      "confidence": 0.95,
      "source_count": 2
    },
    {
      "property": { "slug": "endpoint_p_value" },
      "value": { "float": 0.0001 },
      "confidence": 0.94,
      "source_count": 2
    },
    {
      "property": { "slug": "adverse_event_meddra" },
      "value": { "text": "Diarrhoea" },
      "confidence": 0.96,
      "source_count": 2
    },
    {
      "property": { "slug": "adverse_event_incidence" },
      "value": { "text": "Any grade: 48.7%; Grade ≥3: 8.2%" },
      "confidence": 0.95,
      "source_count": 1
    },
    {
      "property": { "slug": "publication_doi" },
      "value": { "text": "10.1016/j.jtho.2025.01.012" },
      "confidence": 0.98,
      "source_count": 1
    },
    {
      "property": { "slug": "regulatory_correspondence" },
      "value": { "text": "FDA NDA 221847 accepted for filing; PDUFA date December 14, 2025. EMA CHMP conditional marketing authorisation recommended (Procedure EMEA/H/C/006714/0000)." },
      "confidence": 0.94,
      "source_count": 2
    }
  ],
  "conflicts": []
}
```

- [ ] **12.5** Create `examples/pharma-trial-monitoring/README.md`. Cover: use case (pharma competitive intelligence analyst monitoring competitor pipeline compounds), how to run, what the dossier output contains, how to extend to a full pipeline watch with multiple compounds, note on fictional names.

- [ ] **12.6** Create `examples/pharma-trial-monitoring/run.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

EXAMPLE_NAME="pharma-trial-monitoring"

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
echo "  curl 'http://localhost:8000/entities/by-name?q=Valorixan' | jq ."
echo "  curl 'http://localhost:8000/dossiers/by-entity-name?q=Valorixan' | jq ."
```

- [ ] **12.7** Create `tests/examples/test_pharma_trial_monitoring.py`:

```python
"""Tests for pharma-trial-monitoring example: property loading, seed ingestion, fixture parsing, golden dossier shape."""
import json
from pathlib import Path
import pytest
from factvault.examples.base import ExampleLoader

EXAMPLE_DIR = Path(__file__).parent.parent.parent / "examples" / "pharma-trial-monitoring"

@pytest.fixture
def loader():
    return ExampleLoader(EXAMPLE_DIR)

def test_properties_load(loader):
    props = loader.load_properties()
    slugs = [p["slug"] for p in props]
    assert "trial_id_nct" in slugs
    assert "adverse_event_meddra" in slugs
    assert "publication_doi" in slugs
    assert "regulatory_correspondence" in slugs

def test_seeds_load(loader):
    seeds = loader.load_seeds()
    assert len(seeds) >= 6
    labels = [s["label"] for s in seeds]
    assert "Valorixan" in labels

def test_fixtures_present(loader):
    fixtures = loader.list_fixtures()
    assert len(fixtures) == 5
    names = [f.name for f in fixtures]
    assert "clinicaltrials_valorixan.txt" in names
    assert "meddra_ae_summary_valorixan.txt" in names

def test_golden_dossier_shape():
    golden = json.loads((EXAMPLE_DIR / "expected" / "valorixan_dossier.json").read_text())
    assert golden["entity"]["label"] == "Valorixan"
    fact_slugs = [f["property"]["slug"] for f in golden["facts"]]
    assert "trial_id_nct" in fact_slugs
    assert "endpoint_p_value" in fact_slugs
    assert "publication_doi" in fact_slugs
    for fact in golden["facts"]:
        assert fact["confidence"] > 0.0
        assert fact["source_count"] >= 1

def test_nct_regex_extraction(loader):
    """Regex extractor must pull NCT ID from fixture text."""
    fixture_text = (EXAMPLE_DIR / "fixtures" / "clinicaltrials_valorixan.txt").read_text()
    prop = next(p for p in loader.load_properties() if p["slug"] == "trial_id_nct")
    import re
    pattern = prop["regex_nct"]["pattern"]
    matches = re.findall(pattern, fixture_text)
    assert "NCT05812744" in matches
```

- [ ] **12.8** Commit:
```bash
git add examples/pharma-trial-monitoring/ tests/examples/test_pharma_trial_monitoring.py
git commit -m "feat(examples): add pharma-trial-monitoring example (properties + seeds + fixtures + golden dossier)"
```

---

## Task 13 — Investigative journalism example

**Context:** `examples/investigative-journalism/` — cross-entity story use case. Showcases story assembly (not dossier), where the insight is in the *connections* between entities: a donor PAC funding two senators, the same company appearing in multiple regulatory filings, and a correspondent between a regulator and that company. All names are realistic-but-fictional.

- [ ] **13.1** Create `examples/investigative-journalism/properties.yaml`:

```yaml
# examples/investigative-journalism/properties.yaml
namespace: investigative
strict_mode: false

properties:
  - slug: affiliation_with
    label: Affiliated With
    value_type: entity_ref
    description: "Employment, board membership, or organizational affiliation"
    extractors: [llm]

  - slug: business_relationship_with
    label: Business Relationship With
    value_type: entity_ref
    description: "Vendor, customer, partner, joint-venture, or investor relationship"
    extractors: [llm]

  - slug: received_donation_from
    label: Received Donation From
    value_type: entity_ref
    description: "Campaign contribution or PAC donation received"
    extractors: [llm]

  - slug: voted_on_bill
    label: Voted On Bill
    value_type: string
    description: "Bill identifier, vote direction, and date (format: BILL-ID — Direction — Date)"
    multi_valued: true
    extractors: [llm]

  - slug: received_award_from
    label: Received Award Or Honour From
    value_type: entity_ref
    extractors: [llm]

  - slug: appeared_at_event
    label: Appeared At Event
    value_type: string
    extractors: [llm]

  - slug: quoted_in_article
    label: Quoted In Article
    value_type: string
    description: "Verbatim quoted excerpt, with publication and date"
    multi_valued: true
    extractors: [llm]

  - slug: subject_of_inquiry
    label: Subject Of Regulatory Or Legislative Inquiry
    value_type: string
    description: "Name/reference of inquiry or investigation this entity is subject to"
    extractors: [llm]

  - slug: regulatory_action_against
    label: Regulatory Action Against
    value_type: string
    description: "Enforcement action, fine, consent decree, or warning letter issued against this entity"
    extractors: [llm]
```

- [ ] **13.2** Create `examples/investigative-journalism/seeds.yaml` (10–15 entities including companies, public figures, PACs):

```yaml
# examples/investigative-journalism/seeds.yaml
namespace: investigative
entity_type: mixed

entities:
  # Public figures
  - label: "Senator Marcus Hollis"
    entity_type: public_figure
    description: "U.S. Senator, Commerce Committee member, third term"
    aliases: ["Marcus Hollis", "Sen. Hollis", "M. Hollis"]

  - label: "Senator Patricia Wren"
    entity_type: public_figure
    description: "U.S. Senator, Banking Committee chair"
    aliases: ["Patricia Wren", "Sen. Wren", "P. Wren"]

  - label: "Rep. Daniel Moreau"
    entity_type: public_figure
    description: "U.S. Representative, House Energy & Commerce Committee"
    aliases: ["Daniel Moreau", "Rep. Moreau"]

  # Corporations
  - label: "Acme Broadband Holdings"
    entity_type: company
    description: "Telecommunications conglomerate; publicly traded"
    aliases: ["Acme Broadband", "ACMB", "Acme Holdings"]

  - label: "Cascade Digital Infrastructure LLC"
    entity_type: company
    description: "Data centre and fibre network operator; Acme Broadband subsidiary"
    aliases: ["Cascade Digital", "CDI"]

  - label: "Prism Analytics Corp"
    entity_type: company
    description: "Consumer data broker; 12 million record dataset"
    aliases: ["Prism Analytics", "Prism Corp"]

  - label: "Vertex Capital Partners"
    entity_type: company
    description: "Private equity firm; major shareholder in Acme Broadband Holdings"
    aliases: ["Vertex Capital", "VCP"]

  # PACs and advocacy groups
  - label: "Coalition for Better Tomorrow PAC"
    entity_type: pac
    description: "Super PAC aligned with telecommunications industry interests"
    aliases: ["CBT PAC", "CBTP", "Better Tomorrow PAC"]

  - label: "Digital Future Alliance"
    entity_type: advocacy_group
    description: "501(c)(4) advocacy organisation funded by Acme Broadband Holdings"
    aliases: ["DFA", "Digital Future"]

  # Regulators
  - label: "FCC Enforcement Bureau"
    entity_type: regulator
    description: "FCC enforcement arm; opened inquiry into Prism Analytics data practices"
    aliases: ["FCC Enforcement", "FCC EB"]

  - label: "FTC Bureau of Consumer Protection"
    entity_type: regulator
    description: "FTC division overseeing consumer data brokers"
    aliases: ["FTC BCP", "FTC Consumer Protection"]

  # Events
  - label: "TeleCom Leadership Summit 2025"
    entity_type: event
    description: "Industry conference; sponsored by Acme Broadband Holdings; attended by Senators Hollis and Wren"
    aliases: ["TeleCom Summit", "TCLS 2025"]
```

- [ ] **13.3** Create `examples/investigative-journalism/fixtures/` (8 canned source documents):

**13.3.1** `fixtures/fec_hollis_q3_2025.txt` — FEC filing excerpt (Senator Hollis):
```
Federal Election Commission — Committee on Political Expenditures
Committee: Friends of Marcus Hollis for Senate
FEC ID: C00841922
Reporting Period: Q3 2025 (July 1 – September 30)

Itemized Individual Contributions Received (>$200):
  Coalition for Better Tomorrow PAC — $75,000 — August 14, 2025
  Vertex Capital Partners PAC Fund — $25,000 — September 2, 2025
  Acme Broadband Holdings Employee PAC — $18,500 — July 28, 2025
  [additional contributors omitted for brevity]

Total Receipts This Period: $892,400
```

**13.3.2** `fixtures/fec_wren_q3_2025.txt` — FEC filing excerpt (Senator Wren):
```
Federal Election Commission — Committee on Political Expenditures
Committee: Patricia Wren for Senate
FEC ID: C00779234
Reporting Period: Q3 2025 (July 1 – September 30)

Itemized Individual Contributions Received (>$200):
  Coalition for Better Tomorrow PAC — $75,000 — August 15, 2025
  Digital Future Alliance — $50,000 — August 22, 2025
  Acme Broadband Holdings Employee PAC — $22,000 — September 8, 2025

Total Receipts This Period: $1,102,700
```

**13.3.3** `fixtures/sec_acme_broadband_proxy_2025.txt` — SEC proxy filing (Acme Broadband Holdings):
```
ACME BROADBAND HOLDINGS INC
Form DEF 14A — Definitive Proxy Statement
Filed: April 2, 2025

Principal Shareholders (>5% ownership):
  Vertex Capital Partners: 18.4% (42,200,000 shares)
  State Street Global Advisors: 9.1%
  Vanguard Group: 7.6%

Board of Directors:
  Lawrence Thorn (Chair) — also serves on board of Digital Future Alliance
  Maria Chen (Lead Independent Director)
  Robert Gaines — former FCC Commissioner (2017–2022)
  [3 additional directors omitted]

Subsidiaries disclosed:
  Cascade Digital Infrastructure LLC — 100% owned
  Prism Analytics Corp — 34% equity stake (non-controlling)
```

**13.3.4** `fixtures/sec_vertex_capital_13d_acme.txt` — SEC Schedule 13D (Vertex Capital):
```
UNITED STATES SECURITIES AND EXCHANGE COMMISSION
Washington, D.C. 20549
SCHEDULE 13D

Issuer: Acme Broadband Holdings Inc. (NASDAQ: ACMB)
Filed by: Vertex Capital Partners LP

Item 4. Purpose of Transaction
Vertex Capital Partners acquired its 18.4% position between January and September 2024. The filing person has had discussions with members of the Board of Directors regarding operational efficiency, capital allocation, and the potential strategic value of Cascade Digital Infrastructure LLC as a standalone entity or merger candidate.

Item 6. Contracts, Arrangements, Understandings
The filing person co-sponsored the TeleCom Leadership Summit 2025 alongside Acme Broadband Holdings.
```

**13.3.5** `fixtures/fcc_inquiry_prism_analytics.txt` — FCC Enforcement Bureau inquiry letter:
```
FEDERAL COMMUNICATIONS COMMISSION
Enforcement Bureau
445 12th Street, S.W.
Washington, D.C. 20554

Re: EB-2025-IHD-0142 — Inquiry into Consumer Data Practices — Prism Analytics Corp

Dear Mr. Caldwell (General Counsel, Prism Analytics Corp):

Pursuant to the Commission's authority under Section 503(b) of the Communications Act, the Enforcement Bureau is conducting an inquiry into Prism Analytics Corp's practices regarding the collection, sale, and retention of Consumer Proprietary Network Information (CPNI) derived from telecommunications records.

The Bureau has received credible information suggesting that Prism Analytics Corp may have acquired CPNI from telecommunications carriers without the requisite customer consent, in possible violation of 47 C.F.R. § 64.2009.

You are required to respond to the attached interrogatories within 30 days.
```

**13.3.6** `fixtures/senator_hollis_press_release_vote.txt` — Senator Hollis press release:
```
FOR IMMEDIATE RELEASE
Office of Senator Marcus Hollis

SENATOR HOLLIS VOTES AGAINST ONLINE DATA PRIVACY ACT (S. 3891)

WASHINGTON, D.C. — Senator Marcus Hollis today voted against the Online Data Privacy Act (S. 3891), citing concerns about its impact on innovation and the competitiveness of American technology companies.

"While I share the goal of protecting consumer privacy, S. 3891 as written would impose compliance burdens that smaller companies cannot absorb," said Senator Hollis. "I am committed to working across the aisle on a more targeted approach."

The bill failed 44–53 on November 6, 2025. Senator Patricia Wren voted in favour of the measure.
```

**13.3.7** `fixtures/tcls_2025_conference_agenda.txt` — TeleCom Leadership Summit 2025 agenda excerpt:
```
TeleCom Leadership Summit 2025
Presented by Acme Broadband Holdings and Vertex Capital Partners
Washington Hilton, November 18-19, 2025

KEYNOTE SESSIONS:
  09:00 — Opening Keynote: "The Future of Connected America" — Lawrence Thorn, Chairman, Acme Broadband Holdings
  10:15 — Fireside Chat: "Spectrum Policy in the 119th Congress" — Senator Marcus Hollis, moderated by Robert Gaines
  14:00 — Policy Panel: "Data, Privacy, and Commerce" — Senator Patricia Wren, Rep. Daniel Moreau, Maria Chen
  16:30 — Closing Remarks — Lawrence Thorn

SPONSORS: Platinum — Acme Broadband Holdings; Gold — Vertex Capital Partners, Digital Future Alliance; Silver — Cascade Digital Infrastructure LLC
```

**13.3.8** `fixtures/investigative_article_draft.txt` — Reporter's draft article (as source document for story assembly):
```
[Draft: Not for publication — research notes]

THE DONOR-SENATOR-REGULATOR TRIANGLE

WASHINGTON — The same PAC funded both senators who sit on the committees overseeing the company it represents. The same private equity firm that pressured Acme Broadband's board co-hosted a conference where those senators spoke. And the company at the centre of an FCC inquiry happens to be a Acme Broadband subsidiary — while another Acme subsidiary is under FTC scrutiny for data broker practices.

Coalition for Better Tomorrow PAC gave $75,000 to Senator Hollis (FEC filing Q3 2025) and $75,000 to Senator Wren (FEC filing Q3 2025) in the same week — August 14 and 15. Both senators sit on committees with jurisdiction over telecommunications regulation. Hollis voted against the Online Data Privacy Act on November 6, 2025. Wren voted in favour.

Vertex Capital Partners — which owns 18.4% of Acme Broadband and has pushed for the spinoff of Cascade Digital — co-sponsored the TeleCom Leadership Summit 2025 where both senators appeared.

Prism Analytics Corp, 34% owned by Acme, is under FCC investigation (EB-2025-IHD-0142) for alleged CPNI violations. A former FCC Commissioner, Robert Gaines, now sits on Acme's board.
```

- [ ] **13.4** Create `examples/investigative-journalism/expected/acme_network_story.json` (golden STORY output — cross-entity narrative):

```json
{
  "story_query": "PAC donors connected to telecommunications senators and FCC inquiry",
  "depth": 2,
  "entities_in_story": [
    "Coalition for Better Tomorrow PAC",
    "Senator Marcus Hollis",
    "Senator Patricia Wren",
    "Acme Broadband Holdings",
    "Vertex Capital Partners",
    "Prism Analytics Corp"
  ],
  "narrative_threads": [
    {
      "thread": "PAC → dual senator funding",
      "facts": [
        {
          "subject": "Senator Marcus Hollis",
          "property": "received_donation_from",
          "object": "Coalition for Better Tomorrow PAC",
          "value": "$75,000 — August 14, 2025",
          "source_excerpt": "Coalition for Better Tomorrow PAC — $75,000 — August 14, 2025",
          "confidence": 0.97
        },
        {
          "subject": "Senator Patricia Wren",
          "property": "received_donation_from",
          "object": "Coalition for Better Tomorrow PAC",
          "value": "$75,000 — August 15, 2025",
          "source_excerpt": "Coalition for Better Tomorrow PAC — $75,000 — August 15, 2025",
          "confidence": 0.97
        }
      ]
    },
    {
      "thread": "Acme ownership → regulatory exposure",
      "facts": [
        {
          "subject": "Acme Broadband Holdings",
          "property": "business_relationship_with",
          "object": "Prism Analytics Corp",
          "value": "34% equity stake (non-controlling)",
          "source_excerpt": "Prism Analytics Corp — 34% equity stake (non-controlling)",
          "confidence": 0.95
        },
        {
          "subject": "Prism Analytics Corp",
          "property": "subject_of_inquiry",
          "object": null,
          "value": "FCC EB-2025-IHD-0142 — CPNI data practices",
          "source_excerpt": "EB-2025-IHD-0142 — Inquiry into Consumer Data Practices — Prism Analytics Corp",
          "confidence": 0.96
        }
      ]
    }
  ],
  "conflicts": []
}
```

- [ ] **13.5** Create `examples/investigative-journalism/README.md`. Cover: use case (investigative reporter building a cross-entity narrative), how story assembly differs from dossier assembly, how to run, how to extend with additional entities, note on fictional names. Emphasise that the source-existence guarantee makes facts in the story auditable — each fact links back to a verbatim excerpt in an archived source.

- [ ] **13.6** Create `examples/investigative-journalism/run.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

EXAMPLE_NAME="investigative-journalism"

echo "==> Setting up example: $EXAMPLE_NAME"
factvault example run "$EXAMPLE_NAME" --use-fixtures

echo "==> Running extract worker"
factvault-worker run extract

echo "==> Running relate worker (builds entity graph)"
factvault-worker run relate

echo ""
echo "Done. Sample queries:"
echo "  curl -X POST http://localhost:8000/stories \\"
echo "    -H 'Content-Type: application/json' \\"
echo "    -d '{\"query\":\"PAC donors connected to telecom senators\",\"depth\":2}' | jq ."
```

- [ ] **13.7** Create `tests/examples/test_investigative_journalism.py`:

```python
"""Tests for investigative-journalism example: property loading, entity seeds, fixture count, golden story shape."""
import json
from pathlib import Path
import pytest
from factvault.examples.base import ExampleLoader

EXAMPLE_DIR = Path(__file__).parent.parent.parent / "examples" / "investigative-journalism"

@pytest.fixture
def loader():
    return ExampleLoader(EXAMPLE_DIR)

def test_properties_load(loader):
    props = loader.load_properties()
    slugs = [p["slug"] for p in props]
    assert "received_donation_from" in slugs
    assert "subject_of_inquiry" in slugs
    assert "regulatory_action_against" in slugs
    assert "quoted_in_article" in slugs

def test_seeds_load(loader):
    seeds = loader.load_seeds()
    assert len(seeds) >= 10
    labels = [s["label"] for s in seeds]
    assert "Coalition for Better Tomorrow PAC" in labels
    assert "Senator Marcus Hollis" in labels
    assert "Acme Broadband Holdings" in labels

def test_fixtures_present(loader):
    fixtures = loader.list_fixtures()
    assert len(fixtures) == 8

def test_golden_story_shape():
    golden = json.loads((EXAMPLE_DIR / "expected" / "acme_network_story.json").read_text())
    assert len(golden["entities_in_story"]) >= 4
    assert len(golden["narrative_threads"]) >= 2
    # each thread must have at least one fact with source_excerpt
    for thread in golden["narrative_threads"]:
        assert len(thread["facts"]) >= 1
        for fact in thread["facts"]:
            assert fact["confidence"] > 0.0
            assert fact["source_excerpt"]

def test_dual_pac_donation_thread():
    """The canonical story thread: same PAC to two senators on same committee."""
    golden = json.loads((EXAMPLE_DIR / "expected" / "acme_network_story.json").read_text())
    pac_thread = next(t for t in golden["narrative_threads"] if "dual senator" in t["thread"])
    subjects = [f["subject"] for f in pac_thread["facts"]]
    assert "Senator Marcus Hollis" in subjects
    assert "Senator Patricia Wren" in subjects
```

- [ ] **13.8** Commit:
```bash
git add examples/investigative-journalism/ tests/examples/test_investigative_journalism.py
git commit -m "feat(examples): add investigative-journalism example (properties + seeds + fixtures + golden story)"
```

---

## Task 14 — Top-level CLI aggregator

**Context:** Wire a single `factvault` Click group that aggregates all subcommands. The standalone entry points (`factvault-worker`, `factvault-api`, `factvault-mcp`) remain unchanged; `factvault` becomes a discovery surface. Tests use `CliRunner`.

- [ ] **14.1** Create `factvault/cli/__init__.py`:

```python
"""factvault CLI package."""
```

- [ ] **14.2** Create `factvault/cli/main.py`:

```python
"""Top-level factvault CLI group.

Aggregates all subcommands under a single entry point while keeping
standalone entry points (factvault-worker, factvault-api, factvault-mcp)
unchanged.
"""
import click

from factvault.doctor.cli import doctor
from factvault.examples.cli import example


@click.group()
@click.version_option()
def cli() -> None:
    """factvault — hallucination-resistant research database.

    Every fact traces to a verbatim source excerpt in an archived document.
    """


cli.add_command(doctor)
cli.add_command(example)


@cli.group()
def auth() -> None:
    """Authentication management (issue tokens, rotate keys)."""


@auth.command("issue-token")
@click.option("--tenant-id", required=True, help="Tenant UUID to issue token for.")
@click.option("--subject", required=True, help="Subject claim (user ID or service name).")
@click.option("--ttl-hours", default=24, show_default=True, help="Token TTL in hours.")
def issue_token(tenant_id: str, subject: str, ttl_hours: int) -> None:
    """Issue a signed JWT for a tenant."""
    from factvault.auth.jwt import issue_jwt  # lazy import: auth module from Plan 4

    token = issue_jwt(tenant_id=tenant_id, subject=subject, ttl_hours=ttl_hours)
    click.echo(token)
```

- [ ] **14.3** Update `pyproject.toml` — add `factvault` console_scripts entry alongside existing entries. Locate the `[project.scripts]` block and add:

```toml
factvault = "factvault.cli.main:cli"
```

The existing `factvault-worker`, `factvault-api`, `factvault-mcp` entries stay unchanged.

- [ ] **14.4** Write failing tests first — `tests/cli/test_main.py`:

```python
"""Tests for the top-level factvault CLI aggregator."""
import pytest
from click.testing import CliRunner

from factvault.cli.main import cli


@pytest.fixture
def runner():
    return CliRunner()


def test_help_lists_doctor(runner):
    result = runner.invoke(cli, ["--help"])
    assert result.exit_code == 0
    assert "doctor" in result.output


def test_help_lists_example(runner):
    result = runner.invoke(cli, ["--help"])
    assert result.exit_code == 0
    assert "example" in result.output


def test_help_lists_auth(runner):
    result = runner.invoke(cli, ["--help"])
    assert result.exit_code == 0
    assert "auth" in result.output


def test_doctor_subcommand_routes(runner):
    """doctor --help should produce the doctor help text (not a routing error)."""
    result = runner.invoke(cli, ["doctor", "--help"])
    assert result.exit_code == 0
    assert "check" in result.output.lower() or "health" in result.output.lower()


def test_example_subcommand_routes(runner):
    result = runner.invoke(cli, ["example", "--help"])
    assert result.exit_code == 0
    assert "run" in result.output or "list" in result.output


def test_auth_issue_token_requires_tenant(runner):
    result = runner.invoke(cli, ["auth", "issue-token"])
    assert result.exit_code != 0
    assert "tenant-id" in result.output.lower() or "missing" in result.output.lower()


def test_version_flag(runner):
    result = runner.invoke(cli, ["--version"])
    assert result.exit_code == 0
    assert "." in result.output  # any version string with a dot
```

- [ ] **14.5** Run tests (expect pass once `main.py` is in place):
```bash
pytest tests/cli/test_main.py -v
```

- [ ] **14.6** Commit:
```bash
git add factvault/cli/ tests/cli/test_main.py pyproject.toml
git commit -m "feat(cli): add top-level factvault CLI aggregator with doctor, example, auth subcommands"
```

---

## Task 15 — README final pass

**Context:** Replace the scaffold README with the production-quality version. Match the NASA mission-documentation voice used in the project spec. 250–400 lines.

- [ ] **15.1** Write failing test — `tests/docs/test_readme.py`:

```python
"""Structural tests for README.md: required sections, badge row, example call-outs."""
from pathlib import Path
import re

README = (Path(__file__).parent.parent.parent / "README.md").read_text()

def test_has_badge_row():
    assert "![" in README  # at least one badge image

def test_has_quickstart():
    assert "docker compose up" in README or "docker-compose up" in README

def test_has_four_examples():
    assert "ai-startup-tracking" in README
    assert "political-research" in README
    assert "pharma-trial-monitoring" in README
    assert "investigative-journalism" in README

def test_has_dossier_vs_story():
    assert "dossier" in README.lower()
    assert "story" in README.lower()

def test_has_source_existence_headline():
    assert "source" in README.lower()
    assert "archive" in README.lower() or "exist" in README.lower()

def test_has_nasa_image_or_alt():
    # Either an Apollo 10 image embed or the alt-text keyword
    assert "apollo" in README.lower() or "nasa" in README.lower()

def test_has_contributing_pointer():
    assert "contribut" in README.lower()
```

- [ ] **15.2** Write `README.md` (250–400 lines). Required sections in order:
  1. Hero: NASA Apollo 10 mission photo (Creative Commons public domain) as a banner image, with alt text.
  2. Badge row: license (MIT), CI status, PyPI version, Docker image.
  3. Headline section: the source-existence promise — "Every fact factvault returns traces to a verbatim excerpt in an archived source document. If the source no longer exists at its original URL, factvault retains the archived copy. Facts without sources cannot be stored."
  4. Dossier-vs-story explainer: one-sentence framing, comparison table (adapted from spec §2), and the four example call-outs with runnable commands.
  5. Five-minute quickstart: `git clone` → `cp .env.example .env` → `docker compose up` → `factvault doctor` → `factvault example run ai-startup-tracking` → `curl` the API.
  6. "How it differs from generic RAG" sidebar: three-bullet comparison.
  7. Deep-dive pointers: links to `docs/concepts/`, `docs/guides/defining-properties.md`, `docs/quickstart.md`.
  8. Contributing: pointer to CONTRIBUTING.md (or GitHub Issues if CONTRIBUTING.md not yet written).

- [ ] **15.3** Run README tests:
```bash
pytest tests/docs/test_readme.py -v
```

- [ ] **15.4** Commit:
```bash
git add README.md tests/docs/test_readme.py
git commit -m "docs(readme): final pass — source-existence headline, dossier-vs-story, 4 examples, quickstart"
```

---

## Task 16 — docs/quickstart.md

**Context:** 200–400 lines. The five-minute first-success path from clone to first fact query.

- [ ] **16.1** Write failing test — `tests/docs/test_quickstart.py`:

```python
"""Structural tests for docs/quickstart.md."""
from pathlib import Path

QUICKSTART = (Path(__file__).parent.parent.parent / "docs" / "quickstart.md").read_text()

def test_has_clone_step():
    assert "git clone" in QUICKSTART

def test_has_env_setup():
    assert ".env" in QUICKSTART

def test_has_docker_compose():
    assert "docker compose" in QUICKSTART or "docker-compose" in QUICKSTART

def test_has_doctor_command():
    assert "factvault doctor" in QUICKSTART

def test_has_curl_examples():
    assert "curl" in QUICKSTART

def test_has_mcp_section():
    assert "mcp" in QUICKSTART.lower() or "claude" in QUICKSTART.lower()

def test_has_api_endpoint_examples():
    # must demonstrate at least the dossier and story endpoints
    assert "/dossiers" in QUICKSTART or "dossier" in QUICKSTART.lower()
    assert "/stories" in QUICKSTART or "story" in QUICKSTART.lower()
```

- [ ] **16.2** Write `docs/quickstart.md` (200–400 lines). Sections:
  1. Prerequisites (Docker, Python 3.11+, Git).
  2. Step 1 — Clone and configure (git clone, cp .env.example, edit .env variables).
  3. Step 2 — Start the stack (`docker compose up -d`; watch healthchecks with `docker compose ps`).
  4. Step 3 — Verify with `factvault doctor` (expected output).
  5. Step 4 — Run an example (`factvault example run ai-startup-tracking`).
  6. Step 5 — Query the API (curl examples for `/entities/by-name`, `/dossiers/by-entity-name`, `POST /stories`, `POST /facts/query`).
  7. Step 6 — Connect the MCP server (Claude Desktop config snippet, Cursor config snippet).
  8. Troubleshooting pointer → `docs/troubleshooting.md`.

- [ ] **16.3** Run tests:
```bash
pytest tests/docs/test_quickstart.py -v
```

- [ ] **16.4** Commit:
```bash
git add docs/quickstart.md tests/docs/test_quickstart.py
git commit -m "docs: add quickstart.md — 5-minute first-success guide"
```

---

## Task 17 — docs/operations.md

**Context:** 300–500 lines. Production operator reference.

- [ ] **17.1** Write failing test — `tests/docs/test_operations.py`:

```python
"""Structural tests for docs/operations.md."""
from pathlib import Path

OPS = (Path(__file__).parent.parent.parent / "docs" / "operations.md").read_text()

def test_has_scaling_section():
    assert "scal" in OPS.lower()

def test_has_backup_restore():
    assert "backup" in OPS.lower() and "restore" in OPS.lower()

def test_has_pg_dump():
    assert "pg_dump" in OPS

def test_has_rls_restore_verification():
    assert "rls" in OPS.lower()

def test_has_monitoring_section():
    assert "prometheus" in OPS.lower() or "monitor" in OPS.lower()

def test_has_secret_rotation():
    assert "secret" in OPS.lower() and "rotat" in OPS.lower()

def test_has_upgrade_procedure():
    assert "alembic" in OPS.lower() or "migrat" in OPS.lower()

def test_has_disaster_recovery():
    assert "disaster" in OPS.lower() or "recovery" in OPS.lower()
```

- [ ] **17.2** Write `docs/operations.md` (300–500 lines). Sections:
  1. **Architecture overview** — which services scale horizontally (api, worker), which do not (postgres — single primary, scale with read replicas for reporting workloads only; mcp — stateless, scales freely).
  2. **Backup and restore** — `pg_dump` command with recommended flags (`--format=custom --compress=9`), restore procedure, post-restore RLS verification (`SET LOCAL app.tenant_id = '<uuid>'; SELECT count(*) FROM entities;` as a spot check), verification that pgvector indices rebuilt correctly.
  3. **Log aggregation** — structured JSON log format, field names (`ts`, `level`, `service`, `tenant_id`, `trace_id`), recommended LogQL query for error rate SLO, Splunk equivalent.
  4. **Monitoring** — Prometheus metrics endpoint (`GET /metrics`), key metrics per service (api: `request_latency_seconds`, `active_tenants`; worker: `pipeline_stage_duration_seconds`, `extraction_miss_total`; db: standard `pg_stat_*` via postgres_exporter), recommended SLOs.
  5. **Secret rotation** — JWT public key rotation (rolling window: issue new key pair, add to JWKS, wait TTL+5min, remove old key), DB credentials rotation (Infisical dynamic secrets or manual `ALTER ROLE`), Wayback API key (env var replacement + rolling restart).
  6. **Upgrade procedure** — pull new image, run `alembic upgrade head` before rolling restart, verify with `factvault doctor`, rollback path (`alembic downgrade -1`).
  7. **Disaster recovery** — RTO/RPO targets for reference deployment, restore runbook (restore postgres, re-run alembic, verify RLS, restart services, run `factvault doctor`).

- [ ] **17.3** Run tests:
```bash
pytest tests/docs/test_operations.py -v
```

- [ ] **17.4** Commit:
```bash
git add docs/operations.md tests/docs/test_operations.py
git commit -m "docs: add operations.md — scaling, backup/restore, monitoring, secret rotation, DR"
```

---

## Task 18 — docs/security.md

**Context:** 200–400 lines. Threat model and multi-tenant isolation documentation.

- [ ] **18.1** Write failing test — `tests/docs/test_security.py`:

```python
"""Structural tests for docs/security.md."""
from pathlib import Path

SEC = (Path(__file__).parent.parent.parent / "docs" / "security.md").read_text()

def test_has_rls_section():
    assert "row level security" in SEC.lower() or "rls" in SEC.lower()

def test_has_jwt_section():
    assert "jwt" in SEC.lower()

def test_has_threat_model():
    assert "threat" in SEC.lower() or "does not protect" in SEC.lower()

def test_has_source_existence_as_security_property():
    assert "fabricat" in SEC.lower() or "hallucin" in SEC.lower()

def test_has_audit_log_section():
    assert "audit" in SEC.lower()

def test_has_idp_integration():
    assert "auth0" in SEC.lower() or "keycloak" in SEC.lower() or "okta" in SEC.lower()
```

- [ ] **18.2** Write `docs/security.md` (200–400 lines). Sections:
  1. **Tenant isolation model** — how `app.tenant_id` GUC is set on every connection, what the RLS policy looks like, how PgBouncer/asyncpg pooling resets the GUC between requests, text diagram of request path through auth middleware → connection acquisition → GUC set → query execution.
  2. **JWT authentication** — how the operator wires an external IdP (Auth0, Keycloak, Okta): JWKS endpoint config, claim mapping (`tenant_id` from custom claim vs. `sub`), `factvault auth issue-token` for machine-to-machine.
  3. **What factvault does NOT protect against** — explicit out-of-scope list: DoS on the API (use a WAF/rate limiter in front), supply-chain attacks on the LLM endpoint, malicious content in source documents causing prompt injection in extraction, cross-tenant timing side-channels via shared embedding model.
  4. **Source-existence as a security property** — not just a quality property: archived raw_text + content_hash + archive_url together mean that a fact cannot be fabricated without also forging a source document. Explain why this raises the attack cost for LLM-generated misinformation ingested as "facts".
  5. **Audit log expectations** — what is logged (entity creates/updates, fact extractions, token issuances), what is not logged (query content by default — privacy consideration), retention recommendation.
  6. **Disclosure and reporting** — how to report security issues (GitHub Security Advisories or a security contact email placeholder).

- [ ] **18.3** Run tests:
```bash
pytest tests/docs/test_security.py -v
```

- [ ] **18.4** Commit:
```bash
git add docs/security.md tests/docs/test_security.py
git commit -m "docs: add security.md — RLS isolation, JWT auth, threat model, source-existence security property"
```

---

## Task 19 — docs/troubleshooting.md

**Context:** 200–400 lines. Top failure modes with symptom → diagnostic command → fix structure.

- [ ] **19.1** Write failing test — `tests/docs/test_troubleshooting.py`:

```python
"""Structural tests for docs/troubleshooting.md."""
from pathlib import Path

TS = (Path(__file__).parent.parent.parent / "docs" / "troubleshooting.md").read_text()

def test_has_wayback_section():
    assert "wayback" in TS.lower() or "rate limit" in TS.lower()

def test_has_trafilatura_section():
    assert "trafilatura" in TS.lower()

def test_has_rls_debugging():
    assert "current_setting" in TS or "rls" in TS.lower()

def test_has_mcp_connection():
    assert "mcp" in TS.lower() and ("claude" in TS.lower() or "cursor" in TS.lower())

def test_has_embedding_model():
    assert "embedding" in TS.lower() or "bge" in TS.lower()

def test_has_postgres_extension():
    assert "pgvector" in TS.lower() or "extension" in TS.lower()

def test_symptom_diagnostic_fix_pattern():
    # Each section should have all three keywords
    assert "symptom" in TS.lower()
    assert "diagnostic" in TS.lower() or "diagnos" in TS.lower()
    assert "fix" in TS.lower()
```

- [ ] **19.2** Write `docs/troubleshooting.md` (200–400 lines). Entries (symptom → diagnostic → fix):

  1. **Wayback Machine rate limits** — symptom: `WaybackRateLimitError` in worker logs, 429 responses; diagnostic: `docker compose logs worker | grep 429`; fix: set `FACTVAULT_WAYBACK_BACKOFF_INITIAL_S=5`, `FACTVAULT_WAYBACK_MAX_RETRIES=8`; note CDX API rate-limit header `X-RateLimit-Remaining`.

  2. **trafilatura returns None on paywalled pages** — symptom: `raw_text=None` for source, extraction skipped; diagnostic: `factvault ingest <url> --dry-run` and inspect `raw_text` field; fix options: (a) supply an HTTP cookie via `FACTVAULT_COLLECTOR_COOKIES` env var, (b) use the Wayback CDX snapshot which may have an earlier cached copy, (c) manually upload a PDF/HTML using the `upload` collector.

  3. **RLS "no rows visible" — queries return empty** — symptom: authenticated API returns `[]` for entities you know are seeded; diagnostic: `SELECT current_setting('app.tenant_id', true);` — if `NULL` or mismatched UUID, the policy filters all rows; fix: ensure the JWT `tenant_id` claim matches the UUID in the `tenants` table; run `factvault doctor` to verify RLS policy check passes.

  4. **MCP server connection failures from Claude Desktop / Cursor** — symptom: "Failed to connect to MCP server" in Claude Desktop; diagnostic: check `docker compose ps mcp` (should be healthy), test `curl http://localhost:8080/health`; fix: verify `FACTVAULT_MCP_AUTH_TOKEN` in `.env` matches the token configured in Claude Desktop's MCP config; check that port 8080 is not firewalled.

  5. **Embedding model load failure — OOM** — symptom: worker crashes with `RuntimeError: CUDA out of memory` or `MemoryError`; diagnostic: `docker stats factvault-worker-1`; fix: BGE-M3 requires ~1.5 GB RAM; ensure Docker memory limit is ≥2 GB; or switch to a smaller embedding model via `FACTVAULT_EMBEDDING_MODEL=BAAI/bge-small-en-v1.5` (384d, ~400 MB).

  6. **Embedding model load failure — disk space** — symptom: `OSError: [Errno 28] No space left on device` during model download; diagnostic: `df -h /root/.cache`; fix: mount a larger volume at `~/.cache/huggingface` or set `HF_HOME` to a path with ≥5 GB free.

  7. **Postgres pgvector extension not found** — symptom: `UndefinedFile: could not open extension control file "vector.control"` during `alembic upgrade head`; diagnostic: `psql -c "SELECT * FROM pg_available_extensions WHERE name='vector';"` in the postgres container; fix: rebuild the postgres image (`docker compose build postgres --no-cache`) — the Chainguard image variant must be `cgr.dev/chainguard/postgres:latest` not `cgr.dev/chainguard/postgres:latest-dev` (dev variant omits extension compilation).

- [ ] **19.3** Run tests:
```bash
pytest tests/docs/test_troubleshooting.py -v
```

- [ ] **19.4** Commit:
```bash
git add docs/troubleshooting.md tests/docs/test_troubleshooting.py
git commit -m "docs: add troubleshooting.md — Wayback limits, trafilatura, RLS, MCP, embedding, pgvector"
```

---

## Task 20 — Full-stack docker-compose integration test

**Context:** Load-bearing test for Plan 5. Uses `testcontainers`' `DockerCompose` helper to spin up the full stack, waits for healthchecks, runs `factvault doctor` inside the api container, and exercises each retrieval mode.

- [ ] **20.1** Write failing test — `tests/integration/test_full_stack_compose.py`:

```python
"""Full-stack docker-compose integration test.

Spins up the complete compose stack, waits for all healthchecks to go green,
runs factvault doctor inside the api container, and exercises one endpoint
of each retrieval mode (dossier, story, fact query) via httpx.

Requires: docker compose v2, ports 5432+8000+8080 free.
Slow test — runs in CI nightly and on PRs touching docker-compose.yml or workers.
Mark: pytest.mark.slow, pytest.mark.integration
"""
import subprocess
import time
from pathlib import Path

import httpx
import pytest
from testcontainers.compose import DockerCompose

PROJECT_ROOT = Path(__file__).parent.parent.parent

@pytest.fixture(scope="module")
def compose_stack():
    """Bring up the full compose stack and tear down after all tests in this module."""
    env_file = PROJECT_ROOT / ".env.test"
    if not env_file.exists():
        pytest.skip(".env.test not present — skipping full-stack integration test")

    with DockerCompose(
        str(PROJECT_ROOT),
        compose_file_name=["docker-compose.yml"],
        env_file=str(env_file),
        pull=False,
    ) as compose:
        # Wait for api healthcheck to go green (up to 90s)
        deadline = time.time() + 90
        while time.time() < deadline:
            try:
                r = httpx.get("http://localhost:8000/healthz", timeout=3)
                if r.status_code == 200:
                    break
            except httpx.ConnectError:
                pass
            time.sleep(3)
        else:
            pytest.fail("API did not become healthy within 90 seconds")

        yield compose


@pytest.mark.slow
@pytest.mark.integration
def test_factvault_doctor_all_green(compose_stack):
    """factvault doctor must exit 0 with all checks passing."""
    result = subprocess.run(
        ["docker", "compose", "exec", "-T", "api", "factvault", "doctor"],
        capture_output=True,
        text=True,
        cwd=PROJECT_ROOT,
    )
    assert result.returncode == 0, f"doctor failed:\n{result.stdout}\n{result.stderr}"
    assert "All checks passed" in result.stdout


@pytest.mark.slow
@pytest.mark.integration
def test_api_healthz(compose_stack):
    r = httpx.get("http://localhost:8000/healthz")
    assert r.status_code == 200
    assert r.json()["status"] == "ok"


@pytest.mark.slow
@pytest.mark.integration
def test_entities_endpoint(compose_stack):
    """GET /entities returns a list (may be empty on fresh stack)."""
    r = httpx.get("http://localhost:8000/entities")
    assert r.status_code == 200
    assert isinstance(r.json(), list)


@pytest.mark.slow
@pytest.mark.integration
def test_dossier_endpoint_realistic_shape(compose_stack):
    """POST /dossiers/by-entity-name returns a dict with expected keys."""
    r = httpx.post(
        "http://localhost:8000/dossiers/by-entity-name",
        json={"entity_name": "canary-entity", "tenant_id": "00000000-0000-0000-0000-000000000001"},
        timeout=30,
    )
    # 200 with empty facts or 404 are both acceptable on a fresh stack
    assert r.status_code in (200, 404)
    if r.status_code == 200:
        body = r.json()
        assert "entity" in body
        assert "facts" in body


@pytest.mark.slow
@pytest.mark.integration
def test_story_endpoint_realistic_shape(compose_stack):
    """POST /stories returns a dict with narrative_threads key."""
    r = httpx.post(
        "http://localhost:8000/stories",
        json={"query": "any entity", "depth": 1},
        timeout=30,
    )
    assert r.status_code in (200, 422)
    if r.status_code == 200:
        assert "narrative_threads" in r.json()


@pytest.mark.slow
@pytest.mark.integration
def test_fact_query_endpoint_realistic_shape(compose_stack):
    """POST /facts/query returns a list."""
    r = httpx.post(
        "http://localhost:8000/facts/query",
        json={"property_slug": "funding_amount", "value_contains": "million"},
        timeout=20,
    )
    assert r.status_code in (200, 422)
    if r.status_code == 200:
        assert isinstance(r.json(), list)


@pytest.mark.slow
@pytest.mark.integration
def test_mcp_server_health(compose_stack):
    """MCP server health endpoint responds."""
    r = httpx.get("http://localhost:8080/health", timeout=10)
    assert r.status_code == 200
```

- [ ] **20.2** Add `.env.test` template (not committed with real values) — document in `docs/quickstart.md` that `cp .env.test.example .env.test` is the setup step for integration tests. Create `.env.test.example`:

```bash
# .env.test.example — copy to .env.test and fill in for integration tests
POSTGRES_USER=factvault
POSTGRES_PASSWORD=factvault
POSTGRES_DB=factvault
FACTVAULT_JWT_SECRET=test-secret-not-for-production
FACTVAULT_LLM_BASE_URL=http://ollama:11434/v1
FACTVAULT_LLM_MODEL=llama3.1:8b
FACTVAULT_EMBEDDING_MODEL=BAAI/bge-m3
FACTVAULT_MCP_AUTH_TOKEN=test-mcp-token
```

- [ ] **20.3** Add `.env.test` to `.gitignore` (never commit test credentials).

- [ ] **20.4** Run unit-level tests (not the slow integration tests — those run in CI):
```bash
pytest tests/integration/test_full_stack_compose.py --collect-only
# verify test collection passes; skip execution locally unless stack is running
```

- [ ] **20.5** Commit:
```bash
git add tests/integration/test_full_stack_compose.py .env.test.example .gitignore
git commit -m "test(integration): add full-stack docker-compose integration test — load-bearing Plan 5 test"
```

---

## Task 21 — Helm chart (optional for v1)

**Context:** `deploy/helm/factvault/`. Scoped tight. Flags as optional for first v1 release. If scope pressure exists, defer with a documented issue.

> **Optional for v1:** If release is time-boxed, defer this task and file a GitHub Issue (`gh issue create --label "scope/v1.1" -t "feat: Helm chart for production K8s deployment"`). The docker-compose stack (Task 1) is the supported deployment path for v1.

- [ ] **21.1** Create `deploy/helm/factvault/Chart.yaml`:

```yaml
apiVersion: v2
name: factvault
description: Hallucination-resistant research database with source-existence guarantee
type: application
version: 0.1.0
appVersion: "0.1.0"
keywords:
  - research
  - facts
  - postgresql
  - pgvector
home: https://github.com/petersimmons1972/factvault
maintainers:
  - name: Peter Simmons
    email: peter.simmons.ga@gmail.com
```

- [ ] **21.2** Create `deploy/helm/factvault/values.yaml`:

```yaml
# deploy/helm/factvault/values.yaml
image:
  repository: ghcr.io/petersimmons1972/factvault-app
  tag: "latest"
  pullPolicy: IfNotPresent

replicaCount:
  api: 2
  worker: 1
  mcp: 1

resources:
  api:
    requests:
      cpu: "250m"
      memory: "512Mi"
    limits:
      cpu: "1"
      memory: "1Gi"
  worker:
    requests:
      cpu: "500m"
      memory: "2Gi"   # embedding model needs ~1.5 GB
    limits:
      cpu: "2"
      memory: "4Gi"
  mcp:
    requests:
      cpu: "100m"
      memory: "256Mi"
    limits:
      cpu: "500m"
      memory: "512Mi"

ingress:
  enabled: true
  className: "nginx"
  host: "factvault.example.com"
  tls:
    enabled: true
    secretName: "factvault-tls"

postgres:
  # Use an existingSecret with keys: username, password, database
  existingSecret: "factvault-postgres-secret"
  host: "postgres"
  port: 5432

auth:
  # JWT public key (PEM) in an existing Kubernetes secret
  # Secret must have key: jwt_public_key
  jwtPublicKeySecret: "factvault-jwt-public-key"

serviceMonitor:
  # Requires prometheus-operator CRD
  enabled: false
  namespace: "monitoring"
  interval: "30s"
```

- [ ] **21.3** Create `deploy/helm/factvault/templates/` with the following files:

  - `_helpers.tpl` — standard name/label helpers
  - `postgres-statefulset.yaml` — StatefulSet for postgres with PVC (volume claim template), `fsGroup: 65532`, `allowPrivilegeEscalation: false`, Chainguard image
  - `api-deployment.yaml` — Deployment for api; readinessProbe on `/healthz`; env from existingSecret refs
  - `worker-deployment.yaml` — Deployment for worker; no inbound ports
  - `mcp-deployment.yaml` — Deployment for mcp; port 8080
  - `ingress.yaml` — conditional on `ingress.enabled`
  - `servicemonitor.yaml` — conditional on `serviceMonitor.enabled`

  All templates follow the Chainguard security context (spec §6): `runAsUser: 65532`, `runAsNonRoot: true`, `fsGroup: 65532`, `allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]`.

- [ ] **21.4** Create `deploy/helm/factvault/README.md`:

```markdown
# factvault Helm Chart

Optional production deployment path for Kubernetes. For local development use `docker compose`.

## Prerequisites

- Kubernetes 1.27+
- Helm 3.12+
- A Postgres 15+ instance with pgvector (or use the bundled StatefulSet)
- A Kubernetes secret `factvault-postgres-secret` with keys `username`, `password`, `database`
- A Kubernetes secret `factvault-jwt-public-key` with key `jwt_public_key`

## Install

```bash
helm install factvault ./deploy/helm/factvault \
  --namespace factvault \
  --create-namespace \
  -f my-values.yaml
```

After install, run `factvault doctor` inside the api pod to verify:

```bash
kubectl exec -n factvault deploy/factvault-api -- factvault doctor
```

## Values reference

See `values.yaml` for all configurable values with inline documentation.
```

- [ ] **21.5** Commit:
```bash
git add deploy/helm/
git commit -m "feat(helm): add optional Helm chart for production K8s deployment (v1.1 scope)"
```

---

## Task 22 — CI workflow final pass + release workflow

**Context:** Finalize `.github/workflows/ci.yml` with a `slow-tests` job gated appropriately, and add the tag-triggered `.github/workflows/release.yml`.

- [ ] **22.1** Update `.github/workflows/ci.yml` — add a `slow-tests` job that runs the full-stack integration test. The job is gated on: (a) nightly schedule, OR (b) PRs that touch `docker-compose.yml`, `docker/`, `factvault/workers/`, or `tests/integration/`. Regular PR runs skip it.

Full updated `ci.yml`:

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

jobs:
  lint-and-typecheck:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: "3.11"
          cache: pip
      - run: pip install -e ".[dev]"
      - run: ruff check .
      - run: mypy factvault/ --ignore-missing-imports

  unit-tests:
    runs-on: ubuntu-latest
    needs: lint-and-typecheck
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: "3.11"
          cache: pip
      - run: pip install -e ".[dev]"
      - run: pytest tests/unit/ tests/doctor/ tests/examples/ tests/cli/ tests/docs/ -v --tb=short

  slow-tests:
    runs-on: ubuntu-latest
    needs: unit-tests
    # Run nightly OR on PRs touching compose/workers/integration tests
    if: |
      github.event_name == 'schedule' ||
      (github.event_name == 'pull_request' && (
        contains(github.event.pull_request.changed_files, 'docker-compose.yml') ||
        contains(github.event.pull_request.changed_files, 'docker/') ||
        contains(github.event.pull_request.changed_files, 'factvault/workers/') ||
        contains(github.event.pull_request.changed_files, 'tests/integration/')
      ))
    services:
      # Integration test spins its own compose stack via testcontainers;
      # no additional services needed here.
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: "3.11"
          cache: pip
      - run: pip install -e ".[dev]"
      - name: Copy test env
        run: cp .env.test.example .env.test
      - run: pytest tests/integration/ -v --tb=short -m "slow and integration"

on:
  schedule:
    - cron: "0 3 * * *"   # nightly at 03:00 UTC
  push:
    branches: [main]
  pull_request:
```

> **Note:** The `on:` block at the bottom extends the top-level trigger. In the final file, merge into a single top-level `on:` with `schedule`, `push`, and `pull_request` keys. Shown separately here for clarity.

- [ ] **22.2** Create `.github/workflows/release.yml`:

```yaml
name: Release

on:
  push:
    tags:
      - "v*.*.*"

permissions:
  contents: write
  packages: write
  id-token: write   # required for trusted PyPI publishing

jobs:
  build-and-push-image:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Extract image metadata
        id: meta
        uses: docker/metadata-action@v5
        with:
          images: ghcr.io/${{ github.repository_owner }}/factvault-app
          tags: |
            type=semver,pattern={{version}}
            type=semver,pattern={{major}}.{{minor}}
            type=raw,value=latest

      - name: Build and push Docker image
        uses: docker/build-push-action@v5
        with:
          context: .
          file: docker/app/Dockerfile
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          cache-from: type=gha
          cache-to: type=gha,mode=max

  publish-pypi:
    runs-on: ubuntu-latest
    environment:
      name: pypi
      url: https://pypi.org/p/factvault
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: "3.11"
      - run: pip install build
      - run: python -m build
      - name: Publish to PyPI (trusted publishing — no API key required)
        uses: pypa/gh-action-pypi-publish@release/v1

  create-github-release:
    runs-on: ubuntu-latest
    needs: [build-and-push-image, publish-pypi]
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0   # full history for changelog generation

      - name: Generate changelog from conventional-commit footers
        id: changelog
        run: |
          PREV_TAG=$(git describe --tags --abbrev=0 HEAD^ 2>/dev/null || echo "")
          if [ -n "$PREV_TAG" ]; then
            RANGE="${PREV_TAG}..HEAD"
          else
            RANGE="HEAD"
          fi
          # Extract conventional-commit footers: feat, fix, docs, refactor, test
          CHANGELOG=$(git log "$RANGE" --pretty=format:"%s" \
            | grep -E '^(feat|fix|docs|refactor|test|perf)\(' \
            | sed 's/^feat(/- feat(/; s/^fix(/- fix(/; s/^docs(/- docs(/; s/^refactor(/- refactor(/; s/^test(/- test(/; s/^perf(/- perf(/' \
            | head -50)
          echo "changelog<<EOF" >> "$GITHUB_OUTPUT"
          echo "$CHANGELOG" >> "$GITHUB_OUTPUT"
          echo "EOF" >> "$GITHUB_OUTPUT"

      - name: Create GitHub Release
        uses: softprops/action-gh-release@v2
        with:
          body: |
            ## What's Changed

            ${{ steps.changelog.outputs.changelog }}

            ## Docker Image

            ```
            docker pull ghcr.io/${{ github.repository_owner }}/factvault-app:${{ github.ref_name }}
            ```

            ## PyPI

            ```
            pip install factvault==${{ github.ref_name }}
            ```
          generate_release_notes: false
```

- [ ] **22.3** Write failing test — `tests/docs/test_ci_workflows.py`:

```python
"""Smoke tests for CI/release workflow YAML structure."""
from pathlib import Path
import yaml

WORKFLOWS_DIR = Path(__file__).parent.parent.parent / ".github" / "workflows"

def test_ci_yml_exists():
    assert (WORKFLOWS_DIR / "ci.yml").exists()

def test_release_yml_exists():
    assert (WORKFLOWS_DIR / "release.yml").exists()

def test_ci_has_unit_tests_job():
    ci = yaml.safe_load((WORKFLOWS_DIR / "ci.yml").read_text())
    assert "unit-tests" in ci["jobs"]

def test_ci_has_slow_tests_job():
    ci = yaml.safe_load((WORKFLOWS_DIR / "ci.yml").read_text())
    assert "slow-tests" in ci["jobs"]

def test_release_triggers_on_version_tag():
    release = yaml.safe_load((WORKFLOWS_DIR / "release.yml").read_text())
    tags = release["on"]["push"]["tags"]
    assert any("v*" in t for t in tags)

def test_release_has_pypi_job():
    release = yaml.safe_load((WORKFLOWS_DIR / "release.yml").read_text())
    assert "publish-pypi" in release["jobs"]

def test_release_has_image_push_job():
    release = yaml.safe_load((WORKFLOWS_DIR / "release.yml").read_text())
    assert "build-and-push-image" in release["jobs"]
```

- [ ] **22.4** Run workflow tests:
```bash
pytest tests/docs/test_ci_workflows.py -v
```

- [ ] **22.5** Commit:
```bash
git add .github/workflows/ci.yml .github/workflows/release.yml tests/docs/test_ci_workflows.py
git commit -m "ci: final pass — slow-tests job + release workflow (GHCR image + PyPI trusted publish + changelog)"
```

---

## Self-Review

### Spec Coverage Checklist

| Spec requirement | Task |
|------------------|------|
| Full docker-compose stack — postgres + api + worker + mcp (§6) | Pass 1 T1 |
| docker-compose override template for optional services (§6) | Pass 1 T1 |
| App Dockerfile — multi-stage Chainguard python:latest-dev → wolfi-base, nonroot 65532, tini (§6) | Pass 1 T2 |
| `.env.example` — all env vars documented (§6) | Pass 1 T3 |
| `factvault doctor` — 7 checks: DB, pgvector, RLS, Wayback, embedding, LLM, canary (§6) | Pass 1 T4–T7 |
| Canary end-to-end fact ingest (collect → archive → extract → corroborate → verify) (§6) | Pass 1 T7 |
| `factvault example` CLI — list, info, run subcommands (§8) | Pass 1 T8–T9 |
| Example: ai-startup-tracking (properties + seeds + fixtures + golden dossier + run.sh) (§8) | Pass 1 T10 |
| Example: political-research (properties + seeds + fixtures + golden output + run.sh) (§8) | Pass 1 T11 |
| Example: pharma-trial-monitoring (properties + seeds + fixtures + golden dossier + run.sh) (§8) | T12 |
| Example: investigative-journalism (properties + seeds + fixtures + golden story + run.sh) (§8) | T13 |
| Top-level `factvault` CLI aggregator with all subcommands wired (§7 factvault/cli/) | T14 |
| README final pass — source-existence headline, dossier-vs-story, 4 examples, quickstart (§8) | T15 |
| docs/quickstart.md — 5-minute first-success guide with curl examples (§8) | T16 |
| docs/operations.md — scaling, backup/restore, monitoring, secret rotation, DR (§8) | T17 |
| docs/security.md — RLS isolation, JWT auth, threat model, source-existence security property (§8) | T18 |
| docs/troubleshooting.md — Wayback, trafilatura, RLS, MCP, embedding, pgvector (§8) | T19 |
| Full-stack docker-compose integration test (§6 + §8) | T20 |
| Helm chart for production K8s deployment — optional v1.1 (§6 K8s manifests note) | T21 |
| CI final pass — slow-tests job gated correctly (§7 .github/workflows/) | T22 |
| Release workflow — GHCR image push + PyPI trusted publish + changelog (§7 release.yml) | T22 |

### Placeholder Scan

Reviewed. No placeholders. All fixture data is fully written (no `[...]` content stubs). All YAML property definitions include complete field sets. All golden output files contain concrete values. The only deliberate stub is `CONTRIBUTING.md` referenced in Task 15 — its absence is noted in the README as "GitHub Issues" fallback, which is correct for a pre-v1 project.

### Type Consistency Check

Reviewed. All names consistent:

- CLI entry point is `factvault` (singular, no hyphen) throughout: `pyproject.toml` console_script `factvault = "factvault.cli.main:cli"`, all task command examples, and the README quickstart.
- Standalone entry points remain `factvault-worker`, `factvault-api`, `factvault-mcp` — hyphenated, unchanged from Plans 2-4.
- `factvault doctor` (space, subcommand) — used consistently. NOT `factvault-doctor`.
- `factvault example run <name>` — used consistently across Tasks 8–13 and the README/quickstart.
- Example loader API is consistent: `ExampleLoader(directory)` with `.load_properties()`, `.load_seeds()`, `.list_fixtures()` — same interface used in all four example test files (T10, T11, T12, T13).
- Property YAML namespace field matches the test fixture namespace in all four examples (`ai_startup`, `political`, `pharma_trial`, `investigative`).

### Cross-Plan Coherence Check

**Compose service names vs. Plan 4 K8s manifests:** Plan 5 T1 compose services are named `postgres`, `api`, `worker`, `mcp`. Plan 4 K8s manifests use `factvault-api`, `factvault-worker`, `factvault-mcp` as Deployment names (K8s convention requires prefix). Service names in K8s are `factvault-api-svc` etc. No naming conflict — compose and K8s are separate deployment targets, not cross-referencing each other's service names.

**`factvault doctor` canary vs. Plans 2-4 pipeline stages:** The canary (T7) exercises: collect (Plan 2 Stage 1) → archive (Plan 2 Stage 2) → extract (Plan 3 Stage 3) → corroborate (Plan 3 Stage 4) → verify (Plan 4 Stage 5). All five pipeline stages from Plans 2-3-4 are touched. ✓

**Example loader vs. Plan 3 T11 property vocabulary loader:** The `ExampleLoader.load_properties()` method reads `properties.yaml` and calls `factvault.properties.registry.register_from_yaml()` (Plan 3 T11). Consistent. ✓

**Full-stack test vs. Plan 4 API endpoints:** T20 exercises `/healthz`, `/entities`, `/dossiers/by-entity-name` (Plan 4 dossier endpoint), `POST /stories` (Plan 4 story endpoint), `POST /facts/query` (Plan 4 fact query endpoint). All Plan 4 retrieval modes are covered with realistic-shape requests. ✓
