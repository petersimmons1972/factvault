# Fact Pipeline Go Implementation Plan

> **For:** Plan 3 (fact-pipeline) Go rewrite
> **Status:** Active implementation
> **Authority:** `docs/superpowers/specs/2026-05-22-go-transition.md` issue #74
> **Based on:** Python Plan 3 (`docs/superpowers/plans/2026-05-22-fact-pipeline.md`) conceptual scope

**Goal:** Implement the deterministic and LLM-based fact extraction pipeline in Go. Extractors produce `ExtractedFact` values from source text via regex, gazetteer, and OpenAI-compatible LLM calls. The offset-verification gate rejects hallucinated excerpts before database write. A corroborate worker computes confidence from independent source counts. The vocabulary resolver enforces strict or permissive property slug handling.

**Tech Stack:** Go 1.22+, stdlib `net/http` for LLM calls, pgx v5, sqlc for typed queries, stdlib `testing`.

---

## 1. Overview & Core Data Model

### 1.1 `ExtractedFact` struct

`internal/extractors/base.go` defines the canonical extracted fact record.

```go
type ExtractedFact struct {
    SubjectText        string
    PropertySlug       string
    Value              string
    ValueType          string
    Excerpt            string
    ExcerptOffsetStart int
    ExcerptOffsetEnd   int
    ExtractionMethod   string
    SourceConfidence   *float64
}
```

Rules:
- Immutable by convention.
- `SourceConfidence` is optional; set only when a source-specific score is meaningful (typically `nil` for deterministic extractors).
- Offsets are character positions into `sources.raw_text` (UTF-8 aware; not byte indices).
- `ExtractionMethod` identifies the extractor (e.g., `regex:identifiers-v1`, `llm:v1:covered-span`).

### 1.2 `Extractor` interface

```go
type Extractor interface {
    Extract(ctx context.Context, source *db.Source, rawText string) ([]ExtractedFact, error)
}
```

Rules:
- All extractors must accept context for cancellation and logging.
- Extractors do not mutate the input source.
- Deterministic extractors return only verified facts from their own pattern logic.

### 1.3 `StatementProposal` struct (LLM stage)

`internal/extractors/llm.go` defines the LLM response shape before the gate.

```go
type StatementProposal struct {
    SubjectText        string `json:"subject_text"`
    PropertySlug       string `json:"property_slug"`
    Value              string `json:"value"`
    ValueType          string `json:"value_type"`
    Excerpt            string `json:"excerpt"`
    ExcerptOffsetStart int    `json:"excerpt_offset_start"`
    ExcerptOffsetEnd   int    `json:"excerpt_offset_end"`
    EvidenceSpanStart  int    `json:"evidence_span_start,omitempty"`
    EvidenceSpanEnd    int    `json:"evidence_span_end,omitempty"`
}
```

Only `ExcerptOffsetStart` and `ExcerptOffsetEnd` are load-bearing for the verification gate.

---

## 2. Package Layout

```
internal/
  extractors/
    base.go                      # ExtractedFact, Extractor interface
    base_test.go
    deterministic/
      identifiers.go             # CIK, CUSIP, DOI, NCT, ISBN-13
      identifiers_test.go
      money.go                   # USD amounts + suffix normalization
      money_test.go
      dates.go                   # ISO-8601, Month DD YYYY, DD Month YYYY
      dates_test.go
      gazetteer.go               # entity name lookup
      gazetteer_test.go
      runner.go                  # compose all deterministic + covered-span tracking
      runner_test.go
    llm.go                       # OpenAI-compatible client + response parsing
    llm_test.go
  workers/
    extract.go                   # collect → deterministic → llm → gate → insert
    extract_test.go
    corroborate.go               # recompute confidence from independent source counts
    corroborate_test.go
  assembler/
    confidence.go                # deterministic confidence formula
    confidence_test.go
  vocabulary/
    resolver.go                  # strict/permissive property slug resolution
    resolver_test.go
```

CLI entry points (under existing Cobra structure in `cmd/factvault/main.go`):
- `factvault worker extract`
- `factvault worker corroborate`

---

## 3. Deterministic Extractors

All deterministic extractors are implemented. This section documents the contract; implementation details follow the Python plan closely.

### 3.1 Identifier Extractor

`internal/extractors/deterministic/identifiers.go`

Detects and extracts:
- CIK (10-digit or leading-zero SEC identifier)
- CUSIP (9-character CUSIP)
- DOI (Digital Object Identifier)
- NCT (NCT[0-9]{8} clinical trial)
- ISBN-13 (13-digit ISBN or ISBN-formatted)

Regex-driven, exact matches. `ExtractionMethod: "regex:identifiers-v1"`.

**Tests:** `identifiers_test.go` — 9 passing cases covering edge cases and false-positive rejection.

### 3.2 Money Extractor

`internal/extractors/deterministic/money.go`

Detects USD currency amounts. Matches patterns like:
- `$1.2M` → normalized to `1200000`
- `$50K` → normalized to `50000`
- `$1.5 billion` → normalized to `1500000000`

Supports multiplier suffixes: K, M, B, and word forms (thousand, million, billion). Preserves original excerpt; `Value` is the normalized numeric string. `ExtractionMethod: "regex:money-v1"`.

**Tests:** `money_test.go` — 11 passing cases.

### 3.3 Date Extractor

`internal/extractors/deterministic/dates.go`

Parses:
- ISO-8601 dates (`YYYY-MM-DD`, `YYYY-MM-DDTHH:MM:SS`)
- Human-readable formats (`Month DD, YYYY`, `DD Month YYYY`)

Canonical `Value` is RFC3339 (full timestamps) or `YYYY-MM-DD` (date-only). `ExtractionMethod: "regex:dates-v1"`.

**Tests:** `dates_test.go` — 12 passing cases.

### 3.4 Gazetteer Matcher

`internal/extractors/deterministic/gazetteer.go`

Exact-match named entity lookup against bundled CSV gazetteers. Supports case-insensitive matching while preserving the exact source excerpt. Aliases are supported (one primary name, zero or more aliases per entity).

Bundled data:
- `data/gazetteer/sp500_companies.csv` (name, aliases)
- `data/gazetteer/us_politicians.csv` (name, aliases, jurisdiction)

`ExtractionMethod: "gazetteer:v1"`.

**Tests:** `gazetteer_test.go` — 10 passing cases (load tests verify CSV parsing; content tests verify matching logic).

### 3.5 Deterministic Runner

`internal/extractors/deterministic/runner.go`

Orchestrates all four deterministic extractors:
1. Invoke identifiers, money, dates, gazetteer in sequence
2. Deduplicate exact duplicates (same subject, property, value, offset)
3. Preserve extractor provenance on each fact

Does not call the LLM; does not touch the database.

```go
func (r *Runner) Extract(ctx context.Context, source *db.Source, rawText string) ([]ExtractedFact, error)
```

**Tests:** `runner_test.go` — 9 passing cases covering deduplication and offset preservation.

---

## 4. LLM Extractor

`internal/extractors/llm.go`

Transport: stdlib `net/http` only (no OpenAI SDK).

### 4.1 Request contract

```go
type LLMExtractor struct {
    BaseURL   string     // e.g., "http://localhost:11434/v1"
    APIKey    string     // bearer token if needed
    ModelID   string     // e.g., "gpt-4" or "ollama-model"
    Timeout   time.Duration
    HTTPClient *http.Client
}

func (e *LLMExtractor) Extract(ctx context.Context, source *db.Source, rawText string) ([]ExtractedFact, error)
```

The extractor constructs a chat/completions request:
- System prompt provides the fact extraction schema
- User message is the source text
- `response_format: {"type":"json_object"}` requests structured output
- Response is parsed into `[]StatementProposal`

### 4.2 Failure handling

- Transport errors, timeout, or bad status code → return error
- Malformed JSON response → return error
- Valid JSON but unparseable as `[]StatementProposal` → return error
- The caller (extract worker) decides whether to retry

All proposals are returned for downstream gate filtering.

**Tests:** `llm_test.go` — uses `net/http` mock (stdlib compatible); tests JSON parsing, error handling, timeout behavior.

---

## 5. Offset-Verification Gate (Load-Bearing)

This is the anti-hallucination check. Located in `internal/workers/extract.go`.

### 5.1 Function signature

```go
func VerifyExcerptOffset(rawText, excerpt string, offsetStart, offsetEnd int) bool
```

### 5.2 Semantics

- `offsetStart` and `offsetEnd` are character positions (rune indices) into `rawText`
- The function must validate that offsets land on valid UTF-8 rune boundaries
- The substring `rawText[offsetStart:offsetEnd]` must equal `excerpt` exactly
- No case folding, Unicode normalization, or trimming
- Exact string equality only

### 5.3 Failure behavior

If a proposal fails verification:
- Discard it (do not insert into `statement_sources`)
- Log a warning with source ID, proposed excerpt, offsets, and property slug
- Continue processing remaining proposals
- No error return (gate is per-proposal, not fatal)

### 5.4 Integration

`internal/workers/extract.go` workflow:

1. Load archived source text
2. Run deterministic extractors → collect results
3. Call LLM extractor → collect proposals
4. Apply offset gate to every LLM proposal
5. Merge accepted proposals into statement rows
6. Write only verified facts to `statement_sources`

**Tests:** `extract_test.go` — includes dedicated test(s) proving bad offsets are discarded.

---

## 6. Confidence Computation (Pure Function)

`internal/assembler/confidence.go`

This is a line-for-line port of the Python `compute_confidence()` function.

### 6.1 Function signature

```go
func ComputeConfidence(ctx context.Context, querier db.Querier, statementID string, tenantID string) (float64, error)
```

### 6.2 Formula

```
1. Fetch all sources for the statement
2. Cluster sources by independence:
   - Same publisher domain (eTLD+1) → same cluster
   - Trigram similarity ≥ 0.8 of first 2000 chars → same cluster
3. Count clusters: n = len(clusters)
4. Get per-source max confidence (or 1.0 if all nil)
5. Return min(per_source_max, ceiling(n)) where:
   - ceiling(0) = 0.0
   - ceiling(1) = 0.50
   - ceiling(2) = 0.85
   - ceiling(≥3) = 0.95
```

Rules:
- Deterministic, side-effect free (read-only database access)
- No LLM involvement
- No statement ever reaches 1.0 through automated pipeline

**Tests:** `confidence_test.go` — unit tests covering ceiling logic, independence clustering, per-source max handling.

---

## 7. Corroborate Worker

`internal/workers/corroborate.go`

Recomputes confidence for all statements in a tenant.

```go
func CorroborateOnce(ctx context.Context, db *sql.DB, tenantID string, batchSize int) error
```

Workflow:
1. Fetch all statements for the tenant
2. For each statement, call `ComputeConfidence()`
3. Update `statements.confidence` with the computed value
4. Log summary: N statements processed, confidence changes

Runs idempotently; safe to run multiple times.

**Tests:** `corroborate_test.go` — covers statement update, idempotency, batch processing.

---

## 8. Vocabulary Resolver

`internal/vocabulary/resolver.go`

Resolves incoming property labels to canonical vocabulary slugs.

### 8.1 Interface

```go
type Resolver interface {
    Resolve(ctx context.Context, label string) (string, error)
}

type StrictResolver struct {
    querier db.Querier
}

type PermissiveResolver struct {
    querier db.Querier
}
```

### 8.2 Strict mode

- Unknown property label → queue to `proposed_properties` table
- Return error (caller must decide whether to retry or skip)
- The extraction pipeline does not proceed until the label is resolved

### 8.3 Permissive mode

- Unknown property label → create an auto-slug (e.g., `auto_label_slug`)
- Insert into `properties` table as `auto_property`
- Return the auto-slug
- Extraction pipeline continues

### 8.4 Tests

`resolver_test.go` — covers strict mode queueing, permissive mode auto-creation, existing property lookup.

---

## 9. Extract Worker

`internal/workers/extract.go`

Main entry point for the fact extraction pipeline. Orchestrates deterministic + LLM + gate + insert.

```go
func ExtractOnce(ctx context.Context, db *sql.DB, tenantID string, batchSize int) error
```

Workflow:
1. Fetch archived sources (status=archived)
2. For each source:
   a. Load `sources.raw_text`
   b. Run deterministic runner
   c. Run LLM extractor
   d. Apply offset gate to LLM proposals
   e. Merge deterministic + gated LLM results
   f. Resolve property slugs
   g. Insert into `statements` + `statement_sources`
3. Update source status (e.g., `status='extracted'`)
4. Return summary: N sources processed, N facts inserted, N rejected

Idempotent by source; safe to re-run on same sources.

**Tests:** `extract_test.go` — full pipeline integration, gate rejection, property resolution.

---

## 10. CLI Integration

Add two subcommands under the existing Cobra structure in `cmd/factvault/main.go`:

```bash
factvault worker extract
factvault worker corroborate
```

Each subcommand:
- Loads config from `internal/config/`
- Sets tenant context via `internal/db/rls.go`
- Emits structured JSON logs via `log/slog`
- Returns non-zero exit code on startup or fatal errors

---

## 11. Database Migrations

**No new migrations required for Plan 3.** The schema from Plan 1 + Plan 2 is sufficient.

If the vocabulary resolver requires schema support (e.g., `proposed_properties` table), add a goose migration:
- Filename: `00003_vocabulary_support.sql`
- Only created if implementation actually needs it; otherwise, do not add.

---

## 12. Test Plan

Minimum coverage required before branch acceptance:

- Unit tests for all deterministic extractors (identifiers, money, dates, gazetteer)
- Unit tests for deterministic runner (composition, deduplication)
- Unit tests for LLM extractor (HTTP transport, JSON parsing, error handling)
- Unit test for offset-verification gate (bad offsets are discarded)
- Unit tests for confidence computation (ceiling logic, independence, per-source max)
- Unit tests for vocabulary resolver (strict queueing, permissive auto-creation)
- Integration test for extract worker (source → deterministic → llm → gate → insert)
- Integration test for corroborate worker (confidence recomputation)
- End-to-end test: source text → extract → corroborate → verify facts in DB

All tests must pass: `go test ./... -count=1 -race`

---

## 13. Acceptance Criteria

Implementation is complete and ready for PR when:

1. ✅ `go test ./... -count=1 -race` passes
2. ✅ `go vet ./...` passes
3. ✅ `gofumpt -l .` is clean (no formatting issues)
4. ✅ Offset-verification gate test proves bad offsets are discarded (not written to DB)
5. ✅ Vocabulary resolver tests cover strict and permissive modes
6. ✅ LLM extractor uses stdlib `net/http`, not third-party SDK
7. ✅ Concept doc updated: Python `compute_confidence()` → Go `ComputeConfidence()`
8. ✅ All four code areas (extractors, llm, workers, vocabulary) implemented
9. ✅ PR opened against main, NOT merged

---

## 14. Implementation Notes

### Gazetteers and test data

The gazetteer CSV files (`data/gazetteer/*.csv`) must exist and be embedded in the final binary. Options:
- `go:embed` directive (recommended)
- Load from filesystem at runtime

Recommend `go:embed` for zero-external-dependency deployment.

### LLM endpoint configuration

The LLM endpoint URL, API key, and model ID are loaded from:
- Environment variables (`FACTVAULT_LLM_BASE_URL`, `FACTVAULT_LLM_API_KEY`, `FACTVAULT_LLM_MODEL`)
- Config file (if implemented)
- Defaults (localhost:11434 for Ollama development)

### Startup vs. runtime errors

Startup errors (e.g., database unreachable, LLM endpoint down) → exit non-zero immediately.
Runtime errors (e.g., single source fails extraction) → log warning, continue to next source.

