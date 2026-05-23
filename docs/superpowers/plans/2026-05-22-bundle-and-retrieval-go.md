# Plan 4 Go Spec — Bundle and Retrieval

**Issue:** #75  
**Status:** Draft for implementation  
**Scope:** Go port of the retrieval surface: JWT auth, chi API, MCP server, bundle assembler, and dossier worker.

## 1. Purpose

This plan ports the retrieval surface of factvault from Python to Go. The goal is
to expose the same conceptual product surface with the Go stack already locked
by the transition spec:

- `internal/api/` for HTTP retrieval
- `internal/mcp/` for MCP tools
- `internal/auth/` for JWT verification and dev token issuance
- `internal/assembler/` for the shared bundle assembly path
- `internal/workers/dossier.go` for nightly dossier materialization

The Python code path is the prior art, but the Go implementation is the source of
truth for this phase.

## 2. Design Principles

- Keep bundle shape stable.
- Use one bundle assembler for dossiers and stories.
- Resolve `tenant_id` from JWT claims, never from request body.
- Run all DB access inside `db.TenantContext`.
- Use stdlib `crypto` for JWT verification and signing.
- Avoid auto-generated OpenAPI. Maintain a hand-written spec.
- Treat go-sdk maturity as a real integration risk; if it blocks, emit a `block`
  message rather than inventing a parallel protocol.

## 3. Locked Decisions

- HTTP framework: `github.com/go-chi/chi/v5`
- JWT verification: stdlib `crypto` only
- MCP server: `github.com/modelcontextprotocol/go-sdk`
- Database access: `pgx/v5`
- Bundle production: one assembler entry point shared by all retrieval modes
- Dossier depth: `depth=0`
- Story depth: `depth=2` or `depth=3`
- OpenAPI: handwritten YAML

## 4. Files and Packages

### 4.1 `internal/auth/`

Responsibilities:

- Parse and verify JWTs
- Issue development tokens for local use
- Expose claims in a typed Go structure
- Enforce `tenant_id` presence and validity

### 4.2 `internal/api/`

Responsibilities:

- chi router
- JWT middleware
- tenant context injection
- retrieval routes for entities, stories, and facts
- error mapping to RFC 9457 Problem Details JSON

### 4.3 `internal/mcp/`

Responsibilities:

- MCP server bootstrap
- three tools:
  - `factvault__entity_lookup`
  - `factvault__story_query`
  - `factvault__fact_query`
- reuse the same retrieval service layer as HTTP

### 4.4 `internal/assembler/`

Responsibilities:

- one `BundleAssembler` implementation
- dossier bundle assembly
- story bundle assembly
- source, statement, conflict, and confidence projection

### 4.5 `internal/workers/dossier.go`

Responsibilities:

- materialize per-entity dossiers
- write cached bundle output for known entities
- call the shared assembler

### 4.6 `cmd/factvault/`

Responsibilities:

- register `api`, `mcp`, `doctor`, `auth`, `example`, `migrate`, and `worker`
- expose `factvault auth` helpers for token issuance and inspection

### 4.7 `docs/api/openapi.yaml`

Responsibilities:

- hand-maintained retrieval API contract
- path and schema documentation only
- no codegen ownership

## 5. Retrieval Contract

The following API surface must remain conceptually aligned with the Python design:

- Entity lookup returns a canonical entity record and dossier metadata
- Story query returns a query-keyed bundle with graph-expanded supporting facts
- Fact query returns facts, statements, sources, and confidence metadata

Every response must preserve:

- source excerpts
- offsets
- archive URLs
- verification state
- confidence
- conflicts

## 6. Authentication Contract

JWT verification rules:

- Only accepted algorithms are those explicitly supported by the verifier
- `tenant_id` must be present and parseable
- invalid or expired JWTs return 401
- authorization failures return 403
- the JWT claims object is the only source of tenant identity

Dev token issuance:

- `factvault auth` should be able to issue local development tokens
- tokens are only for local or controlled environments
- signing keys are configured explicitly

## 7. Bundle Assembly Contract

The bundle assembler is the core of this plan.

It must:

- accept a subject identifier or query context plus depth
- load all relevant statements, qualifiers, sources, and conflicts
- compute the projected confidence values deterministically
- preserve source provenance in the output
- be reusable by both dossier and story retrieval paths

The assembler must not duplicate query logic across HTTP, MCP, and worker paths.
It is the single code path for bundle production.

## 8. Dossier Contract

The dossier worker should:

- enumerate entities that require precomputation
- assemble `depth=0` bundles
- persist the result in the dossier cache/store
- use the same bundle schema as the API

## 9. MCP Contract

The MCP server should expose exactly the retrieval capabilities needed by the
product surface:

- entity lookup
- story query
- fact query

It should not fork a second retrieval model. It should call the same service
layer used by HTTP.

## 10. Implementation Order

1. Auth primitives and claim verification
2. Bundle assembler core
3. API router and retrieval handlers
4. MCP server and tool wiring
5. Dossier worker
6. OpenAPI refresh
7. Integration tests and doctored fixtures

## 11. Testing Requirements

Required coverage:

- JWT verification success/failure cases
- tenant context injection behavior
- bundle assembly golden tests
- HTTP route tests for retrieval endpoints
- MCP tool tests
- dossier worker persistence tests
- end-to-end retrieval path using the same assembler from both API and MCP

## 12. Risks

- go-sdk capability gaps may block MCP tool wiring
- JWT implementation mistakes can break tenant isolation
- any divergence in bundle shape will leak into downstream LLM prompts
- API and MCP must stay behaviorally aligned

If go-sdk blocks implementation, stop and emit a `block` message with the exact
gap. Do not improvise a second protocol.

## 13. Acceptance Criteria

- `docs/superpowers/plans/2026-05-22-bundle-and-retrieval-go.md` exists
- `internal/auth/` implemented
- `internal/api/` implemented
- `internal/mcp/` implemented
- `internal/assembler/bundle.go` implemented
- `internal/workers/dossier.go` implemented
- `factvault auth`, `factvault api`, `factvault mcp`, and `factvault worker dossier` work
- `go test ./... -count=1 -race` passes
- `go vet ./...` passes
- `sqlc diff` passes

## 14. Non-Goals

- Changing the bundle schema
- Changing RLS semantics
- Adding network transport to the agent-comms bus
- Rewriting the embedder microservice in Go

