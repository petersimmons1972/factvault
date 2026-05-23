# Bundle and Retrieval Go Implementation Plan

**Date:** 2026-05-23
**Status:** Implementation (Plan 4 in Go)
**Depends on:** Plan 3 (fact pipeline Go) — merged to main
**Authority:** `docs/superpowers/specs/2026-05-22-go-transition.md`

---

## Overview

This document specifies the Go implementation of Plan 4: Bundle and Retrieval. It supersedes the Python Plan 4 spec (`2026-05-22-bundle-and-retrieval.md`) for Go-native code.

The plan produces the canonical JSON bundle assembly function and the REST API + MCP server retrieval surface:

1. **Bundle assembler** — `assemble(ctx, tx, entity_ids, depth, tenant_id) -> Bundle` produces both dossier (depth=0) and story (depth=1/2/3) JSON bundles from the database.
2. **HTTP API (chi)** — REST endpoints for entity dossier lookup, story queries, and structured fact queries, with JWT middleware for tenant isolation.
3. **JWT middleware** — Validates RS256 tokens using a public key, extracts `tenant_id`, and enforces RLS via `SET LOCAL app.tenant_id`.
4. **MCP server** — Exposes three tools (dossier, story, search) via the MCP protocol.
5. **Dossier worker** — Periodic pre-computation job that assembles and caches dossiers in the `dossiers` table.
6. **Concept doc update** — Rewrite `docs/concepts/dossiers-vs-stories.md` to reflect Go `pgx.Tx` semantics instead of Python SQLAlchemy.

---

## Package Layout

```
internal/
  api/
    api.go                                # chi router, request/response structs
    auth.go                              # JWT verification middleware
    middleware.go                        # Tenant context injection
  assembler/
    bundle.go                            # assemble() function, Bundle struct
    graph.go                             # Recursive CTE graph expansion (delegated to database)
  workers/
    dossier.go                           # Dossier pre-computation worker
  mcp/
    server.go                            # MCP server implementation
```

---

## 1. Locked Implementation Rules

These rules override any conflicting guidance:

1. **Language:** Go 1.23+ (project minimum).
2. **Web Framework:** `github.com/go-chi/chi/v5` — chi router for REST API.
3. **Database:** `github.com/jackc/pgx/v5` with `pgvector-go` for vectors. All tenant context via `SET LOCAL app.tenant_id` (RLS enforced at database layer).
4. **JWT:** stdlib `crypto/hmac`, `crypto/sha256`, base64. No external JWT library — parse and validate RS256 by hand using `crypto/rsa` + `crypto/sha256`.
5. **HTTP Middleware:** stdlib `net/http` only. Tenant context via request context (`ctx.Value()`, `ctx.WithValue()`).
6. **MCP Server:** `github.com/modelcontextprotocol/go-sdk` — **demur-worthy component** (see briefing).
7. **Test Database:** Use the existing `testdb` package; all bundle/API tests run inside a transactional test DB.
8. **Single Binary:** All entry points (`api`, `mcp`, `worker dossier`) are cobra subcommands of the main `factvault` CLI.

---

## 2. Core Data Structures

### 2.1 Bundle

The Bundle JSON structure produced by `assemble()`. Identical output for dossier and story.

```go
type Bundle struct {
    EntityID    string                 `json:"entity_id"`      // if depth=0, single entity; if depth>0, first seed
    Entities    []BundleEntity         `json:"entities"`
    Statements  []BundleStatement      `json:"statements"`
    Sources     []BundleSource         `json:"sources"`
    Qualifiers  []BundleQualifier      `json:"qualifiers,omitempty"`
    Relations   []BundleRelation       `json:"relations,omitempty"`
    AssembledAt time.Time              `json:"assembled_at"`
    TenantID    string                 `json:"tenant_id"`
}

type BundleEntity struct {
    ID            string    `json:"id"`
    Name          string    `json:"name"`
    CanonicalName *string   `json:"canonical_name,omitempty"`
    TypeURI       string    `json:"type_uri"`
    Description   *string   `json:"description,omitempty"`
}

type BundleStatement struct {
    ID               string    `json:"id"`
    EntityID         string    `json:"entity_id"`
    PropertySlug     string    `json:"property_slug"`
    Value            string    `json:"value"`
    ValueType        string    `json:"value_type"`
    Rank             int       `json:"rank"`                 // ordinal position, 1 = most confident
    Confidence       float64   `json:"confidence"`           // 0.0–1.0, aggregated from sources
    SourceIDs        []string  `json:"source_ids"`
    QualifierIDs     []string  `json:"qualifier_ids,omitempty"`
}

type BundleSource struct {
    ID                string    `json:"id"`
    URL               string    `json:"url"`
    ArchiveURL        *string   `json:"archive_url,omitempty"`
    PublishedAt       *string   `json:"published_at,omitempty"`      // RFC3339
    VerificationStatus string   `json:"verification_status"`         // "primary", "corroborating", "unverified"
    RawText           string    `json:"raw_text"`                   // Verbatim excerpt
    ExcerptOffsetStart int      `json:"excerpt_offset_start"`
    ExcerptOffsetEnd   int      `json:"excerpt_offset_end"`
}

type BundleQualifier struct {
    ID               string  `json:"id"`
    StatementID      string  `json:"statement_id"`
    PropertySlug     string  `json:"property_slug"`
    Value            string  `json:"value"`
    ValueType        string  `json:"value_type"`
}

type BundleRelation struct {
    ID                   string  `json:"id"`
    SourceEntityID       string  `json:"source_entity_id"`
    TargetEntityID       string  `json:"target_entity_id"`
    RelationTypeSlug     string  `json:"relation_type_slug"`
    Confidence           float64 `json:"confidence"`
}
```

### 2.2 HTTP Request/Response Structures

```go
// GET /entities/{id}/dossier response
type DossierResponse struct {
    Bundle Bundle `json:"bundle"`
    CachedAt *time.Time `json:"cached_at,omitempty"`     // if served from dossiers table
}

// POST /stories request
type StoryRequest struct {
    Query          string  `json:"query"`
    Depth          int     `json:"depth,omitempty"`       // default 2, max 3
    MaxFacts       *int    `json:"max_facts,omitempty"`
    MinConfidence  float64 `json:"min_confidence,omitempty"` // default 0.0
}

type StoryResponse struct {
    Bundle Bundle `json:"bundle"`
}

// POST /facts/query request
type FactsQueryRequest struct {
    Query          string  `json:"query"`
    MaxFacts       *int    `json:"max_facts,omitempty"`
    MinConfidence  float64 `json:"min_confidence,omitempty"`
}

type FactsQueryResponse struct {
    Bundle Bundle `json:"bundle"`
}

// Health check responses
type HealthResponse struct {
    Status string `json:"status"`
}

type ReadyResponse struct {
    Ready bool   `json:"ready"`
    Errors []string `json:"errors,omitempty"`
}
```

### 2.3 JWT Claims

```go
type JWTClaims struct {
    TenantID string    `json:"tenant_id"`
    Subject  string    `json:"sub"`
    IssuedAt int64     `json:"iat"`
    ExpiresAt int64    `json:"exp"`
}
```

---

## 3. Assembler (`internal/assembler/bundle.go`)

The core function that produces bundles.

### 3.1 Function Signature

```go
func Assemble(
    ctx context.Context,
    tx pgx.Tx,
    entityIDs []string,
    depth int,
    tenantID string,
) (*Bundle, error)
```

**Semantics:**
- `ctx` supports cancellation and deadlines.
- `tx` is a database transaction (or connection). RLS is enforced by the database; the caller must have set `SET LOCAL app.tenant_id` before calling.
- `entityIDs` is the seed set. If `len(entityIDs) == 1` and `depth == 0`, this is a dossier assembly. Otherwise, it's a story.
- `depth` controls graph expansion: 0 = only seed entities, 1–3 = recursive CTE with that many hops.
- Returns the canonical Bundle JSON structure or an error.

### 3.2 Algorithm

1. **Load seed entities** from `entities` table. Return error if any ID is not found or not accessible under RLS.
2. **If depth > 0:** Execute recursive CTE (in `internal/assembler/graph.go`) to find all related entities within `depth` hops, gating edges at `confidence >= 0.4`.
3. **Load statements** for all entities in the result set, ordered by `rank` ascending (lower rank = higher confidence).
4. **Load sources** for all statements, including `raw_text` excerpt and character offsets.
5. **Load qualifiers** for all statements (optional; omit if empty).
6. **Load relations** for all statement-backed relationships in the result set.
7. **Compute confidence** for each statement using the `internal/assembler/confidence.go` formula (already implemented in Plan 3).
8. **Assemble Bundle struct** with all entities, statements (sorted by rank), sources, qualifiers, relations.
9. **Marshal to JSON** and return.

### 3.3 Error Handling

- If any entity in the seed set is not found: return `ErrEntityNotFound`.
- If RLS denies access: return `ErrTenantIsolation`.
- If database connection fails: return the underlying `pgx` error.
- If depth is <0 or >3: return `ErrInvalidDepth`.

---

## 4. Graph Expansion (`internal/assembler/graph.go`)

Recursive CTE to find related entities within `depth` hops.

### 4.1 Function Signature

```go
func ExpandGraph(
    ctx context.Context,
    tx pgx.Tx,
    seedEntityIDs []string,
    depth int,
    tenantID string,
) ([]string, error) // Returns all entity IDs (seed + expanded)
```

**Notes:**
- The recursive CTE lives in the database (SQL), not Go code. This function orchestrates the query.
- The CTE gates each edge traversal at `confidence >= 0.4` to prevent low-confidence synthetic edges from polluting results.
- Returns the full set of entity IDs (including seeds) that are reachable within `depth` hops.

### 4.2 SQL Recursive CTE (pseudocode)

```sql
WITH RECURSIVE related_entities AS (
  -- Base: seed entities
  SELECT entity_id FROM (VALUES (...)) AS t(entity_id)
  UNION
  -- Recursive: follow relations at confidence >= 0.4
  SELECT r.target_entity_id
  FROM relations r
  JOIN related_entities re ON r.source_entity_id = re.entity_id
  WHERE r.confidence >= 0.4
  LIMIT depth  -- approximate; actual logic gates per-iteration
)
SELECT DISTINCT entity_id FROM related_entities
```

The actual SQL is more complex (multi-stage recursion with depth tracking).

---

## 5. HTTP API (`internal/api/api.go`)

chi-based REST API with JWT middleware.

### 5.1 Routes

All routes return JSON unless otherwise specified.

| Method | Route | Handler | Auth | Response |
|--------|-------|---------|------|----------|
| `GET` | `/healthz` | health | None | `HealthResponse` (200) |
| `GET` | `/readyz` | readiness | None | `ReadyResponse` (200 or 503) |
| `GET` | `/entities/{id}/dossier` | getDossier | JWT | `DossierResponse` (200, 404, 500) |
| `POST` | `/stories` | postStory | JWT | `StoryResponse` (200, 400, 500) |
| `POST` | `/facts/query` | postFactsQuery | JWT | `FactsQueryResponse` (200, 400, 500) |

### 5.2 Handler Signature

```go
func (s *Server) getDossier(w http.ResponseWriter, r *http.Request) {
    tenantID := r.Context().Value(contextKeyTenantID).(string)
    entityID := chi.URLParam(r, "id")
    // Implementation
}
```

All handlers:
- Extract `tenantID` from the request context (injected by JWT middleware).
- Call `Assemble()` with the appropriate parameters.
- Serialize the result as JSON and write to `w`.

### 5.3 Error Responses

All error responses are JSON:

```go
type ErrorResponse struct {
    Error string `json:"error"`
    Code  string `json:"code"`
}
```

Codes:
- `"entity_not_found"` → HTTP 404
- `"invalid_request"` → HTTP 400
- `"unauthorized"` → HTTP 401
- `"internal_error"` → HTTP 500

---

## 6. JWT Middleware (`internal/api/auth.go`)

Validates RS256 tokens and injects tenant context.

### 6.1 JWTVerifier Struct

```go
type JWTVerifier struct {
    publicKey *rsa.PublicKey
}

func NewJWTVerifier(publicKeyPEM string) (*JWTVerifier, error) {
    // Parse public key from PEM-encoded RSA public key
    // Return *JWTVerifier or error
}

func (v *JWTVerifier) Verify(tokenString string) (*JWTClaims, error) {
    // Parse token as RS256 using crypto/rsa + crypto/sha256
    // Validate signature
    // Extract and return JWTClaims or error
}
```

**Implementation notes:**
- Parse RS256 signature manually: decode base64-urlencoded token parts, compute HMAC-SHA256 over header+payload, compare.
- Actually, RS256 is RSA-PKCS1v15 signature, not HMAC. Use `rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, hash, signature)`.
- Validate `exp` claim against current time; return error if expired.

### 6.2 Middleware Chain

```go
func (s *Server) jwtMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Extract Authorization header (Bearer token)
        // Verify token using JWTVerifier
        // Inject tenant_id into request context via ctx.WithValue()
        // Call next
    })
}
```

Health routes (`/healthz`, `/readyz`) skip JWT middleware.

---

## 7. Tenant Context Middleware (`internal/api/middleware.go`)

Injects tenant context into database operations.

```go
func (s *Server) tenantContextMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        tenantID := r.Context().Value(contextKeyTenantID).(string)
        // Before database operations, execute:
        // conn.Exec(ctx, "SET LOCAL app.tenant_id TO $1", tenantID)
        // This is typically done in each handler right before database calls
        next.ServeHTTP(w, r)
    })
}
```

Typically, the pattern is:
1. JWT middleware injects `tenant_id` into context.
2. Each handler calls `conn.Exec(ctx, "SET LOCAL app.tenant_id TO $1", tenantID)`.
3. Postgres RLS policies read `current_setting('app.tenant_id')` and enforce isolation.

---

## 8. Dossier Worker (`internal/workers/dossier.go`)

Periodic job that pre-computes and caches dossiers.

### 8.1 Function Signature

```go
func RunDossierWorker(ctx context.Context, connPool *pgx.Pool) error
```

### 8.2 Algorithm

1. Query `dossiers` table for entities with stale cache (`assembled_at <= now() - interval '24 hours'`).
2. Fetch all entity IDs that should have a dossier (configurable set, or all entities for the tenant).
3. For each entity:
   a. Begin transaction with `connPool.BeginTx()`.
   b. Set `SET LOCAL app.tenant_id` for the tenant.
   c. Call `Assemble(ctx, tx, [entityID], 0, tenantID)`.
   d. Upsert the result into `dossiers` table (bundle JSONB column).
   e. Commit transaction.
4. Log progress and errors.

### 8.3 Cobra Subcommand

```
factvault worker dossier [--tenant-id=<uuid>] [--entity-id=<uuid>] [--all]
```

Flags:
- `--tenant-id`: Process dossiers for this tenant only.
- `--entity-id`: Pre-compute a specific entity dossier.
- `--all`: Force recomputation of all dossiers (ignore TTL).

---

## 9. MCP Server (`internal/mcp/server.go`)

Exposes three tools via MCP protocol.

### 9.1 Tools

| Tool | Input | Output | Purpose |
|------|-------|--------|---------|
| `dossier` | entity_id (string), depth (int, default 0) | bundle JSON | Pre-computed entity dossier |
| `story` | query (string), depth (int, default 2) | bundle JSON | Graph-expanded story |
| `search` | query (string), entity_limit (int, default 10) | entities [] | Embedding similarity search |

### 9.2 Server Struct

```go
type MCPServer struct {
    connPool *pgx.Pool
    verifier *JWTVerifier
    // Configuration
}

func NewMCPServer(connPool *pgx.Pool, verifier *JWTVerifier) *MCPServer {
    // Initialize
}

func (s *MCPServer) Start(ctx context.Context) error {
    // Start MCP server, register tools, listen for tool calls
}
```

**Note:** The go-sdk API is underdocumented. See briefing for demur strategy.

---

## 10. Test Strategy

All tests use the transactional test database from `internal/testdb`.

### 10.1 Assembler Tests (`internal/assembler/bundle_test.go`)

- **Test dossier assembly (depth=0):** seed 1 entity, verify Bundle contains that entity's statements + sources.
- **Test story assembly (depth=2):** seed 1 entity with 1 relation, verify Bundle includes both entities + shared statements.
- **Test RLS enforcement:** attempt to assemble with wrong tenant_id, verify error or empty result.
- **Test invalid entity ID:** verify ErrEntityNotFound.
- **Test graph expansion:** seed 1 entity with depth=3, verify all reachable entities included.

### 10.2 API Tests (`internal/api/api_test.go`)

- **Test health route (no JWT):** GET /healthz returns 200.
- **Test readiness route (no JWT):** GET /readyz returns 200/503 based on DB state.
- **Test dossier route with valid JWT:** GET /entities/{id}/dossier returns 200 + Bundle.
- **Test dossier route with invalid JWT:** 401 Unauthorized.
- **Test story route with valid JWT:** POST /stories with valid query returns 200 + Bundle.
- **Test tenant isolation:** two tenants' JWTs, verify each sees only their own data.

### 10.3 JWT Tests (`internal/api/auth_test.go`)

- **Test token verification:** valid RS256 token, verify claims extracted.
- **Test expired token:** verify error returned.
- **Test invalid signature:** verify error returned.
- **Test missing tenant_id claim:** verify error returned.

### 10.4 Dossier Worker Tests (`internal/workers/dossier_test.go`)

- **Test dossier pre-compute:** run worker, verify dossiers table populated.
- **Test dossier cache TTL:** verify stale dossiers are refreshed.
- **Test multi-tenant isolation:** two tenants' dossiers, verify no cross-contamination.

### 10.5 Integration Tests (`tests/integration/retrieval_e2e_test.go`)

- **Full pipeline:** source → fact extraction → assembler → API → client.
- Verify end-to-end bundle consistency: entities → statements → sources → confidence.

---

## 11. Concept Doc Rewrite (`docs/concepts/dossiers-vs-stories.md`)

Rewrite §"One assembler, two invocations" to reference Go with `pgx.Tx` semantics instead of Python SQLAlchemy:

**Before (Python):**
```python
def assemble(
    entity_ids: list[str],
    depth: int,
    tenant_id: str,
    query: str | None = None,
    max_facts: int | None = None,
    min_confidence: float = 0.0,
) -> dict:
```

**After (Go):**
```go
func Assemble(
    ctx context.Context,
    tx pgx.Tx,
    entityIDs []string,
    depth int,
    tenantID string,
) (*Bundle, error)
```

Update the description to note:
- `pgx.Tx` for transaction-scoped assembly (caller manages begin/commit/rollback).
- RLS enforcement via `SET LOCAL app.tenant_id` on the transaction.
- Bundle struct is marshaled to JSON by `json.Marshal()`.

---

## 12. Implementation Checklist

- [ ] **T1:** Write `internal/api/api.go` — chi router skeleton, request/response structs (hand-rolled, no generics).
- [ ] **T2:** Write `internal/api/auth.go` — JWTVerifier, RS256 signature verification.
- [ ] **T3:** Write `internal/api/middleware.go` — JWT middleware chain.
- [ ] **T4:** Write `internal/assembler/bundle.go` — Assemble function, Bundle struct.
- [ ] **T5:** Write `internal/assembler/graph.go` — ExpandGraph function with recursive CTE.
- [ ] **T6:** Write `internal/workers/dossier.go` — Dossier pre-computation worker.
- [ ] **T7:** Write `internal/mcp/server.go` — MCP server (demur if go-sdk is unclear).
- [ ] **T8:** Write tests for assembler, API, JWT, dossier worker.
- [ ] **T9:** Write integration tests.
- [ ] **T10:** Update `docs/concepts/dossiers-vs-stories.md`.
- [ ] **T11:** Add cobra subcommands to main CLI (`factvault api`, `factvault mcp`, `factvault worker dossier`).
- [ ] **T12:** Update CI/CD for new code paths.

---

## 13. Known Risks & Demurs

### Risk 1: MCP go-sdk Maturity

The `github.com/modelcontextprotocol/go-sdk` is documented as immature and sparse on examples. If function signatures cannot be verified by reading the SDK source or official examples, the implementer **MUST demur** — write a TODO comment and stub the MCP server with clear error messages. A partial PR (API + assembler + worker, but no MCP server) is acceptable and preferred over fabricated signatures.

**Demur Template:**
```go
// TODO MCP server: [reason] — demurred per coordinator brief
// Example: "function signatures for tool registration not found in go-sdk docs or examples"
func (s *MCPServer) Start(ctx context.Context) error {
    return fmt.Errorf("MCP server not yet implemented")
}
```

### Risk 2: Concurrent Graph Expansion

The recursive CTE may timeout on large graphs. If performance becomes an issue, consider:
- Caching expanded entity sets per (tenant, entity, depth) key.
- Limiting depth to 2 in production.
- Pre-computing story candidates nightly (similar to dossier pattern).

---

## 14. Deliverables

1. **Go code:** Fully tested, passing `go test ./... -race`, `go vet ./...`, `gofumpt -l .`.
2. **PR:** Opened against main branch with:
   - Explicit "Shipped" vs. "Demurred" breakdown.
   - All test results.
   - Concept doc diff.
   - Specific MCP demurs (if any) with reason.
3. **Issue closure:** #75 remains open; PR body notes "Partial — follow-up #75 tracks remaining MCP work."

---

## 15. Execution Order

1. TDD: write failing tests for assembler (Assemble + ExpandGraph).
2. Implement assembler functions.
3. TDD: write tests for API handlers.
4. Implement API handlers + JWT middleware.
5. TDD: write tests for dossier worker.
6. Implement dossier worker.
7. Attempt MCP server; demur if go-sdk unclear.
8. Integration tests.
9. Update concept doc.
10. Commit + PR.

---

**End of Plan 4 Go Spec**
