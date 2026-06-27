# Factvault Architecture v1.0

**Date:** 2026-05-23  
**Status:** Active — Phase 1 execution begins immediately  
**Author:** Peter Simmons  
**Scope:** Cross-cutting architecture specification for all subsystems

---

## 1. Purpose & Scope

This document defines the load-bearing architectural constraints for factvault as a complete system. It is not a language spec, a toolchain guide, or an implementation plan — those live in `go-transition.md` and per-plan specs. This spec codifies three foundational decisions and their implications across all six pipeline stages, all deployment tiers, and all operator personas.

**What this spec covers:**
- Three architectural constraints that shape all downstream design
- Logical component boundaries and inter-component contracts
- Database abstraction and multi-tenant isolation strategy
- Deployment paths and operational surface
- Backward-compatibility with Plans 1–3

**What this spec does NOT cover:**
- Language choice (see go-transition.md)
- Individual implementation task breakdowns (see per-plan specs)
- Detailed SQL schema (see migrations/)
- Performance tuning, caching policies, or observability tooling

---

## 2. Architectural Pillars (Load-Bearing Constraints)

### Pillar A: Local-First, Frontier-Optional

**Constraint:** factvault must run end-to-end on local infrastructure with zero external API keys required.

**Definition:**
- All inference runs locally: local embedder (BGE-M3 via Python microservice), local LLM extractor (olla/Qwen3-32B via stdlib HTTP).
- All storage is local: Postgres on localhost (or in-container), local Wayback snapshot storage (if enabled).
- All archiving is best-effort: Wayback SPN2 submission is optional and never blocks a source INSERT.
- Privacy boundary: No fact content, raw_text, or embedding vectors leave the host unless the operator explicitly routes them via config.

**Frontier models as plugins:** Anthropic, OpenAI, and Google LLM endpoints are PLUG-IN options. A single config flag routes extract worker calls to a remote frontier model instead of the local LLM. No code changes required — the plugin point is `LLMClient` interface (§4).

**Implication:** Default-case operators never see a login screen, an API key prompt, or a bill. The system is not opt-out-friendly with frontier models; it is opt-in-only.

### Pillar B: Replaceable Database

**Constraint:** The data layer is abstracted behind a `Store` interface. Postgres + pgvector is production default; SQLite + sqlite-vec is first-class for single-machine deploys; in-memory is first-class for tests.

**Definition:**
- `Store` interface abstracts schema, queries, and RLS.
- `VectorStore` interface abstracts embedding search across backends.
- Migrations are per-backend: goose for Postgres, native runner for SQLite.
- Tenant isolation (RLS) becomes interface-level: Postgres uses GUC + RLS policies; SQLite uses predicate filtering; in-memory uses namespace.

**Implication:** A single source is written once, versioned once, and executed against three backends. Operators can pick the backend that fits their footprint (Postgres for >1M facts, SQLite for <100k facts, in-memory for integration tests).

### Pillar C: Minimal Operator Skill Barrier

**Constraint:** The default path requires Docker and nothing else. No required external keys, no required Postgres admin, no required Kubernetes.

**Definition:**
- Tier 1 (default): `docker compose up -d` → working factvault in 2 minutes.
- Tier 2 (advanced): K8s + Helm for production clusters.
- Tier 3 (opt-in): bare metal / external managed Postgres (unsupported but documented).

**Implication:** On-boarding curve is zero. The `factvault doctor` command runs seven checks and exits 0 on success; every check links to a remediation command if it fails.

---

## 3. Logical Components

```
┌─────────────────────────────────────────────────────────────┐
│  Collectors (RSS, HTTP, Sitemap, Searxng, Upload, CDX)     │
└────────────────┬────────────────────────────────────────────┘
                 │ [raw_html, url, content_hash]
                 ▼
┌─────────────────────────────────────────────────────────────┐
│  Archive Worker (readability, Wayback SPN2, compress)       │
└────────────────┬────────────────────────────────────────────┘
                 │ [raw_text, archive_url, zlib(raw_html)]
                 ▼
┌─────────────────────────────────────────────────────────────┐
│  Extract Worker                                             │
│  ├─ Deterministic extractors (identifiers, money, dates)    │
│  ├─ LLM extractor (OpenAI-compatible endpoint)              │
│  ├─ Offset-verification gate (anti-hallucination)           │
│  └─ Vocabulary resolver (strict / permissive mode)          │
└────────────────┬────────────────────────────────────────────┘
                 │ [statements, statement_sources]
                 ▼
┌─────────────────────────────────────────────────────────────┐
│  Corroborate Worker (confidence recompute)                  │
└────────────────┬────────────────────────────────────────────┘
                 │ [updated statement.confidence]
                 ▼
┌─────────────────────────────────────────────────────────────┐
│  Verify Worker (daily CronJob, append-only audit)           │
└────────────────┬────────────────────────────────────────────┘
                 │ [source_verifications (immutable)]
                 ▼
┌─────────────────────────────────────────────────────────────┐
│  Relate Worker (entity edges from statement values)          │
└────────────────┬────────────────────────────────────────────┘
                 │ [relations]
                 ▼
┌─────────────────────────────────────────────────────────────┐
│  Dossier Worker (nightly pre-assembly for registered ents)  │
└────────────────┬────────────────────────────────────────────┘
                 │ [dossiers (cache)]
                 ▼
┌─────────────────────────────────────────────────────────────┐
│  REST API / MCP Server (retrieval layer)                    │
│  ├─ GET /entities/{id}/dossier                              │
│  ├─ POST /stories (on-demand assembly)                      │
│  └─ MCP tools (entity_lookup, story_query, fact_query)      │
└─────────────────────────────────────────────────────────────┘
```

**Contract per stage:**
1. **Collect:** Sources are idempotent on (tenant_id, url). Raw HTML stored, never re-fetched.
2. **Archive:** raw_text extracted via go-readability. Wayback best-effort, never blocking.
3. **Extract:** Deterministic first, LLM second, offset-verification gate mandatory, vocabulary resolution per config.
4. **Corroborate:** Confidence recomputed from scratch on every run.
5. **Verify:** Daily CronJob, append-only, no source ever deleted.
6. **Relate:** Entity edges from statement value references.
7. **Dossier:** Nightly pre-assembly for registered entities.
8. **Retrieval:** REST API and MCP server both read from the same assembler.

---

## 4. Local-First / Frontier-Optional Interface

### LLMClient Interface

```go
type LLMClient interface {
    // Extract sends a JSON schema prompt to the configured LLM endpoint.
    // The endpoint is OpenAI-compatible (chat/completions + response_format).
    Extract(ctx context.Context, source *db.Source, rawText string) ([]StatementProposal, error)
}
```

**Implementations:**

| Name              | Backend                      | Config Key                                         | Cost | Latency    |
|-------------------|------------------------------|----------------------------------------------------|------|------------|
| LocalLLMClient    | Ollama (Qwen3-32B)           | `llm_local_base_url` (default: localhost:11434)   | $0   | 2-8s       |
| AnthropicClient   | Claude 3.5 Sonnet API        | `llm_anthropic_api_key`                            | $3k  | 200ms      |
| OpenAIClient      | GPT-4o API                  | `llm_openai_api_key`                               | $2k  | 150ms      |
| GoogleClient      | Gemini 2.0 Flash API         | `llm_google_api_key`                               | $1k  | 300ms      |

**Default:** LocalLLMClient (no key required). Operator selects frontier model via single config key.

### EmbeddingClient Interface

```go
type EmbeddingClient interface {
    // Embed returns a [][]float32 for the given texts.
    // All implementations must use the same model (BGE-M3, 1024 dimensions) to preserve embedding space.
    Embed(ctx context.Context, texts []string) ([][]float32, error)
}
```

**Implementations:**

| Name                      | Backend                                 | Config Key                          | Note |
|---------------------------|-----------------------------------------|-------------------------------------|------|
| LocalEmbedderClient       | Python microservice (sentence-transformers) | `embedder_url` (default: http://embedder:8080) | Default; runs in-container |
| RemoteEmbedderClient      | Voyage, Cohere, Anthropic               | provider-specific key               | Opt-in only; must preserve 1024-dim BGE-M3 space |

**Default:** LocalEmbedderClient. The Python embedder is bundled in docker-compose.

### Configuration Scheme

```yaml
# config.yaml — operators omit most keys; these are the defaults
llm:
  # PLUGIN POINT: route LLM calls to frontier model
  type: local  # or: anthropic, openai, google
  base_url: http://localhost:11434/v1  # for type: local
  # api_key: "${FACTVAULT_LLM_API_KEY}"  # set for frontier models
  model: qwen2.5-32b  # for type: local; ignored for frontier

embeddings:
  # PLUGIN POINT: route embedding calls to remote service (rare)
  type: local  # or: voyage, cohere, etc.
  url: http://embedder:8080  # for type: local
  # api_key: "${FACTVAULT_EMBEDDER_API_KEY}"  # if remote

# Worker config (per-stage control)
workers:
  extract:
    llm_backend: local  # override global if needed
    vocabulary_mode: strict  # or: permissive
    deterministic_only: false  # if true, skip LLM entirely

  archive:
    wayback_enabled: true  # best-effort; never blocking
    wayback_retries: 3

# Database config (see §5)
database:
  driver: postgres  # or: sqlite, memory
  dsn: "postgres://..."

# Operational config
tenants:
  - id: "default"
    name: "Default Workspace"
    properties_vocab_file: config/properties-default.yaml

logging:
  level: info
  format: json
```

**Privacy guarantee:** If `llm.type: local` and `embeddings.type: local`, zero bytes of fact content, raw_text, or vectors egress the host. Network sniffing outside the container network reveals zero proprietary data.

---

## 5. Store Abstraction

### Store Interface

```go
type Store interface {
    // Entity operations
    CreateEntity(ctx context.Context, tenant pgtype.UUID, label string) (*Entity, error)
    GetEntity(ctx context.Context, tenant pgtype.UUID, id pgtype.UUID) (*Entity, error)
    ListEntities(ctx context.Context, tenant pgtype.UUID, limit int) ([]Entity, error)

    // Property operations
    GetProperty(ctx context.Context, slug string) (*Property, error)
    ListProperties(ctx context.Context) ([]Property, error)
    CreateStatement(ctx context.Context, stmt *Statement) error

    // ... full CRUD for entities, properties, statements, sources, etc.
}
```

### VectorStore Interface

```go
type VectorStore interface {
    // SearchNearest returns the top-k entities nearest to the query vector in embedding space.
    SearchNearest(ctx context.Context, tenant pgtype.UUID, embedding []float32, k int) ([]EntityWithScore, error)
}
```

### Backend Matrix

| Backend    | Table Schema | Vector Search | RLS Strategy         | Migration Tool | Test Fixture | Footprint |
|-----------|---|---|---|---|---|---|
| Postgres   | pgvector type | HNSW indices | GUC + row policies   | goose (SQL)    | dockertest   | 100MB - ∞  |
| SQLite     | native BLOB | sqlite-vss    | predicate filtering  | native runner  | tmpdir       | <100MB     |
| In-Memory  | Go maps      | FAISS-style  | namespace isolation  | none           | direct       | <10MB      |

### Migration Strategy

**Postgres:**
- Migrations live in `migrations/00001_initial_schema.sql`, `00002_hnsw_indices.sql`, etc.
- goose is the runner: `factvault migrate` executes them.
- Backward compat: all migrations are reversible (goose Down blocks included).

**SQLite:**
- Migrations are embedded Go functions in `internal/migrations/sqlite.go`.
- Auto-apply on first pool creation if schema doesn't exist.
- No `factvault migrate` subcommand needed; pools self-initialize.

**In-Memory:**
- No persistence; no migrations. Schema created at pool startup via reflection.

### Tenant Context Implementation

**Postgres (RLS):**
```sql
SET LOCAL app.tenant_id = 'uuid-here';
SELECT * FROM entities;  -- row-level security filters silently
```

**SQLite (predicate filtering):**
```go
// queries.go
type Queries struct {
    tenantID pgtype.UUID  // injected per call
}

func (q *Queries) ListEntities(ctx context.Context) ([]Entity, error) {
    // All queries include: WHERE tenant_id = q.tenantID
    return q.db.QueryContext(ctx, 
        "SELECT * FROM entities WHERE tenant_id = ?", q.tenantID)
}
```

**In-Memory (namespace):**
```go
type InMemStore struct {
    data map[pgtype.UUID]map[string]Entity  // [tenant_id][entity_id]
}
```

---

## 6. Deploy Paths

### Tier 1: Docker Compose (Default)

**Target:** Single-machine operators, developers, research teams. Zero external dependencies.

**What `docker compose up` includes:**
- `postgres:16` with pgvector extension
- `factvault:latest` (Go binary, all subcommands)
- `factvault-embedder:latest` (Python microservice)
- `ollama:latest` (local LLM, Qwen3-32B pre-pulled)

**Operating model:**
```bash
docker compose up -d                 # Start all services
factvault doctor                     # Verify health
factvault worker collect             # Poll collectors
factvault worker extract             # Run extractors
factvault api                        # Start REST server on :8080
```

**Scaling:** Workers are stateless and can run as separate containers via `docker compose run`.

### Tier 2: Kubernetes + Helm

**Target:** Production deployments, multi-tenant hosts, compliance environments.

**Helm chart includes:**
- `factvault-api` Deployment
- `factvault-embedder` Deployment + PVC (model cache)
- CronJob per worker (collect, archive, extract, corroborate, verify, relate, dossier)
- ConfigMap for config.yaml
- Postgres StatefulSet (or external managed Postgres)
- Service, Ingress, RoleBinding for RBAC

**Operating model:**
> **Note:** The Helm chart is not included in this repository. Use the `deploy/k8s/` manifests directly.

```bash
# Example (chart not included — use deploy/k8s/ manifests):
# helm install factvault ./helm/factvault \
#   --values values-prod.yaml \
#   --set database.external=true \
#   --set llm.type=anthropic
```

### Tier 3: Bare Metal (Unsupported)

**Target:** Advanced operators, custom deployments, air-gapped environments.

**Documented but unsupported:** All components run as systemd services or native binaries. RLS still enforced at the Postgres level; all other guarantees held. No dedicated support; operators follow Tier 2 (Helm) docs and adapt.

---

## 7. Configuration

### YAML Schema (Canonical Reference)

Configuration is managed via environment variables; see docs/operator-guide.md § Environment variables. Key config areas:

- `llm.*` — LLM backend and model selection
- `embeddings.*` — embedding service backend
- `database.*` — database driver, DSN, migration settings
- `workers.*` — per-stage control (enabled, retry count, vocabulary mode)
- `logging.*` — log level, format, output
- `tenants.*` — multi-tenant configuration

### Environment Variable Precedence

All keys accept env var overrides via `${VAR_NAME}` interpolation:
```yaml
database:
  dsn: "${FACTVAULT_DATABASE_URL}"  # replaced at load time
llm:
  api_key: "${FACTVAULT_LLM_API_KEY}"
```

### Reasonable Defaults

Every knob has a sensible default so operators can start with minimal config:
```yaml
# Minimal config — everything else uses defaults
database:
  driver: postgres
```

Operators can omit:
- `llm.*` — defaults to local Ollama at localhost:11434
- `embeddings.*` — defaults to local Python microservice
- `workers.*` — defaults enable all stages
- `logging.*` — defaults to info level, JSON format

---

## 8. Required vs. Optional Dependencies

### Required (Zero External Keys)

- Docker (for Tier 1)
- Postgres (bundled in compose) or SQLite (bundled in binary)
- Python 3.11+ (only for embedder microservice; bundled in Tier 1)

### Auto-Bootstrapped

- Postgres schema (migrations run on startup)
- BGE-M3 model (auto-downloaded by embedder on first run)
- Qwen3-32B model (auto-pulled by Ollama on first run, Tier 1 only)

### Opt-In

- Anthropic API key (frontier LLM option)
- OpenAI API key (frontier LLM option)
- Google API key (frontier LLM option)
- External managed Postgres (Tier 2)
- External Wayback Machine (best-effort, disabled by default)
- Kubernetes (Tier 2)

---

## 9. Operational Surface

### Single Binary Subcommands

```bash
factvault doctor                                    # 7-check health + remediation
factvault worker {collect,archive,extract,corroborate,verify,relate,dossier}
factvault api                                       # Start REST server (port 8080)
factvault mcp                                       # Start MCP server (stdio)
factvault example <domain>                          # Load example data
factvault migrate                                   # Run goose migrations
```

### Health Checks (doctor output)

```
[1/7] Database reachable ......................... OK
[2/7] pgvector extension loaded ................. OK
[3/7] RLS policies applied ...................... OK
[4/7] Wayback API reachable ..................... OK (or SKIP)
[5/7] Embedding model loadable .................. OK (BGE-M3 / 1024d)
[6/7] LLM endpoint responding ................... OK (http://localhost:11434/v1)
[7/7] Canary fact ingest end-to-end ............ OK

All checks passed. Deployment is ready.
```

### Logging

All output is structured JSON via stdlib `log/slog`:
```json
{"time":"2026-05-23T10:30:45Z","level":"INFO","msg":"source archived","source_id":"123e4567-...","content_hash":"a3f2c1d4...","worker":"archive"}
```

---

## 10. Backward-Compat with Plans 1–3

### What Plans 1–2 Already Implement

| Component | Status | Notes |
|-----------|--------|-------|
| Database schema (9 tables, RLS, v_conflicts view) | Merged on main | Unchanged by Go rewrite |
| Source collectors (RSS, HTTP, Sitemap) | Merged on main | Go rewrite in Plan 2 |
| Archive worker (readability, Wayback) | Merged on main | Go rewrite in Plan 2 |
| Deterministic extractors | Merged on main (Plan 3) | Go rewrite in Plan 3 |
| Vocabulary resolver | Partial (Plan 3 T10, Python) | Abandoned; Go rewrite in Plan 3 |
| LLM extractor | Spec only | Go rewrite in Plan 3 |
| Confidence formula | Spec only | Go rewrite in Plan 3 |

### Gaps Requiring Follow-Up Issues

1. **Store abstraction:** Current code uses pgx directly. `internal/db/querier.go` (sqlc-generated) must be wrapped behind a `Store` interface.
2. **SQLite backend:** No SQLite implementation exists yet. Phase B work.
3. **Frontier model plugins:** Config layer exists, but `LLMClient` interface must be exposed as pluggable.
4. **docker-compose template:** Exists in partial form; needs Python embedder + Ollama services added.

---

## 11. Migration Roadmap

### Phase A: Store Interface Extraction (Week 1–2)

- [x] Goose migrations (complete in Plan 1)
- [ ] Wrap sqlc-generated `Queries` struct behind `Store` interface
- [ ] Expose `WithPool` and `TenantContext` (complete in Plan 1)
- [ ] Verify zero behavioral change; tests pass

**Deliverable:** Every `internal/db/queries.New(tx).MethodName()` call is callable via `store.MethodName()` interface.

### Phase B: SQLite Backend (Week 3–4)

- [ ] `internal/db/sqlite/backend.go` implementing `Store` interface
- [ ] Query predicates include `WHERE tenant_id = ?` for all RLS
- [ ] In-memory backend for unit tests
- [ ] Verification test: same test suite passes against Postgres, SQLite, and in-memory

**Deliverable:** Operators can swap `database.driver: sqlite` in config.yaml and get identical behavior.

### Phase C: LLMClient / EmbeddingClient Plugins (Week 5–6)

- [ ] Extract `internal/extractors/llm.go` client behind `LLMClient` interface
- [ ] Implement `LocalLLMClient`, `AnthropicClient`, `OpenAIClient`, `GoogleClient`
- [ ] Config loader wires the appropriate client based on `llm.type`
- [ ] Verification: extract worker runs with each backend, produces identical AST

**Deliverable:** Operators can set `llm.type: anthropic` and extract worker calls Anthropic; `llm.type: local` calls Ollama.

### Phase D: docker-compose Polish (Week 7–8)

- [ ] `docker-compose.yml` with Postgres + embedder + Ollama + factvault API
- [ ] Services health-check each other
- [ ] Default config.yaml shipped with the compose file
- [ ] Verification: `docker compose up -d && factvault doctor` exits 0 in <2 min

**Deliverable:** Operators run one command; system is ready in 2 minutes.

### Phase E: Documentation & Examples (Week 9–10)

- [ ] Getting Started: 5-minute walkthrough loading an example domain
- [ ] Architecture guide: this spec + visual diagrams
- [ ] Operator runbooks: docker-compose, K8s, upgrade paths
- [ ] Verification: docs match the system; walkthrough succeeds on clean machine

**Deliverable:** New operators land a working dossier from a sample domain in <10 minutes.

---

## 12. Open Questions

1. **Frontier model cost guardrails:** Should `factvault` emit a cost estimate before extracting 10k+ sources via Anthropic? (Impacts Phase C config, not architecture.)

2. **SQLite schema sync:** When the Postgres schema evolves (new table, new index), how is SQLite kept in sync? (Impacts Phase B: do we hand-port or generate SQL-to-Go migrations?)

3. **Embedding space migration:** If we ever need to upgrade from BGE-M3 to a larger or different model, what's the non-destructive recompute strategy? (Doesn't block v1.0; candidate for v1.1.)

4. **Multi-tenant isolation on Tier 3:** If an operator runs bare-metal without RLS (PostgreSQL RLS disabled), how do we ensure tenant isolation is still enforced? (Documented as unsupported; no blocker for v1.0.)

5. **Wayback submission across frontier models:** If the LLM backend is frontier (Anthropic/OpenAI), should we still submit to Wayback? (Config question; default is yes. Implication: archive URLs are always local Wayback, not external.)

6. **Scaling the relations worker:** With large fact counts, the relate worker (entity → entity edges) could be CPU-heavy. Should it be shardable by entity_id? (Candidate for v1.2; document as sequential-only in v1.0.)

---

## 13. References

- **go-transition.md** — Language and toolchain decisions
- **2026-05-22-schema-and-migrations-go.md** (Plan 1) — Database layer specifics
- **2026-05-22-fact-pipeline-go.md** (Plan 3) — Extract worker and confidence formula
- **2026-05-22-factvault-design.md** — Full system design (all four pillars, six stages, bundle JSON)
- **AGENTS.md** — Conventions for agents working on factvault
- **CLAUDE.md** — Founder's core principles (QC.2: simplicity, no laziness, minimal impact)

---

**End of Factvault Architecture v1.0**
