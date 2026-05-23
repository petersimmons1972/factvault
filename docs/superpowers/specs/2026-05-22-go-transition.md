# Go Transition Spec

**Date:** 2026-05-22
**Status:** Active — Phase 1 execution begins immediately

---

## Overview

This document captures the language transition decision for factvault. The project was initially designed and its first two plans (schema + source pipeline) partially implemented in Python. This spec records the decision to rewrite workers, API, MCP server, and CLI in Go, with a single dedicated Python microservice retained for embedding computation. It covers rationale, the locked stack, scope of impact per plan, migration sequencing, and known risks. All subsequent plan-level implementation specs reference this document as the authoritative source for language and toolchain decisions.

---

## 1. Header

| Field      | Value                                                                 |
|------------|-----------------------------------------------------------------------|
| Title      | Go Transition Spec                                                    |
| Date       | 2026-05-22                                                            |
| Status     | Active                                                                |
| Phase      | Phase 1 execution begins immediately                                  |
| Author     | Peter Simmons                                                         |
| Supersedes | Python toolchain implied by Plans 1 and 2 as merged to main          |

**Framing.** Plans 1 and 2 were designed and merged using Python (Alembic, SQLAlchemy, Click, asyncpg, testcontainers-python). This document captures the decision to migrate to Go for all application code — workers, API, MCP server, CLI, and test harness — effective immediately. The Python embedder microservice is retained as a single bounded service. The database schema (SQL), the bundle JSON contract, the six-stage pipeline structure, and the multi-tenant RLS guarantees are all language-agnostic and do not change. The migration is a rewrite of the application layer, not an architectural revision.

---

## 2. Why This Change

### 2.1 Operational Footprint

A Go binary compiles to a single static executable with no runtime dependency. Deploying a new version is `docker pull` + `kubectl rollout restart` — no virtualenv, no pip, no site-packages, no Python version matrix. The current Python stack requires managing `pyproject.toml`, Alembic migrations, a separate migration container, and multiple entry-point processes. With Go, every subcommand (`worker`, `api`, `mcp`, `doctor`, `auth`, `example`, `migrate`) lives in the same binary. The Dockerfile build stage produces one artifact; the runtime stage is a minimal Chainguard `wolfi-base` with no interpreter overhead.

### 2.2 Concurrency Model

The worker processes are polling loops that fan out concurrent HTTP fetches, database writes, and embedding calls. Go's goroutine model is a natural fit for this pattern. A `collect` worker that polls six collector sources can spawn six goroutines sharing a single pgx connection pool and a `sync.WaitGroup` boundary — without the overhead of Python threads (GIL-bound for CPU work) or asyncio (event-loop complexity for mixed IO/CPU workloads). The pipeline pattern — a goroutine per stage, channels for back-pressure, context cancellation for shutdown — maps directly to the six-stage architecture.

### 2.3 Type Safety on a Security Boundary

Multi-tenant isolation is enforced at the database layer via Postgres RLS, but the application layer is the trust boundary: the code that sets `app.tenant_id` on every database connection before executing a query. A type error at this boundary (a `nil` UUID, a wrong-type assertion, a silent string conversion) could cause a tenant bleed. Go's compile-time type system, combined with `pgx`'s typed scan targets, eliminates entire classes of type-coercion bugs that are silent in Python. The `tenant_id` is a `pgtype.UUID` from the moment it leaves the JWT verifier to the moment it is written to `SET LOCAL app.tenant_id`. There is no stringly-typed path.

### 2.4 Founder's Broader Stack

The homelab and production tooling — `hermes`, `ai-fleet-controller`, `engram-go` — are all Go services. Sharing toolchain means sharing patterns for K8s probes, graceful shutdown, structured logging (`log/slog`), and Chainguard image builds. Drift between the factvault Python stack and the Go-native homelab toolchain creates maintenance overhead. Converging on Go eliminates that drift.

---

## 3. What Stays Python

### The Embedder Microservice

The Python embedder microservice (`services/embedder/`) is the single Python component that survives the transition. It is a narrow FastAPI service that loads `BAAI/bge-m3` via `sentence-transformers` and exposes a single `POST /embed` endpoint.

**Why not Go for embeddings?**
- `sentence-transformers` is the reference implementation for BGE-M3. It uses HuggingFace tokenizers and the exact model weights from `BAAI/bge-m3` on HuggingFace Hub.
- Ollama and other Go-adjacent wrappers load the same weights, but tokenizer behavior can diverge between versions, producing different token sequences for edge-case inputs (Unicode, long sequences, multi-lingual text). A divergence in tokenization produces a divergence in embedding space, which silently corrupts similarity search results.
- BGE-M3 is a 1024-dimension model. The stored embeddings in the database are the BGE-M3 embedding space, period. Every query embedding must come from the same tokenizer + weight checkpoint as the stored embeddings. Locking that to `sentence-transformers` + the pinned HuggingFace model ID is the only way to guarantee this.
- The tradeoff: one Python container in the deploy stack. The benefit: known-good embeddings. The tradeoff is worth it.

The embedder contract is fully specified in §8 below. Go workers call it over HTTP; the Go API does not need direct access.

---

## 4. Locked Stack

These decisions are final and are not subject to re-litigation in any subsequent plan spec. If a plan spec proposes a different library for a purpose covered here, this spec takes precedence.

### 4.1 Language

**Go 1.22+.** The minimum version is 1.22 for range-over-func iterators and the improved `net/http` mux. The `go.mod` pins the minimum Go version.

### 4.2 Single Binary — Cobra Subcommands

**`github.com/spf13/cobra`** — CLI framework.

All application entry points are subcommands of a single `factvault` binary:

| Subcommand  | Purpose                                                     |
|-------------|-------------------------------------------------------------|
| `worker`    | Run a named pipeline worker (collect, archive, extract, …) |
| `api`       | Start the chi HTTP API server                               |
| `mcp`       | Start the MCP server (go-sdk)                               |
| `doctor`    | Run first-boot health checks                                |
| `auth`      | JWT utilities (issue dev keys, verify token)                |
| `example`   | Load an example domain from YAML fixtures                   |
| `migrate`   | Run goose database migrations                               |

Justification: single binary simplifies the Dockerfile (`COPY factvault /usr/local/bin/`), Kubernetes manifests (one image, different command args), and local development (`./factvault doctor`).

### 4.3 Web Framework

**`github.com/go-chi/chi/v5`** — HTTP router for the REST API.

Justification: chi is stdlib-compatible (`net/http`), lightweight, composable middleware, no code generation required. It handles the routing patterns needed (path params, middleware chains for JWT verification and tenant context injection) without magic. FastAPI's OpenAPI generation is replaced by a hand-maintained OpenAPI spec in `docs/api/`.

### 4.4 Database Driver

**`github.com/jackc/pgx/v5`** with the `pgvector-go` extension for vector type support.

Justification: pgx is the canonical high-performance Postgres driver for Go. It supports binary protocol, typed scan targets (`pgtype.UUID`, `pgtype.Timestamptz`, `pgtype.Numeric`), and prepared statements. The `pgvector-go` library registers a custom `pgtype` codec for `vector(N)` columns, enabling strongly-typed scan/encode for embedding columns without going through `[]float32` ↔ string round-trips.

**pgvector-go custom type registration pattern:**

```go
// internal/db/conn.go
import (
    "github.com/jackc/pgx/v5/pgxpool"
    pgvector "github.com/pgvector/pgvector-go"
    pgxvec  "github.com/pgvector/pgvector-go/pgx"
)

func NewPool(ctx context.Context, connStr string) (*pgxpool.Pool, error) {
    cfg, err := pgxpool.ParseConfig(connStr)
    if err != nil {
        return nil, err
    }
    cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
        pgxvec.RegisterTypes(ctx, conn)
        return nil
    }
    return pgxpool.NewWithConfig(ctx, cfg)
}
```

Every pool acquired through `NewPool` has the vector type registered. Queries that scan into `pgvector.Vector` work transparently.

### 4.5 Typed Queries

**`sqlc`** — code generation from SQL files.

SQL query files live in `internal/db/queries/*.sql`. `sqlc generate` produces typed Go functions and structs in `internal/db/`. No ORM. No reflection-based query builder. The generated code is committed to the repository; CI fails if the generated code is stale (`sqlc diff`).

Justification: typed queries at compile time, SQL files readable by any DBA, no SQLAlchemy model drift.

### 4.6 Migrations

**`github.com/pressly/goose/v3`** — SQL migration runner, replacing Alembic.

Migration files live in `migrations/` as plain `*.sql` files with goose directive comments:

```sql
-- +goose Up
ALTER TABLE sources ADD COLUMN publisher_domain TEXT;

-- +goose Down
ALTER TABLE sources DROP COLUMN publisher_domain;
```

The `factvault migrate` subcommand wraps goose and is the only migration entry point. CI runs `factvault migrate up` against a test database on every PR. The goose migration history is stored in the `goose_db_version` table (goose default).

Justification: Alembic's Python-based migration scripts are replaced by plain SQL, which is portable, readable, and reviewable without a Python environment.

### 4.7 MCP Server

**`github.com/modelcontextprotocol/go-sdk`** — MCP server implementation.

The MCP server runs as a subprocess (stdio transport) or as an HTTP server (SSE transport). Both modes are supported by go-sdk. The `factvault mcp` subcommand starts the server in the configured transport mode.

**Known risk:** go-sdk is community-maintained and less mature than the Python MCP SDK. See §11 for the hand-rolling budget.

### 4.8 HTML→Text Extraction

**`github.com/go-shiori/go-readability`** — HTML to clean text extraction, replacing trafilatura.

go-readability implements Mozilla's Readability algorithm. It handles most article pages well. For aggressively JS-rendered or paywalled pages, it is less sophisticated than trafilatura.

**Known limitation:** go-readability is documented as a known v1 limitation. See §11.

### 4.9 HTTP Client

**stdlib `net/http`** — no third-party HTTP client.

Go's standard library HTTP client is sufficient for all use cases: collector fetches, Wayback SPN2 submission, embedder microservice calls, LLM API calls. Using stdlib avoids dependency on third-party HTTP clients with diverging interfaces.

### 4.10 Logging

**`log/slog`** — structured logging from stdlib (Go 1.21+).

All log output is JSON-formatted structured logs via `slog.New(slog.NewJSONHandler(os.Stdout, nil))`. Fields: `time`, `level`, `msg`, plus contextual fields (tenant_id, source_id, worker_name, etc.) via `slog.With`. No third-party logging library.

### 4.11 Testing

**stdlib `testing`** + **`github.com/ory/dockertest/v3`** for integration tests requiring a live Postgres instance.

Justification: dockertest spins up a real Postgres container, runs the goose migrations, and tears it down after the test. This replaces `testcontainers-python`. The `testing` stdlib is sufficient for all test patterns; no third-party test framework (testify, gomock) is needed in v1.

### 4.12 Snapshot / Golden Tests

**`github.com/google/go-cmp`** — deep equality and diff-readable output for golden/snapshot tests.

Used in end-to-end tests to compare bundle JSON output against expected fixtures. `go-cmp` produces a human-readable diff when test output diverges from the golden file, making CI failures actionable.

---

## 5. Project Layout

```
factvault/
├── cmd/
│   └── factvault/
│       └── main.go                      # Cobra root + subcommand registration
├── internal/
│   ├── api/                             # chi HTTP server + routes
│   ├── workers/                         # archive, verify, extract, corroborate, dossier, relate
│   ├── collectors/                      # rss, http, sitemap, searxng, wayback_cdx, upload
│   ├── archiving/                       # wayback submission, htmlextract (go-readability), hash
│   ├── extractors/                      # deterministic (identifiers, money, dates, gazetteer) + llm
│   ├── vocabulary/                      # property resolver (strict + permissive modes)
│   ├── assembler/                       # bundle building + confidence computation
│   ├── mcp/                             # MCP server tools (entity_lookup, story_query, fact_query)
│   ├── doctor/                          # first-boot health checks (7 checks)
│   ├── examples/                        # example domain YAML loader
│   ├── auth/                            # JWT verification + dev key issuance
│   ├── db/
│   │   ├── conn.go                      # pgx pool constructor + pgvector type registration
│   │   ├── rls.go                       # SET LOCAL app.tenant_id helper (replaces rls.py)
│   │   └── queries/                     # *.sql files consumed by sqlc codegen
│   ├── config/                          # YAML config loader (collector schedules, LLM endpoint, etc.)
│   └── embedclient/                     # HTTP client for the Python embedder microservice
├── migrations/                          # *.sql goose migration files (replaces alembic/versions/)
├── services/
│   └── embedder/                        # Python sentence-transformers microservice (stays Python)
│       ├── app.py                       # FastAPI app (POST /embed, GET /healthz)
│       ├── pyproject.toml               # sentence-transformers, fastapi, uvicorn
│       └── Dockerfile                   # Chainguard python:latest-dev build → wolfi-base runtime
├── deploy/
│   ├── docker/                          # Dockerfile for the Go binary (multi-stage, wolfi-base)
│   └── k8s/                             # K8s manifests: Deployment, CronJob, Service, PVC, ConfigMap
├── docs/                                # All project documentation
│   ├── concepts/                        # Mental model docs (language-agnostic)
│   ├── guides/                          # Operator how-tos
│   ├── api/                             # OpenAPI spec + rendered reference
│   └── superpowers/
│       └── specs/                       # Design specs (this file + factvault-design.md)
├── examples/                            # Runnable example domains (YAML fixtures, unchanged)
│   ├── ai-startup-tracking/
│   ├── political-research/
│   ├── pharma-trial-monitoring/
│   └── investigative-journalism/
├── tests/
│   ├── unit/                            # stdlib testing; fast, no external deps
│   ├── integration/                     # dockertest Postgres; goose migrations applied before test
│   └── e2e/                             # end-to-end pipeline tests using example fixtures
├── .github/
│   └── workflows/
│       ├── ci.yml                       # go vet, go test ./..., sqlc diff on every PR
│       ├── integration.yml              # integration tests on merge to main
│       └── release.yml                  # docker build + push to GHCR on tag
├── go.mod                               # module declaration + dependency pins
├── go.sum                               # dependency checksums
├── Makefile                             # dev tasks: build, test, migrate, generate, lint
└── README.md
```

**Key structural differences from the Python layout:**

| Python (pre-migration)                    | Go (post-migration)                        |
|-------------------------------------------|--------------------------------------------|
| `factvault/` Python package               | `cmd/factvault/` + `internal/` packages    |
| `alembic.ini` + `alembic/versions/`       | `migrations/*.sql` + goose                 |
| `pyproject.toml` entry points             | Cobra subcommand registration in `main.go` |
| `testcontainers-python` in conftest.py    | `dockertest` in `_test.go` setup functions |
| `sentence-transformers` in worker process | `services/embedder/` microservice          |
| `factvault/db/` (SQLAlchemy + rls.py)     | `internal/db/` (pgx + conn.go + rls.go)   |
| `factvault/cli/` (Click)                  | Cobra subcommands in `cmd/factvault/`      |

---

## 6. Migration Scope by Plan

### Plan 1 — Schema (Alembic + SQLAlchemy)

**Status:** Merged to main. Python code present on main in `factvault/db/`, `alembic/`, `conftest.py`.

**Go rewrite scope:**
- `alembic/versions/*.py` → `migrations/*.sql` (goose format). The SQL itself is copied verbatim; only the Python wrapper and Alembic history table are replaced.
- `factvault/db/models.py` (SQLAlchemy ORM models) → deleted. `sqlc` generates typed structs from `internal/db/queries/*.sql`; no ORM layer.
- `factvault/db/rls.py` (RLS session helper) → `internal/db/rls.go`. Same semantics: `SET LOCAL app.tenant_id = $1` wrapped in a helper that takes a `pgx.Tx` and a `pgtype.UUID`.
- `conftest.py` (testcontainers-python setup) → `internal/db/testhelper_test.go` using dockertest. The helper spins up a Postgres container, runs `factvault migrate up`, and returns a connection string for test use.
- `factvault/db/session.py` (asyncpg/SQLAlchemy connection pool) → `internal/db/conn.go` (pgx pool + pgvector type registration, as shown in §4.4).

**Schema itself:** unchanged. The SQL DDL in the Alembic migrations is the canonical schema definition; it is copied as-is into goose migration files.

**Deletion:** All Python Plan 1 code (`factvault/db/`, `alembic/`, `conftest.py`, `alembic.ini`) is deleted as part of Plan 1 Go execution. The Python files are not kept alongside the Go code.

### Plan 2 — Source Pipeline (Python Collectors + Workers)

**Status:** Merged to main. Python code present on main in `factvault/workers/`, `factvault/collectors/`, `factvault/cli/`, `tests/`.

**Go rewrite scope:**
- `factvault/collectors/*.py` → `internal/collectors/*.go`. Each collector is a Go struct implementing a `Collector` interface (`Fetch(ctx context.Context) (<-chan RawDocument, error)`).
- `factvault/workers/collect.py` → `internal/workers/collect.go`. Polling loop using goroutines per collector, pgx for DB writes.
- `factvault/workers/archive.py` → `internal/workers/archive.go`. go-readability replaces trafilatura; Wayback SPN2 submission via stdlib `net/http`.
- `factvault/workers/verify.py` → `internal/workers/verify.go`. Re-fetch via stdlib `net/http`, SHA-256 via `crypto/sha256`, excerpt check in Go.
- `factvault/cli/` (Click commands) → Cobra subcommands registered in `cmd/factvault/main.go`.
- `tests/unit/` (pytest) → `internal/*/` `_test.go` files (stdlib `testing`).
- `tests/integration/` (pytest + testcontainers) → `tests/integration/` Go test files with dockertest.

**YAML-described collector format:** unchanged. The same `config/collectors/*.yaml` format is loaded by the Go collector registry.

**Deletion:** All Python Plan 2 code deleted as part of Plan 2 Go execution.

### Plan 3 — Fact Pipeline (Extractors, LLM Client, Corroborate Worker)

**Status:** `feat/fact-pipeline` branch, not merged. T10 Python vocab resolver is on that branch.

**Go rewrite scope:**
- Deterministic extractors → `internal/extractors/deterministic/` (funding, dates, identifiers, entities). No Python regex library equivalents needed; Go's `regexp` stdlib is sufficient.
- LLM extractor → `internal/extractors/llm.go`. The Go extractor calls the configured OpenAI-compatible endpoint via stdlib `net/http`, sends the JSON schema prompt, and parses the structured JSON response.
- `factvault/assembler/confidence.py` → `internal/assembler/confidence.go`. The deterministic confidence formula is a pure function; translation is mechanical.
- `factvault/workers/corroborate.py` → `internal/workers/corroborate.go`.
- `factvault/vocabulary/` (vocab resolver) → `internal/vocabulary/`. The `feat/fact-pipeline` T10 vocab resolver is abandoned; the Go version is written from scratch.

**`feat/fact-pipeline` branch:** abandoned. Do not attempt to merge or port the Python code from that branch. The Go Plan 3 implementation starts from the design spec, not from the existing Python code.

**LLM client pattern:**
```go
// internal/extractors/llm.go
type LLMClient struct {
    BaseURL string
    APIKey  string
    Model   string
    http    *http.Client
}

func (c *LLMClient) Extract(ctx context.Context, source *db.Source, text string) ([]StatementProposal, error) {
    // POST to BaseURL/chat/completions with response_format: {type: "json_object"}
    // Parse response into []StatementProposal
    // Return; excerpt-offset check runs in the caller (workers/extract.go)
}
```

### Plan 4 — Bundle and Retrieval (REST API, MCP Server, JWT Auth)

**Status:** Not yet written.

**Go rewrite scope:**
- `factvault/api/` (FastAPI) → `internal/api/` (chi). Routes, middleware (JWT verification, tenant context injection), request/response models (plain Go structs, JSON tags).
- `factvault/mcp/server.py` (mcp-python-sdk) → `internal/mcp/` (go-sdk). Three tools: `factvault__entity_lookup`, `factvault__story_query`, `factvault__fact_query`.
- JWT auth → `internal/auth/` (JWT verification + dev key issuance via `factvault auth` subcommand).
- `factvault/assembler/bundle.py` → `internal/assembler/bundle.go`. Single entry point for all bundle production (dossier + story paths). The bundle JSON shape is unchanged.

**OpenAPI spec:** maintained by hand in `docs/api/openapi.yaml`. No automatic generation from code annotations.

### Plan 5 — Deploy and Examples

**Status:** Not yet written.

**Go rewrite scope:**
- `docker-compose.yml`: updated to include three services: `postgres` (Chainguard postgres + pgvector), `embedder` (Python microservice), `factvault` (Go binary, all subcommands). Workers are invoked via `docker compose run factvault worker <name>` or as separate CronJob containers in K8s.
- K8s manifests: one Deployment for the API server, one Deployment for the embedder microservice, one CronJob per worker type, one ConfigMap for configuration.
- `examples/` YAML fixture format: unchanged.
- `internal/examples/` Go loader: reads the same YAML format, calls the Go API/DB layer directly to populate the database.

---

## 7. What Doesn't Change

The following are language-agnostic and are not affected by the Go transition:

- **The database schema.** SQL is portable. The DDL in `migrations/` is identical to the Alembic-generated SQL, modulo formatting.
- **The bundle JSON contract.** The canonical bundle JSON shape (§3.4 of the design spec) is unchanged. Downstream LLM consumers, the MCP tools, and the REST API all produce and consume the same JSON structure.
- **The source-existence guarantee.** `raw_text` + `archive_url` + `content_hash` as the evidentiary record; `source_verifications` as the append-only audit log; `raw_html` zlib-compressed; excerpt-offset check before every `statement_sources` INSERT.
- **The tenant isolation guarantees.** RLS policies are SQL; they are unchanged. The Go application layer enforces the same `SET LOCAL app.tenant_id` contract as the Python layer.
- **The six-stage pipeline.** Collect → Archive → Extract → Corroborate → Verify → Relate. Stage boundaries, status transitions, and invariants are unchanged.
- **The example domains and YAML formats.** `examples/ai-startup-tracking/`, `political-research/`, `pharma-trial-monitoring/`, `investigative-journalism/` and their fixture formats are unchanged.
- **The Wayback SPN2 submission behavior.** Best-effort, 3 retries, archive failure is not a blocker.
- **The confidence formula.** Deterministic independent-source-count formula; ceilings at 0.50 / 0.85 / 0.95; LLM never sets confidence.
- **The property vocabulary model.** Strict vs. permissive mode, `proposed_properties` queue, `v_conflicts` view.
- **The embedding model and dimensions.** BGE-M3, 1024 dimensions, HNSW indices on all four tables.
- **The `doctor` output format.** Seven checks, same output structure; Go implementation is a clean port.

---

## 8. The Python Embedder Microservice Contract

The embedder microservice is the one bounded Python component in the deploy stack. Its contract is stable and must not be changed without a versioned protocol bump.

### 8.1 HTTP Endpoint

```
POST /embed
Content-Type: application/json

Request:
{
  "texts": ["string one", "string two", ...]
}

Response (200 OK):
{
  "vectors": [
    [1024 floats],
    [1024 floats],
    ...
  ]
}
```

- One vector per input text. The response array length always equals the request `texts` array length.
- Vectors are `float32` encoded as JSON numbers. The Go `embedclient` decodes them as `[]float32`.
- Maximum batch size: 64 texts per request. Larger batches must be split by the caller.

### 8.2 Health Check

```
GET /healthz

Response (200 OK — model loaded):
{
  "status": "ok",
  "model": "BAAI/bge-m3"
}

Response (503 Service Unavailable — model loading):
{
  "status": "loading",
  "model": "BAAI/bge-m3"
}
```

The K8s readiness probe polls `GET /healthz` with a `StartPeriod` of 120 seconds to accommodate model loading time. The `factvault doctor` command checks `/healthz` as check [5/7].

### 8.3 Model and Dimensions

- Model: `BAAI/bge-m3` loaded via `sentence-transformers`.
- Output dimension: **1024**. This matches the `vector(1024)` columns in the schema.
- The model is loaded once at startup and pinned in memory for the lifetime of the process.

### 8.4 Environment Variables

| Variable          | Default           | Purpose                                      |
|-------------------|-------------------|----------------------------------------------|
| `EMBEDDER_MODEL`  | `BAAI/bge-m3`     | HuggingFace model ID to load                 |
| `EMBEDDER_PORT`   | `8080`            | Port to listen on                            |
| `EMBEDDER_HOST`   | `0.0.0.0`         | Bind address                                 |

The Go workers reach the embedder at `FACTVAULT_EMBEDDER_URL` (default `http://embedder:8080`).

### 8.5 Deployment

**docker-compose:** one service named `embedder`, built from `services/embedder/Dockerfile`.

**K8s:** one Deployment named `factvault-embedder` with `replicas: 1`. One Service named `factvault-embedder` exposing port 8080. Resource requests: `cpu: 500m, memory: 4Gi` (BGE-M3 model is ~2.2 GB in float32). A separate PVC mounts the model cache at `/root/.cache/huggingface` to avoid re-downloading the model on pod restart.

**Dockerfile pattern:**
```dockerfile
# Build stage: sentence-transformers installation
FROM cgr.dev/chainguard/python:latest-dev AS builder
WORKDIR /app
COPY pyproject.toml .
RUN pip install --prefix=/install .

# Runtime stage: minimal wolfi-base
FROM cgr.dev/chainguard/wolfi-base AS runtime
COPY --from=builder /install /usr/local
COPY app.py /app/app.py
USER 65532
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["python", "/app/app.py"]
```

### 8.6 Go Embed Client

The `internal/embedclient/` package provides a typed Go client for the embedder:

```go
// internal/embedclient/client.go
type Client struct {
    BaseURL string
    http    *http.Client
}

func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
    // POST to BaseURL/embed
    // Validate len(response.Vectors) == len(texts)
    // Return [][]float32
}
```

The workers import `embedclient.Client` directly. The embedder URL is injected from config.

---

## 9. Migration Sequence (Plan of Record)

### Phase 1 — This PR

1. Write this Go transition spec (`docs/superpowers/specs/2026-05-22-go-transition.md`). ✓
2. Update the factvault design spec to reflect Go (§6 operational shape + §7 repository layout).
3. Write the Plan 1 Go rewrite spec.
4. Execute Plan 1 Go rewrite: goose migrations, pgx pool + rls.go, sqlc setup, dockertest integration test harness.
5. Scaffold the Python embedder microservice (`services/embedder/`): `app.py`, `pyproject.toml`, `Dockerfile`.
6. Delete Python Plan 1 code from main.

**Success criteria for Phase 1:**
- `factvault migrate up` runs all goose migrations against a live Postgres.
- `go test ./internal/db/...` passes against a dockertest Postgres container.
- `factvault doctor` passes checks [1/3] (DB reachable, pgvector loaded, RLS applied).
- Python embedder `GET /healthz` returns `{"status": "ok", "model": "BAAI/bge-m3"}` in docker-compose.

### Phase 2 — Plan 2 Go Rewrite

1. Write the Plan 2 Go rewrite spec (collectors, archive worker, verify worker, CLI).
2. Execute: `internal/collectors/`, `internal/workers/collect.go` + `archive.go` + `verify.go`, Cobra subcommands.
3. Delete Python Plan 2 code from main.
4. End-to-end smoke test: `factvault worker collect --config config/collectors/test.yaml` → sources table populated.

**Success criteria for Phase 2:**
- `factvault worker collect` ingests from RSS and HTTP collectors.
- `factvault worker archive` populates `raw_text` and submits to Wayback.
- `factvault worker verify` writes `source_verifications` rows.
- `go test ./tests/integration/...` passes with a dockertest Postgres.

### Phase 3 — Plans 3, 4, 5 Go Rewrites

1. Refile Codex Issues #25–#57 as Go-flavored briefs (replacing Python assumptions).
2. Plan 3: extractors, LLM client, corroborate worker, confidence computation.
3. Plan 4: chi API, go-sdk MCP server, JWT auth, bundle assembler.
4. Plan 5: docker-compose stack update, K8s manifests, example domain loader.
5. End-to-end demo: `factvault example load examples/ai-startup-tracking` → `factvault worker collect` → `factvault api` → `GET /entities/{id}/dossier` returns a sourced bundle.

---

## 10. What to Do With Existing Python Code on Main

### Plan 1 Python Code

Files on main: `factvault/db/` (models.py, session.py, rls.py), `alembic/`, `alembic.ini`, `conftest.py`, `pyproject.toml` (partially).

**Action:** Deleted as part of Plan 1 Go execution (Phase 1). Not archived or kept alongside the Go code. The SQL DDL content of the Alembic migration files is preserved by copying it into goose migration files; the Python wrapper is discarded.

**Commit message pattern:** `chore: delete Plan 1 Python code — replaced by Go (goose + pgx + dockertest)`

### Plan 2 Python Code

Files on main: `factvault/workers/` (collect.py, archive.py, verify.py), `factvault/collectors/`, `factvault/cli/`, `tests/` (pytest suite), `pyproject.toml` (worker entry points).

**Action:** Deleted as part of Plan 2 Go execution (Phase 2). Not kept alongside Go code.

**Commit message pattern:** `chore: delete Plan 2 Python code — replaced by Go workers + Cobra CLI`

### Plan 3 T10 Python Vocab Resolver on `feat/fact-pipeline`

File: `factvault/vocabulary/` on `feat/fact-pipeline` branch.

**Action:** Branch abandoned. Do not merge. Do not attempt to port the Python code. The Go Plan 3 vocabulary resolver is written from scratch using the design spec as the source of truth.

**Branch disposition:** Close the `feat/fact-pipeline` PR (if open) with a comment referencing this transition spec. The branch remains in git history but receives no further commits.

### What Stays

`services/embedder/` is Python and is permanent. It is not deleted.

---

## 11. Risks and Known Unknowns

### Risk 1 — `modelcontextprotocol/go-sdk` Maturity

**Risk:** `github.com/modelcontextprotocol/go-sdk` is a community-maintained library. It is less battle-tested than the Python MCP SDK. API surface may change between minor versions; some edge cases in the MCP protocol may not be implemented.

**Mitigation:** Pin the go-sdk version in `go.mod`. Write integration tests against the three MCP tool endpoints before Phase 3 completion. If go-sdk proves insufficient, the MCP server can be hand-rolled: the MCP protocol over stdio is a well-specified JSON-RPC 2.0 dialect, and the three tools (`entity_lookup`, `story_query`, `fact_query`) have simple request/response shapes that do not require complex protocol features.

**Budget:** If go-sdk is blocking during Plan 4 execution, allocate up to one sprint (2–3 days) to hand-roll the MCP stdio server before escalating to the founder.

### Risk 2 — `go-readability` vs. trafilatura

**Risk:** `go-readability` implements Mozilla Readability, which is designed for article extraction from news-style pages. It is less sophisticated than trafilatura on paywalled pages, aggressively JS-rendered single-page applications, and non-article content (forum threads, government documents, SEC filings).

**Mitigation:** Document as a known v1 limitation in the `factvault doctor` output and the operator guide. For the four example domains (AI startup tracking, political research, pharma, investigative journalism), the primary source types (press releases, news articles, SEC filings in HTML) are well-handled by go-readability. If a deployment encounters a high rate of extraction failures, the embedder service already demonstrates the pattern for calling a Python microservice; a Python trafilatura microservice can be added on the same pattern without changing the Go worker interface.

**v2 path:** Trafilatura microservice at `services/extractor/` with `POST /extract` → `{raw_html: string}` → `{raw_text: string}`. The Go archive worker calls it the same way it calls the embedder. This is a v2 scope item.

### Risk 3 — `pgvector-go` Type Registration

**Risk:** The `pgvector-go` custom type registration must be called in `AfterConnect` on the pgx pool. Missing this registration causes a runtime error when scanning `vector(N)` columns. This is easy to get right but catastrophic when missed (panics in production).

**Mitigation:** The `NewPool` function in `internal/db/conn.go` is the single entry point for all pool construction. It is the only place where `pgxvec.RegisterTypes` is called. Tests that scan vector columns will fail loudly if registration is missing, catching the issue in CI before production.

**Pattern is documented in §4.4 above.** All agents writing code that touches vector columns must read §4.4 before writing pool construction code.

### Risk 4 — dockertest vs. testcontainers-go

**Background:** Two popular libraries exist for test containers in Go: `github.com/ory/dockertest/v3` and `github.com/testcontainers/testcontainers-go`.

**Decision:** This spec picks **dockertest**. Rationale: dockertest uses stdlib patterns (no external daemon, direct Docker API interaction), is lower overhead, and aligns with the general preference for stdlib-adjacent dependencies. testcontainers-go is more featureful and has a larger ecosystem, but the additional features (modules, compose support) are not needed for the factvault test pattern (one Postgres container per test package).

**This decision is final.** Do not re-litigate in plan specs. If a specific integration test requires testcontainers-go features, file an issue before changing the test harness.

### Risk 5 — Codex Issues #25–#57 Python Assumptions

**Risk:** Issues #25–#57 were filed against the Python implementation. Their briefs contain Python-specific language (pytest fixtures, SQLAlchemy session patterns, Pydantic models, asyncpg pool usage). If these issues are executed without refiling as Go-flavored briefs, agents will implement Python code in a Go project.

**Mitigation:** Phase 3 begins with a bulk-refile pass: every open Codex issue is updated with a Go-flavored brief. This is a mandatory pre-step before dispatching any Plan 3+ implementation agents. See §9, Phase 3, step 1.

---

*End of Go Transition Spec.*
