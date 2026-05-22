# factvault — Design Spec

**Date:** 2026-05-22
**Status:** Draft (pending founder review)

---

## Pitch

factvault is a self-hostable research database where every fact is grounded in a verifiable, durably-archived source. Users plug it into their LLM stack and get hallucination-resistant long-form output because every fact carries the verbatim excerpt, URL, archived snapshot, content hash, and verification status that produced it. The design is opinionated: the source-existence layer is non-negotiable, confidence is computed deterministically from independent source count rather than guessed by a model, and conflicts are preserved rather than silently resolved.

---

## Table of Contents

1. [The Promise](#1-the-promise)
2. [Dossiers vs. Stories](#2-dossiers-vs-stories)
3. [Architecture — The Four Pillars in Detail](#3-architecture--the-four-pillars-in-detail)
   - 3.1 [Source Existence Layer](#31-source-existence-layer)
   - 3.2 [Statement Model](#32-statement-model)
   - 3.3 [Corroboration and Confidence](#33-corroboration-and-confidence)
   - 3.4 [Bundle Assembly](#34-bundle-assembly)
4. [Pipeline — The Six Stages](#4-pipeline--the-six-stages)
5. [Retrieval Surface](#5-retrieval-surface)
6. [Operational Shape](#6-operational-shape)
7. [Repository Layout](#7-repository-layout)
8. [Documentation Strategy](#8-documentation-strategy)
9. [Explicit Non-Goals for v1](#9-explicit-non-goals-for-v1)
10. [Open Questions / Known Unknowns](#10-open-questions--known-unknowns)

---

## 1. The Promise

factvault rests on four architectural pillars. They are listed in priority order. Every design decision in this document exists to serve pillar one; pillars two through four exist to make pillar one useful.

### Pillar 1 — Source Existence

Every source is captured with the full `raw_text`, a Wayback Machine snapshot URL, and a SHA-256 content hash computed at ingest. Periodic re-verification runs are logged in an append-only `source_verifications` table — nothing is deleted, overwritten, or tombstoned. If the original URL dies tomorrow, the captured `raw_text`, `raw_html`, and `archive_url` remain authoritative forever. The verbatim excerpt that grounded a fact is stored with character-level offsets into the source body; extraction is only accepted when a deterministic offset check confirms the excerpt is actually present in the body at those offsets. LLM hallucinations are rejected at extraction time, not discovered later. This pillar is non-negotiable. The whole project exists because of it.

### Pillar 2 — Structured Facts

Facts are represented using a Wikidata-inspired statement model on Postgres. The vocabulary of predicates (properties) is controlled: `properties` rows are registered slugs with explicit value types. This prevents the two failure modes that make EAV databases useless over time — free-text predicate soup where `"founded_in"`, `"founding year"`, and `"yearFounded"` silently diverge, and schema.org-as-structural-schema where your database design is coupled to a third-party vocabulary it cannot extend. schema.org URIs appear only in `entities.type_uri` for interoperability, never as structural schema. The property vocabulary is configurable: in strict mode (default), unknown slugs proposed by the LLM extractor are rejected and queued for human review; in permissive mode, they auto-register.

### Pillar 3 — Cross-Source Corroboration

Confidence is computed deterministically from independent source count. A statement supported by a single source has a confidence ceiling of 0.5; three independent sources agreeing raises it to 0.95. Independence is determined by publisher domain and trigram similarity — two articles from the same outlet or with trigram similarity ≥ 0.8 are not independent. Conflicts — two non-deprecated statements with the same `(subject_id, property_id)` and differing values — are preserved in full via a `rank` enum (`preferred` / `normal` / `deprecated`) and surfaced via a `v_conflicts` SQL view. Conflicts are never destructively resolved. The LLM never sets confidence.

### Pillar 4 — Story Assembly

A single shared `bundle_assembler` function produces JSON for both pre-computed entity-keyed **dossiers** and on-demand query-keyed **stories**. There is exactly one code path for bundle production. Dossier workers call it with `depth=0` for a single entity; story endpoints call it with `depth=2` or `depth=3` for recursive graph traversal through `relations`. The bundle carries every source, excerpt, offset, archive URL, and verification status for every fact it contains — downstream LLMs have everything they need to cite claims and refuse to confabulate.

---

## 2. Dossiers vs. Stories

### The One-Sentence Framing

*A dossier answers "tell me about X". A story answers "tell me what's going on with this idea". Use a dossier when you know the entity; use a story when you have a question that spans entities.*

### Comparison Table

| Dimension           | Dossier                                          | Story                                                  |
|---------------------|--------------------------------------------------|--------------------------------------------------------|
| **Keyed by**        | Entity ID or canonical name                      | Free-text query                                        |
| **Question answered** | "Tell me everything about entity X"            | "What's happening with [concept / event / trend]"      |
| **Computation**     | Pre-computed nightly; served from cache          | On-demand; recursive CTE graph traversal at query time |
| **Scope**           | One entity, all statements and qualifiers        | Multi-entity subgraph, depth-bounded                   |
| **Use case**        | Monitoring a known set of entities over time     | Investigative queries spanning unknown entity sets     |
| **Stable URL**      | Yes — `/entities/{id}/dossier`                   | No — POST to `/stories` with query body                |

### Dossier Examples

**Example A — VC associate monitoring 200 AI startups.**
The associate pre-registers each company as an entity with `type_uri: "https://schema.org/Organization"`. Each night the dossier worker runs `assemble(entity_id, depth=0)` for all 200 entities. The resulting bundle contains: funding rounds (amount, round type, lead investor, date, source), headcount statements (number, date, source), product announcements (name, description, date, source), SEC/EDGAR filings (form type, filed_at, CIK, source), founders (person entities, roles, tenures), and press coverage (headline, publisher, published_at, excerpt, archive_url). The LLM receives the pre-assembled bundle, has complete sourcing for every claim, and generates a weekly portfolio digest that cites every number back to the verbatim excerpt that produced it.

**Example B — Journalist tracking 50 politicians.**
Each politician is an entity. The dossier contains: voting record (bill, vote, date, chamber, source), campaign donors (donor entity or name, amount, date, FEC filing source with CIK), quotes (verbatim text with full character offsets into the source body, outlet, date, archive_url), committee memberships (committee, role, start/end dates, source), and family business ties (related entity, relationship type, confidence, sources). The journalist asks Claude to draft a profile — the bundle provides everything needed to include only facts that are sourced, and the verbatim excerpt for every quote eliminates paraphrasing errors. When the journalist asks "did his vote match his donor's interests?" the LLM has both rows and can reason over them without inventing anything.

**Example C — Pharma analyst tracking Phase II/III drugs.**
Each drug compound is an entity. The dossier contains: trial registrations (ClinicalTrials.gov NCT ID, phase, indication, sponsor, enrollment, start/end dates), primary endpoints (description, met/not met, p-value, source), adverse event summaries (MedDRA code, incidence, severity, source), publications (DOI, journal, trial result summary, excerpt, archive_url), regulatory correspondence (FDA/EMA document type, date, outcome, source URL), and competing drugs (related entity, development stage, confidence). The analyst feeds the bundle to an LLM to draft a competitive intelligence note; every claim about endpoints or adverse events points back to the trial registration or publication that established it.

### Story Examples

**Example A — CFO departures and SEC inquiries.**
A journalist asks: "Which biotechs lost a CFO in the last 18 months, and which of those also had an SEC inquiry in the same period?" factvault does not know in advance which entities satisfy both conditions. The story endpoint receives the query, runs embedding similarity search to find entities in the biotech sector, then expands the graph two hops: departure events → company entities → regulatory event entities. The returned bundle contains only entities that appear in both subgraphs, with the sourced statements that establish both conditions. The LLM receives a factually-grounded entity list with evidence for each inclusion criterion, writes the story, and cites both the SEC filing and the departure announcement for each company.

**Example B — Acquisition chain narrative.**
A researcher asks about the chain: "Acme acquires DataCo → FTC review → MegaCorp layoffs." These are three separate entities connected by event-typed relations. The story endpoint traverses the graph at `depth=3`, collecting the acquisition statement (with the Reuters press release excerpt and archive_url), the FTC review statement (with the agency docket source), and the MegaCorp layoff statement (with the Bloomberg news source). The bundle stitches the three-entity narrative with full sourcing at every node. The LLM writes a coherent timeline that names sources at each step rather than asserting causal links without evidence.

**Example C — State AI legislation and PAC donors.**
A policy analyst asks: "Which states passed AI legislation since 2024, and which bill sponsors received donations from AI PACs?" The story endpoint resolves "AI legislation" entities from the bills subgraph, then expands to sponsor entities, then expands to donor relationships. The bundle contains: state entities, bill entities with vote counts and passage dates, sponsor entities with FEC donor statements, and PAC entities identified by description embedding similarity to "AI lobbying." The LLM receives a sourced cross-entity dataset and drafts a policy brief where every donation figure and vote count is backed by a verbatim excerpt from FEC data or state legislature records.

---

## 3. Architecture — The Four Pillars in Detail

### 3.1 Source Existence Layer

#### `sources` Table

```sql
CREATE TABLE sources (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL,
    url              TEXT NOT NULL,
    fetched_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    content_hash     TEXT NOT NULL,            -- SHA-256 hex of raw body at fetch time
    raw_html         BYTEA,                    -- zlib-compressed raw HTML
    raw_text         TEXT,                     -- NULL until stage 2 (archive worker); populated permanently then
    archive_url      TEXT,                     -- Wayback Save Page Now URL captured at ingest
    publisher        TEXT,
    title            TEXT,
    published_at     TIMESTAMPTZ,
    embedding        vector(1024),             -- BGE-M3 embedding of raw_text
    last_verified_at TIMESTAMPTZ,
    status           TEXT NOT NULL DEFAULT 'collected'
                     CHECK (status IN ('collected', 'archived', 'extracted', 'verified', 'link-rot', 'content-changed')),
    UNIQUE (tenant_id, url)
);

CREATE INDEX idx_sources_tenant_status   ON sources (tenant_id, status);
CREATE INDEX idx_sources_last_verified   ON sources (last_verified_at);
CREATE INDEX idx_sources_published_at    ON sources (published_at);
```

**Invariants:**
- `content_hash` is SHA-256 of the raw HTTP response body at the moment of first fetch. It is never updated in place; content changes are recorded in `source_verifications`.
- `raw_text` is NULL at stage 1 (Collect) and is populated permanently by stage 2 (Archive). Any query that reads `raw_text` must only run after `status >= 'archived'`. Downstream processing reads `raw_text`, never re-fetches the URL.
- `archive_url` capture is best-effort (Wayback may be rate-limited or unavailable). A missing `archive_url` does not block ingestion. The archived copy is supplementary insurance; `raw_text` is the primary durability guarantee.
- `raw_html` is stored compressed to control storage growth. Compression is zlib at the application layer before INSERT.

#### `statement_sources` Junction Table

```sql
CREATE TABLE statement_sources (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    statement_id          UUID NOT NULL REFERENCES statements(id) ON DELETE CASCADE,
    source_id             UUID NOT NULL REFERENCES sources(id),
    excerpt               TEXT NOT NULL,       -- verbatim passage from sources.raw_text
    excerpt_offset_start  INTEGER NOT NULL,    -- character offset into sources.raw_text
    excerpt_offset_end    INTEGER NOT NULL,    -- character offset into sources.raw_text
    extraction_method     TEXT NOT NULL,       -- e.g. 'llm:gpt-5:v1', 'regex:funding-pattern-v3', 'human'
    extracted_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    confidence            NUMERIC(4,3),        -- source-specific confidence before corroboration
    CONSTRAINT fk_statement_sources_tenant
        CHECK (true)                           -- enforced via RLS; no explicit FK to tenant_id needed here
);

CREATE INDEX idx_stmt_sources_statement  ON statement_sources (statement_id);
CREATE INDEX idx_stmt_sources_source     ON statement_sources (source_id);
```

**The excerpt-offset check — load-bearing guarantee.**

Before any row is inserted into `statement_sources`, the extraction worker verifies:

```python
actual = source.raw_text[offset_start:offset_end]
if actual != excerpt:
    raise ExcerptMismatch(
        f"Claimed excerpt does not match source body at [{offset_start}:{offset_end}]. "
        f"Expected: {excerpt!r}. Got: {actual!r}."
    )
```

This check runs in `workers/extract.py` before the database INSERT. An LLM that fabricates an excerpt will produce offsets that do not correspond to any text in the source body. The check catches this at extraction time and rejects the statement entirely. The statement is never written to the database with a bad excerpt. This is the primary anti-hallucination mechanism at the extraction boundary.

Minor whitespace normalization (collapse runs of whitespace to single space) is applied to both sides of the comparison to tolerate HTML-to-text conversion artifacts.

#### `source_verifications` Append-Only Log

```sql
CREATE TABLE source_verifications (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id        UUID NOT NULL REFERENCES sources(id),
    tenant_id        UUID NOT NULL,
    verified_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    status           TEXT NOT NULL
                     CHECK (status IN ('live', 'link-rot', 'content-changed', 'excerpt-missing')),
    new_content_hash TEXT,     -- NULL if status='link-rot' (fetch failed); populated otherwise
    notes            TEXT
);

CREATE INDEX idx_source_verifications_source  ON source_verifications (source_id, verified_at DESC);
CREATE INDEX idx_source_verifications_status  ON source_verifications (status, verified_at DESC);
```

**Immutability invariant:** Rows in `source_verifications` are never updated or deleted. The table is an append-only audit log. A Postgres trigger enforces this:

```sql
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
```

**Verification cadence:**
- The `verify` worker runs daily as a Kubernetes CronJob.
- Any source with `last_verified_at` older than 7 days OR `last_verified_at IS NULL` is re-fetched.
- Additionally, any source with `last_verified_at` older than 30 days is always re-verified regardless of prior status.
- Re-fetch computes a new SHA-256 hash and compares with the stored `content_hash`.
- For every `statement_sources` row linked to this source, the worker checks that `excerpt` appears in the re-fetched body at `(excerpt_offset_start, excerpt_offset_end)` within ±20 characters tolerance for minor whitespace drift.
- Writes one `source_verifications` row per verification run per source.
- Updates `sources.last_verified_at` and `sources.status` to reflect current state.

**What happens when a source goes dead:**
- `source_verifications.status = 'link-rot'` is logged.
- `sources.status` is updated to `'link-rot'`.
- The `raw_text` column and `archive_url` remain unchanged and authoritative.
- No statements are deleted. The `sources.raw_text` remains the evidentiary record.
- The bundle assembler includes `verification_status` in every source block so downstream consumers know the current status of each source.

---

### 3.2 Statement Model

#### DDL

```sql
-- Entities: named things in the world
CREATE TABLE entities (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL,
    ext_id      TEXT,                        -- external canonical ID (e.g. 'Q12345' for Wikidata, 'CIK:0001318605')
    label       TEXT NOT NULL,
    type_uri    TEXT,                        -- schema.org URI for interop only; not structural schema
    description TEXT,
    embedding   vector(1024),               -- BGE-M3 embedding of label + description
    meta        JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, ext_id) NULLS NOT DISTINCT
);

CREATE INDEX idx_entities_tenant       ON entities (tenant_id);
CREATE INDEX idx_entities_label        ON entities (tenant_id, lower(label));
CREATE INDEX idx_entities_type         ON entities (tenant_id, type_uri);
CREATE INDEX idx_entities_embedding    ON entities USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- Properties: the controlled vocabulary of predicates
CREATE TABLE properties (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID,                        -- NULL = system-wide property; non-NULL = tenant-specific
    slug        TEXT NOT NULL,               -- machine-readable key; e.g. 'founded_in', 'ceo', 'raised_usd'
    label       TEXT NOT NULL,               -- human-readable label
    value_type  TEXT NOT NULL
                CHECK (value_type IN ('entity_ref', 'string', 'number', 'date', 'url')),
    description TEXT,
    UNIQUE (tenant_id, slug) NULLS NOT DISTINCT
);

-- Proposed properties: LLM-proposed slugs awaiting human review (strict mode)
CREATE TABLE proposed_properties (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL,
    slug        TEXT NOT NULL,
    label       TEXT,
    value_type  TEXT,
    proposed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    proposed_by TEXT,                        -- extraction_method that proposed it
    reviewed    BOOLEAN NOT NULL DEFAULT false,
    approved    BOOLEAN,
    reviewed_at TIMESTAMPTZ,
    UNIQUE (tenant_id, slug)
);

-- Statements: the facts
CREATE TABLE statements (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    subject_id   UUID NOT NULL REFERENCES entities(id),
    property_id  UUID NOT NULL REFERENCES properties(id),
    -- value columns; exactly one is non-NULL (enforced by CHECK below)
    val_entity   UUID REFERENCES entities(id),   -- when value_type = 'entity_ref'
    val_text     TEXT,                            -- when value_type = 'string' or 'url'
    val_number   NUMERIC,                         -- when value_type = 'number'
    val_date     TIMESTAMPTZ,                     -- when value_type = 'date'
    val_json     JSONB,                           -- structured auxiliary data (not primary value)
    rank         TEXT NOT NULL DEFAULT 'normal'
                 CHECK (rank IN ('preferred', 'normal', 'deprecated')),
    confidence   NUMERIC(4,3) NOT NULL
                 CHECK (confidence >= 0 AND confidence <= 1),
    embedding    vector(1024),                    -- BGE-M3 embedding of the statement's textual representation
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_statement_value_populated
        CHECK (
            (val_entity IS NOT NULL)::int +
            (val_text   IS NOT NULL)::int +
            (val_number IS NOT NULL)::int +
            (val_date   IS NOT NULL)::int = 1
        )
);

CREATE INDEX idx_statements_subject          ON statements (subject_id, property_id, rank);
CREATE INDEX idx_statements_tenant           ON statements (tenant_id, subject_id);
CREATE INDEX idx_statements_val_entity       ON statements (val_entity) WHERE val_entity IS NOT NULL;
CREATE INDEX idx_statements_confidence       ON statements (confidence DESC);
CREATE INDEX idx_statements_embedding        ON statements USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- Qualifiers: contextual metadata on statements
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

-- Relations: derived from entity-valued statements; optimized for graph traversal
CREATE TABLE relations (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    source_id    UUID NOT NULL REFERENCES entities(id),
    target_id    UUID NOT NULL REFERENCES entities(id),
    type         TEXT NOT NULL,              -- mirrors property.slug of the originating statement
    weight       NUMERIC,
    confidence   NUMERIC(4,3),
    description  TEXT,
    embedding    vector(1024),
    meta         JSONB NOT NULL DEFAULT '{}',
    -- back-reference to the originating statement (NULL for embedding-similarity edges)
    statement_id UUID REFERENCES statements(id) ON DELETE CASCADE,
    UNIQUE (tenant_id, source_id, target_id, type)
);

CREATE INDEX idx_relations_source     ON relations (tenant_id, source_id);
CREATE INDEX idx_relations_target     ON relations (tenant_id, target_id);
CREATE INDEX idx_relations_type       ON relations (tenant_id, type);
CREATE INDEX idx_relations_embedding  ON relations USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);
```

#### `relations` — Derived View Rationale

`relations` is a materialized projection of entity-valued statements. It exists because graph traversal (`WHERE source_id = $1 OR target_id = $1`) and fact retrieval (`WHERE subject_id = $1 AND property_id = $2`) have fundamentally different access patterns. Maintaining both surfaces is the correct tradeoff:

- `statements` is the source of truth. A `relations` row with `statement_id` non-NULL is authoritative — the underlying statement holds the evidence.
- `relations` rows where `type = 'embedding-near'` and `statement_id IS NULL` are synthetic edges from embedding-similarity discovery. They are confidence-gated (minimum 0.6) and are never returned in source attribution.
- The `relate` worker keeps `relations` in sync by listening on a Postgres NOTIFY channel (or polling `statements` for new entity-valued rows at 60-second intervals).

#### Controlled Vocabulary — Strict vs. Permissive Mode

The `properties` table is the gatekeeper for what facts can be expressed. A property slug such as `raised_usd`, `ceo`, `founded_in`, or `adverse_event_incidence` must exist before a statement using it can be written.

**Strict mode (default):**
- Any statement proposed with an unknown `property_slug` is rejected before INSERT.
- The proposed slug, value type, and originating extraction method are written to `proposed_properties` for human review.
- The extraction worker logs the rejection and continues processing remaining statements from the document.
- Human reviews `proposed_properties` via CLI (`factvault props review`) or the admin endpoint.

**Permissive mode:**
- Any statement with an unknown `property_slug` causes an automatic INSERT into `properties` with `label = slug` and the inferred `value_type`.
- No human gate.
- Appropriate for rapid prototyping or when the property vocabulary is intentionally open.
- Configured per deployment via `FACTVAULT_PROPERTY_MODE=permissive` env var.

The default is strict. Permissive mode must be opted into explicitly. The failure mode strict mode prevents — "founded_in", "founding year", "yearFounded", "year_founded" all meaning the same thing but being separate properties — is more damaging to long-term database quality than the friction of property review.

#### `v_conflicts` View

```sql
CREATE OR REPLACE VIEW v_conflicts AS
SELECT
    s1.id           AS statement_a_id,
    s2.id           AS statement_b_id,
    s1.tenant_id,
    s1.subject_id,
    s1.property_id,
    p.slug          AS property_slug,
    s1.val_text     AS val_a_text,
    s1.val_number   AS val_a_number,
    s1.val_date     AS val_a_date,
    s1.val_entity   AS val_a_entity,
    s2.val_text     AS val_b_text,
    s2.val_number   AS val_b_number,
    s2.val_date     AS val_b_date,
    s2.val_entity   AS val_b_entity,
    s1.confidence   AS confidence_a,
    s2.confidence   AS confidence_b,
    s1.rank         AS rank_a,
    s2.rank         AS rank_b,
    s1.created_at   AS created_a,
    s2.created_at   AS created_b
FROM statements s1
JOIN statements s2
    ON  s1.subject_id  = s2.subject_id
    AND s1.property_id = s2.property_id
    AND s1.id < s2.id   -- avoid duplicate pairs
    AND s1.rank != 'deprecated'
    AND s2.rank != 'deprecated'
JOIN properties p ON p.id = s1.property_id
WHERE
    -- values differ (check each type column)
    (s1.val_text   IS DISTINCT FROM s2.val_text)   OR
    (s1.val_number IS DISTINCT FROM s2.val_number) OR
    (s1.val_date   IS DISTINCT FROM s2.val_date)   OR
    (s1.val_entity IS DISTINCT FROM s2.val_entity);
```

Conflicts are surfaced, not resolved. The human or a worker sets `rank = 'preferred'` on the authoritative statement and `rank = 'deprecated'` on the superseded one. Neither is deleted.

---

### 3.3 Corroboration and Confidence

#### Confidence Formula

Confidence is computed in `factvault/assembler/confidence.py`. The LLM never sets confidence. The formula is:

```python
from factvault.db import get_sources_for_statement

def compute_confidence(statement_id: str, tenant_id: str) -> float:
    """
    Deterministic confidence from independent source count.
    Returns a value in [0.0, 1.0].
    """
    sources = get_sources_for_statement(statement_id, tenant_id)
    
    if not sources:
        return 0.0
    
    independent_groups = _cluster_by_independence(sources)
    n = len(independent_groups)
    
    # source_per_group_max: the max per-source confidence across the group,
    # as stored in statement_sources.confidence (set by the extractor, not this function).
    # The corroboration ceiling caps the final value regardless of per-source confidence.
    per_source_max = max(s.confidence for s in sources if s.confidence is not None) if any(
        s.confidence is not None for s in sources
    ) else 1.0
    
    if n == 1:
        return min(per_source_max, 0.50)
    elif n == 2:
        return min(per_source_max, 0.85)
    else:  # n >= 3
        return min(per_source_max, 0.95)


def _cluster_by_independence(sources: list) -> list[list]:
    """
    Two sources are NOT independent if:
      (a) they share the same publisher domain, OR
      (b) trigram similarity of their raw_text is >= 0.8 (syndicated content).
    Returns a list of independent groups.
    """
    ...
```

**Independence criteria — precise definition:**
- **Same publisher:** `publisher` domain (eTLD+1) is identical. `reuters.com` and `thomsonreuters.com` are separate. `reuters.com` and `uk.reuters.com` are the same.
- **Trigram similarity:** The trigram similarity of `source_a.raw_text[:2000]` vs `source_b.raw_text[:2000]` is ≥ 0.8. The 2000-character prefix captures the article lede, which is the most commonly syndicated portion. If similarity ≥ 0.8, the sources are treated as the same independent group regardless of publisher domain. This catches wire copy published under different outlets.

**Confidence ceilings summary:**

| Independent sources | Confidence ceiling |
|---------------------|--------------------|
| 0                   | 0.0                |
| 1                   | 0.50               |
| 2                   | 0.85               |
| ≥ 3                 | 0.95               |

No statement ever reaches confidence 1.0 through automated corroboration. A human can manually set `rank = 'preferred'` on a statement and update its confidence to a higher value, but the automated pipeline caps at 0.95.

#### Conflict Detection

After the corroborate worker runs for a new statement, it queries `v_conflicts` for any newly created conflicts involving the same `(subject_id, property_id)` pair. If a conflict is found:

1. Both statements retain their current `rank = 'normal'`.
2. The conflict appears in `v_conflicts`.
3. The bundle assembler includes conflicting statements in the `conflicts[]` block of any bundle that touches these entities.
4. A worker (or human) resolves by setting `rank = 'preferred'` on the authoritative value and `rank = 'deprecated'` on the superseded value.

Confidence is re-computed for any statement whose source set changes (e.g., a new source corroborating an existing statement). The `corroborate` worker owns this recomputation.

---

### 3.4 Bundle Assembly

#### Single Assembler

```python
# factvault/assembler/bundle.py

def assemble(
    entity_ids: list[str],
    depth: int,
    tenant_id: str,
    query: str | None = None,
    max_facts: int | None = None,
    min_confidence: float = 0.0,
) -> dict:
    """
    Single entry point for all bundle production.
    
    - depth=0: statements for entity_ids only (dossier).
    - depth=1..3: recursive CTE through relations, collecting entities
      within `depth` hops (story).
    
    Returns the canonical bundle JSON structure.
    """
    ...
```

**Dossier path:** `depth=0`. The dossier worker calls `assemble(entity_ids=[eid], depth=0, tenant_id=tid)` nightly for each registered entity. The result is written to the `dossiers` cache table (keyed by `(tenant_id, entity_id, assembled_at)`). Stale dossiers (older than 24 hours) are recomputed on the next nightly run. The `dossiers` table schema: `(id UUID pk, tenant_id UUID not null, entity_id UUID not null references entities(id), assembled_at TIMESTAMPTZ not null default now(), bundle JSONB not null)` with a unique index on `(tenant_id, entity_id)`. `GET /entities/{id}/dossier` returns the most recent row for that entity, or triggers an on-demand recompute if none exists or if the row is stale.

**Story path:** `depth=2` or `depth=3`. The story endpoint calls `assemble(entity_ids=[...], depth=depth, tenant_id=tid, query=query)` on demand. Entity IDs are seeded by an embedding similarity search against `entities.embedding` for the query string, top-K results with cosine similarity > 0.6. The recursive CTE then expands from those seeds.

#### Recursive CTE for Graph Traversal

```sql
-- Used inside bundle assembler for depth > 0
WITH RECURSIVE entity_graph AS (
    -- Seed: starting entities
    SELECT id, 0 AS depth
    FROM entities
    WHERE id = ANY($1) AND tenant_id = $2

    UNION ALL

    -- Expand one hop through relations
    SELECT
        CASE
            WHEN r.source_id = eg.id THEN r.target_id
            ELSE r.source_id
        END AS id,
        eg.depth + 1
    FROM entity_graph eg
    JOIN relations r
        ON (r.source_id = eg.id OR r.target_id = eg.id)
        AND r.tenant_id = $2
        AND r.confidence >= 0.4   -- low confidence edges not traversed
    WHERE eg.depth < $3           -- depth limit
)
SELECT DISTINCT id FROM entity_graph;
```

#### Canonical Bundle JSON Shape

The bundle returned by `assemble()` follows this structure:

```json
{
  "query": {
    "type": "dossier",
    "entity_ids": ["<UUID>"],
    "depth": 0,
    "tenant_id": "<UUID>"
  },
  "assembled_at": "2026-05-22T04:00:00Z",
  "entities": [
    {
      "id": "<UUID>",
      "label": "MegaCorp",
      "type_uri": "https://schema.org/Organization",
      "description": "US-listed technology conglomerate",
      "ext_id": "CIK:0001234567"
    }
  ],
  "facts": [
    {
      "id": "<UUID>",
      "subject": { "id": "<UUID>", "label": "MegaCorp" },
      "property": { "slug": "acquired", "label": "Acquired", "value_type": "entity_ref" },
      "value": { "entity": { "id": "<UUID>", "label": "Acme Corp" } },
      "qualifiers": [
        {
          "property": { "slug": "point_in_time", "label": "Point in time", "value_type": "date" },
          "value": { "date": "2025-11-14T00:00:00Z" }
        },
        {
          "property": { "slug": "deal_value_usd", "label": "Deal value (USD)", "value_type": "number" },
          "value": { "number": 4200000000 }
        }
      ],
      "rank": "preferred",
      "confidence": 0.85,
      "sources": [
        {
          "id": "<UUID>",
          "url": "https://www.reuters.com/markets/deals/megacorp-acquires-acme-2025-11-14/",
          "publisher": "reuters.com",
          "fetched_at": "2025-11-14T18:30:00Z",
          "content_hash": "a3f2c1d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2",
          "archive_url": "https://web.archive.org/web/20251114183000/https://www.reuters.com/markets/deals/megacorp-acquires-acme-2025-11-14/",
          "excerpt": "MegaCorp Inc. announced Tuesday it would acquire Acme Corp for $4.2 billion in an all-cash transaction expected to close in the first quarter of 2026.",
          "excerpt_offset_start": 1243,
          "excerpt_offset_end": 1396,
          "last_verified_at": "2026-05-21T04:00:00Z",
          "verification_status": "live",
          "extraction_method": "llm:gpt-5:v1"
        },
        {
          "id": "<UUID>",
          "url": "https://www.wsj.com/articles/megacorp-buys-acme-corp-for-4-2-billion-abc123",
          "publisher": "wsj.com",
          "fetched_at": "2025-11-14T20:00:00Z",
          "content_hash": "b4e3d2c1a0f9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3",
          "archive_url": "https://web.archive.org/web/20251114200000/https://www.wsj.com/articles/megacorp-buys-acme-corp-for-4-2-billion-abc123",
          "excerpt": "MegaCorp said it agreed to buy Acme Corp for $4.2 billion in cash, in a deal that would expand MegaCorp's presence in enterprise data infrastructure.",
          "excerpt_offset_start": 892,
          "excerpt_offset_end": 1046,
          "last_verified_at": "2026-05-21T04:00:00Z",
          "verification_status": "live",
          "extraction_method": "llm:gpt-5:v1"
        }
      ]
    }
  ],
  "relations": [
    {
      "source": { "id": "<UUID>", "label": "MegaCorp" },
      "target": { "id": "<UUID>", "label": "Acme Corp" },
      "type": "acquired",
      "confidence": 0.85,
      "statement_id": "<UUID>"
    }
  ],
  "conflicts": []
}
```

**Bundle invariants:**
- Every entry in `facts[].sources` carries a non-empty `excerpt` and verified `(excerpt_offset_start, excerpt_offset_end)`.
- `verification_status` reflects the most recent `source_verifications` row for this source.
- `conflicts[]` is populated with statement pairs from `v_conflicts` for any entity in this bundle.
- The bundle is serializable to JSON without loss; `NUMERIC` values are serialized as JSON numbers (not strings).

---

## 4. Pipeline — The Six Stages

### Stage Overview

| # | Stage        | Input                     | Output                              | Key file(s)                                 |
|---|--------------|---------------------------|-------------------------------------|---------------------------------------------|
| 1 | Collect      | Config / external signal  | `sources` row, status='collected'   | `workers/collect.py`, `collectors/*.py`     |
| 2 | Archive      | status='collected'        | `raw_text`, `archive_url`, hash     | `workers/archive.py`                        |
| 3 | Extract      | status='archived'         | `statements`, `statement_sources`   | `workers/extract.py`, `extractors/`         |
| 4 | Corroborate  | New statements            | Updated confidence; `v_conflicts`   | `workers/corroborate.py`                    |
| 5 | Verify       | Sources older than 7d     | `source_verifications` rows         | `workers/verify.py`                         |
| 6 | Relate       | New entity-valued stmts   | `relations` rows                    | `workers/relate.py`                         |

---

### Stage 1 — Collect

**Input:** Collector configuration (YAML or Python class), optional trigger (RSS item, scheduled crawl, HTTP webhook, manual upload).

**Output:** One `sources` row per document with `status='collected'`, `url`, `fetched_at`, `raw_html` (uncompressed, stored temporarily), `content_hash`. No `raw_text` yet — text extraction is stage 2.

**Guarantees:**
- Idempotent on `(tenant_id, url)`. Re-collecting the same URL within 1 hour of the last fetch returns the existing `sources.id` without re-inserting.
- `raw_html` is stored at INSERT; the original HTTP response body is not re-fetched downstream.
- No personally identifiable information is collected without an explicit tenant-configured allowlist.

**Collector interface:**

```python
# collectors/base.py
from abc import ABC, abstractmethod
from dataclasses import dataclass
from typing import Iterator

@dataclass
class RawDocument:
    url: str
    raw_html: bytes
    content_hash: str          # SHA-256 of raw_html
    fetched_at: datetime
    publisher: str | None = None
    title: str | None = None
    published_at: datetime | None = None
    metadata: dict = field(default_factory=dict)

class BaseCollector(ABC):
    @abstractmethod
    def fetch(self) -> Iterator[RawDocument]:
        ...
```

**Shipped collectors:**
- `collectors/rss.py` — RSS/Atom feed poller with configurable interval and deduplicate-by-GUID.
- `collectors/sitemap.py` — XML sitemap crawler with `lastmod` filter.
- `collectors/searxng.py` — SearXNG API collector; issues queries and collects result URLs.
- `collectors/wayback_cdx.py` — Internet Archive CDX API; collects historical snapshots for a domain.
- `collectors/http.py` — Direct HTTP URL list; simplest collector for one-off ingestion.
- `collectors/upload.py` — File upload endpoint; accepts HTML or PDF, extracts text, creates source.

**YAML-described HTTP collector:**

Deployments that only need simple URL lists or scheduled HTTP fetches can describe a collector in YAML without writing Python:

```yaml
# config/collectors/ai-news.yaml
type: http
schedule: "0 */4 * * *"   # every 4 hours
urls:
  - https://techcrunch.com/tag/artificial-intelligence/feed/
  - https://venturebeat.com/category/ai/feed/
publisher_override: null   # infer from domain
```

New collectors via Python entrypoint:

```toml
# pyproject.toml
[project.entry-points."factvault.collectors"]
my_collector = "mypackage.collectors.my_collector:MyCollector"
```

---

### Stage 2 — Archive

**Input:** `sources` rows with `status='collected'`.

**Output:** `raw_text` populated (extracted from `raw_html` via trafilatura), `raw_html` compressed (zlib), `archive_url` populated (best-effort), `content_hash` verified, `status='archived'`.

**Guarantees:**
- `raw_text` extraction uses trafilatura with `include_comments=False`, `include_tables=True`, `no_fallback=False`. The exact trafilatura config is pinned in `workers/archive.py` and cannot be changed without a migration that re-extracts affected sources.
- Wayback Save Page Now submission uses the Internet Archive SPN2 API. Failure (rate limit, network error, or 5xx) is logged and retried with exponential backoff up to 3 times over 10 minutes. After 3 failures, the source proceeds to `status='archived'` with `archive_url=NULL`. Wayback failure does not block ingestion.
- `raw_html` is zlib-compressed at level 6 before UPDATE. The compression is applied in `workers/archive.py` immediately after text extraction.

**Wayback failure policy:** Archive failure is not a blocker. The `raw_text` column is the primary durability guarantee. The `archive_url` is supplementary insurance. A source with `archive_url=NULL` is fully valid and will be processed normally by downstream stages.

---

### Stage 3 — Extract

**Input:** `sources` rows with `status='archived'`.

**Output:** `statements` rows, `statement_sources` rows (with verified excerpt offsets), `qualifiers` rows, `proposed_properties` rows (in strict mode for unknown slugs).

**Guarantees:**
- Excerpt-offset check runs before every `statement_sources` INSERT. Statements with failing offset checks are rejected; a structured error is logged to `extraction_errors` table (see below).
- Deterministic extractors run before the LLM extractor. Any coverage from deterministic patterns reduces the LLM call surface.
- LLM structured output uses a strict JSON schema; free-form generation is not used for fact extraction.
- Property slug validation runs before statement INSERT. In strict mode, unknown slugs produce a `proposed_properties` row; the statement is not written.

**Extraction pipeline order:**

```python
# workers/extract.py — high-level flow

for source in get_sources_with_status('archived', tenant_id):
    # 1. Deterministic extractors (fast, no LLM cost)
    det_statements = []
    for extractor in deterministic_extractors:
        det_statements.extend(extractor.extract(source))
    
    # 2. Identify uncovered text spans
    covered_spans = [s.covered_span for s in det_statements]
    uncovered_text = subtract_spans(source.raw_text, covered_spans)
    
    # 3. LLM extractor on uncovered text only
    llm_statements = []
    if uncovered_text:
        llm_statements = llm_extractor.extract(source, uncovered_text)
    
    # 4. Excerpt-offset check on all proposed statements
    validated = []
    for stmt in det_statements + llm_statements:
        if verify_excerpt_offset(source, stmt):
            validated.append(stmt)
        else:
            log_extraction_error(source, stmt, 'excerpt_offset_mismatch')
    
    # 5. Property slug validation
    accepted = []
    for stmt in validated:
        if validate_property_slug(stmt.property_slug, tenant_id):
            accepted.append(stmt)
        # else: proposed_properties row written by validate_property_slug()
    
    # 6. Write to DB
    write_statements(accepted, source, tenant_id)
    update_source_status(source.id, 'extracted')
```

**`extraction_errors` table (operational log, not audit):**

```sql
CREATE TABLE extraction_errors (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL,
    source_id        UUID REFERENCES sources(id),
    error_type       TEXT NOT NULL,   -- 'excerpt_offset_mismatch', 'unknown_property', 'schema_violation', etc.
    extraction_method TEXT,
    raw_proposal     JSONB,           -- the rejected statement proposal
    logged_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**LLM structured output schema (sent to the LLM as `response_format`):**

```json
{
  "type": "object",
  "properties": {
    "statements": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["subject_label", "property_slug", "value", "excerpt", "excerpt_offset_start", "excerpt_offset_end"],
        "properties": {
          "subject_label":        { "type": "string" },
          "property_slug":        { "type": "string" },
          "value":                { "type": "string" },
          "value_type_hint":      { "type": "string", "enum": ["entity_ref", "string", "number", "date", "url"] },
          "excerpt":              { "type": "string" },
          "excerpt_offset_start": { "type": "integer", "minimum": 0 },
          "excerpt_offset_end":   { "type": "integer", "minimum": 1 },
          "qualifiers":           { "type": "array", "items": { "type": "object" } }
        }
      }
    }
  }
}
```

**Deterministic extractors shipped in v1:**

| Extractor                          | Patterns covered                                                  |
|------------------------------------|-------------------------------------------------------------------|
| `extractors/deterministic/funding.py` | Funding amounts, round types (Series A/B/C), date phrases      |
| `extractors/deterministic/dates.py`   | ISO dates, natural language dates, fiscal quarters              |
| `extractors/deterministic/identifiers.py` | CIK numbers, CUSIP, ISIN, DOI, NCT IDs, EDGAR form types  |
| `extractors/deterministic/entities.py`   | Named entity recognition via spaCy (gazetteer-augmented)    |

---

### Stage 4 — Corroborate

**Input:** Newly written `statements` rows (polled or notified via Postgres LISTEN/NOTIFY).

**Output:** Updated `confidence` on statements; new entries visible in `v_conflicts` for any newly created conflicts.

**Guarantees:**
- Confidence is recomputed from scratch on every run for affected statements, not incrementally updated. This prevents accumulation of rounding errors.
- Independence check uses trigram similarity from `pg_trgm` extension (installed on the database), applied to the first 2000 characters of `raw_text`. Similarity ≥ 0.8 → not independent.
- Conflict detection runs after confidence update. If a conflict is detected, no automatic resolution is performed. Both statements persist with `rank='normal'`.

```python
# workers/corroborate.py

def corroborate_statement(statement_id: str, tenant_id: str):
    stmt = get_statement(statement_id, tenant_id)
    sources = get_sources_for_statement(statement_id, tenant_id)
    
    new_confidence = compute_confidence(statement_id, tenant_id)
    update_statement_confidence(statement_id, new_confidence, tenant_id)
    
    # Check for newly created conflicts
    conflicts = get_conflicts_for(stmt.subject_id, stmt.property_id, tenant_id)
    if conflicts:
        notify_conflict(conflicts, tenant_id)
```

---

### Stage 5 — Verify

**Input:** All `sources` rows where `last_verified_at IS NULL OR last_verified_at < now() - interval '7 days'`. Additionally, all sources with `last_verified_at < now() - interval '30 days'` regardless of prior status.

**Output:** New `source_verifications` rows; updated `sources.last_verified_at` and `sources.status`.

**Guarantees:**
- No source row or statement row is deleted. Verification is a read + log operation.
- Excerpt check uses the re-fetched body (not the stored `raw_text`) to detect content changes. Both the stored and re-fetched body are compared.
- If `content-changed` is detected, the new hash is written to `source_verifications.new_content_hash` and a new `raw_text` is stored alongside the original in a `source_snapshots` table (see open question #5 — this table is not yet fully specified).
- Verification runs as a Kubernetes CronJob at `0 2 * * *` (02:00 UTC daily). It processes at most 5000 sources per run to bound execution time. Sources are prioritized by `last_verified_at ASC NULLS FIRST`.

**Excerpt tolerance:** When checking whether the stored `excerpt` still appears at `(excerpt_offset_start, excerpt_offset_end)`, a tolerance of ±20 characters is applied after collapsing whitespace. This accommodates minor formatting changes (e.g., removal of a trailing space, change from double to single newline) without falsely flagging `excerpt-missing`.

---

### Stage 6 — Relate

**Input:** New `statements` rows where `val_entity IS NOT NULL` (entity-valued statements); also runs a periodic embedding-similarity sweep.

**Output:** `relations` rows kept in sync with entity-valued statements; additional `type='embedding-near'` edges from embedding similarity discovery.

**Guarantees:**
- Every entity-valued statement has a corresponding `relations` row within 60 seconds of statement INSERT (via NOTIFY polling or explicit trigger).
- A `relations` row with `statement_id` non-NULL is authoritative; deleting the originating statement cascades to delete the relation row (via `ON DELETE CASCADE`).
- `type='embedding-near'` edges are written only when cosine similarity ≥ 0.6 and `confidence` is set to the similarity score. These edges are never returned in `facts[]` or `sources[]` blocks of bundles; they are traversal-only edges.
- The embedding similarity sweep runs weekly, not daily, to control compute cost.

```python
# workers/relate.py — sync path

def sync_entity_valued_statements(tenant_id: str):
    """
    For each entity-valued statement with no corresponding relations row,
    create the relations row.
    """
    new_stmts = get_entity_valued_statements_without_relation(tenant_id)
    for stmt in new_stmts:
        upsert_relation(
            source_id=stmt.subject_id,
            target_id=stmt.val_entity,
            type=get_property_slug(stmt.property_id),
            confidence=stmt.confidence,
            statement_id=stmt.id,
            tenant_id=tenant_id,
        )
```

---

## 5. Retrieval Surface

### REST API (FastAPI)

All endpoints accept `Authorization: Bearer <JWT>` and extract `tenant_id` from the JWT claims. Postgres RLS enforces tenant isolation at the database layer independently of the application.

| Method | Path                              | Description                                                          |
|--------|-----------------------------------|----------------------------------------------------------------------|
| GET    | `/entities/{id}/dossier`          | Returns cached dossier bundle for entity. Recomputes if stale.       |
| GET    | `/entities/by-name`               | `?q=<name>` — fuzzy entity lookup by label; returns top-5 matches.  |
| POST   | `/stories`                        | `{query, depth, max_facts}` — on-demand story bundle.                |
| POST   | `/facts/query`                    | `{subject_type, property, qualifiers, min_confidence}` — fact filter.|
| GET    | `/properties`                     | List registered property slugs for this tenant.                      |
| POST   | `/properties`                     | Register a new property slug (admin token required).                 |
| GET    | `/sources/{id}`                   | Full source record including raw_text and verification history.      |
| GET    | `/conflicts`                      | List all active conflicts for this tenant.                           |
| GET    | `/health`                         | Liveness probe. Returns 200 if DB is reachable.                      |

**`POST /stories` request body:**

```json
{
  "query": "which biotechs lost a CFO in the last 18 months and had an SEC inquiry",
  "depth": 2,
  "max_facts": 500,
  "min_confidence": 0.4
}
```

**`POST /facts/query` request body:**

```json
{
  "subject_type": "https://schema.org/Organization",
  "property": "raised_usd",
  "qualifiers": [
    { "property": "point_in_time", "gte": "2024-01-01" }
  ],
  "min_confidence": 0.5
}
```

All endpoints return the canonical bundle JSON structure documented in §3.4.

**Pagination:** `GET /entities/by-name` and `POST /facts/query` support `?limit=` (default 20, max 200) and `?cursor=` (opaque cursor based on `created_at` + `id`).

**Error responses:** Standard RFC 9457 Problem Details JSON. `400` for malformed requests, `401` for missing/expired JWT, `403` for tenant isolation violations, `404` for unknown entities, `422` for validation errors, `429` for rate limiting.

---

### MCP Server

The MCP server wraps the same three retrieval modes as MCP tools. It exposes `factvault` as a tool provider for Claude Desktop, Cursor, and any agent stack supporting the Model Context Protocol.

```python
# factvault/mcp/server.py

@mcp_server.tool("factvault__entity_lookup")
def entity_lookup(entity_name: str, tenant_id: str) -> dict:
    """
    Look up an entity by name and return its full dossier bundle.
    Returns: canonical bundle JSON with all sourced facts.
    """
    ...

@mcp_server.tool("factvault__story_query")
def story_query(query: str, depth: int = 2, max_facts: int = 300, tenant_id: str = ...) -> dict:
    """
    Run a cross-entity story query and return the assembled bundle.
    Returns: canonical bundle JSON traversing the entity graph.
    """
    ...

@mcp_server.tool("factvault__fact_query")
def fact_query(
    property_slug: str,
    subject_type: str | None = None,
    min_confidence: float = 0.4,
    tenant_id: str = ...,
) -> dict:
    """
    Query facts by property, optionally filtered by subject entity type.
    Returns: canonical bundle JSON with matching facts.
    """
    ...
```

MCP authentication uses the same JWT tokens as the REST API. The `tenant_id` is extracted from the JWT and cannot be overridden by tool arguments.

---

## 6. Operational Shape

### Multi-Tenancy

`tenant_id` is present on every table. Postgres RLS policies enforce isolation:

```sql
-- Example RLS policy (applied to all tenant-scoped tables)
ALTER TABLE entities ENABLE ROW LEVEL SECURITY;

CREATE POLICY entities_tenant_isolation ON entities
    USING (tenant_id = current_setting('app.current_tenant_id')::uuid);

-- Application sets tenant_id on connection acquisition:
-- SET LOCAL app.current_tenant_id = '<tenant_uuid>';
```

Every database connection acquired by the API or workers must set `app.current_tenant_id` before executing any query. Connection pooling (PgBouncer or built-in asyncpg pooling) is configured to reset this setting between connections.

### Container Standard

All images follow the Chainguard base image standard:

**Workers and API:**
```dockerfile
# Build stage
FROM cgr.dev/chainguard/python:latest-dev AS builder
WORKDIR /app
COPY pyproject.toml .
RUN pip install --prefix=/install .

# Runtime stage
FROM cgr.dev/chainguard/wolfi-base AS runtime
COPY --from=builder /install /usr/local
COPY --from=builder /usr/bin/tini /sbin/tini
USER 65532
ENTRYPOINT ["/sbin/tini", "--"]
```

**Kubernetes security context (applied to all Deployments and CronJobs):**

```yaml
securityContext:
  runAsUser: 65532
  runAsNonRoot: true
  fsGroup: 65532
  allowPrivilegeEscalation: false
  capabilities:
    drop:
      - ALL
```

`fsGroup: 65532` is mandatory. Without it, volume mounts for any PVC (config files, temporary extraction scratch space) will be owned by root and the nonroot process cannot read them, causing a crashloop.

**Postgres:**
- Base image: `cgr.dev/chainguard/postgres:latest` with pgvector extension installed.
- pgvector must be compiled against the same Postgres major version. The Dockerfile pins `pgvector` version.

### Embedding Model

- Default: BGE-M3, 1024 dimensions.
- Loaded locally via `sentence-transformers`. The model weights are downloaded at build time and baked into the image (or mounted as a read-only PVC).
- Embeddings are computed for: `entities.embedding` (label + description), `statements.embedding` (textual representation of subject + property + value), `relations.embedding` (description), `sources.embedding` (raw_text[:4096]).
- HNSW indices on all four tables use `vector_cosine_ops` with `m=16, ef_construction=64`.
- Embedding computation is CPU-only by default. GPU acceleration is opt-in via `FACTVAULT_EMBEDDING_DEVICE=cuda` env var; the same model weights are used.

### LLM Backend

- Pluggable via OpenAI-compatible API contract.
- Default: points at `http://localhost:11434/v1` (Ollama) or the `FACTVAULT_LLM_BASE_URL` env var.
- Swap to hosted providers: `FACTVAULT_LLM_BASE_URL=https://api.openai.com/v1`, `FACTVAULT_LLM_API_KEY=sk-...`.
- Model name: `FACTVAULT_LLM_MODEL=gpt-4o` or `llama3.1:8b` or any compatible model.
- Structured output (JSON mode) is required. If the configured endpoint does not support `response_format: {type: json_object}`, extraction falls back to regex parsing of the LLM response, with lower coverage.

### `factvault doctor` CLI

```
$ factvault doctor

[1/7] Database reachable ...................... OK
[2/7] pgvector extension loaded .............. OK
[3/7] RLS policies applied ................... OK
[4/7] Wayback API reachable .................. OK
[5/7] Embedding model loadable ............... OK (BGE-M3 / 1024d)
[6/7] LLM endpoint responding ................ OK (http://localhost:11434/v1)
[7/7] Canary fact ingest end-to-end .......... OK
       - Collected:   https://example.com/canary
       - Archived:    raw_text=847 chars, archive_url=OK
       - Extracted:   1 statement, excerpt_offset_check=PASS
       - Corroborated: confidence=0.50 (1 source)
       - Verified:    status=live

All checks passed. factvault is ready.
```

On any failure, `doctor` exits non-zero and prints the remediation command or documentation link. It is intended as the first command run after deployment and the first diagnostic step in any support ticket.

### License

MIT.

---

## 7. Repository Layout

```
factvault/
├── factvault/                  # Main Python package
│   ├── api/                    # FastAPI application; routes, auth, request models
│   ├── mcp/                    # MCP server; tool definitions, auth middleware
│   ├── workers/                # Pipeline stage workers (collect, archive, extract, corroborate, verify, relate)
│   ├── collectors/             # Collector implementations (rss, sitemap, searxng, wayback_cdx, http, upload)
│   ├── extractors/             # Extraction logic
│   │   ├── deterministic/      # Regex/pattern extractors (funding, dates, identifiers, entities)
│   │   └── llm.py              # LLM extractor with structured output schema
│   ├── assembler/              # Bundle assembly (bundle.py, confidence.py)
│   ├── db/                     # Database layer (migrations via Alembic, connection pool, RLS helpers)
│   └── cli/                    # CLI commands (doctor, props, ingest, verify-now)
│
├── docs/                       # All documentation
│   ├── concepts/               # Mental model docs (facts-and-sources, dossiers-vs-stories,
│   │   │                       #   confidence-and-corroboration, source-existence)
│   ├── guides/                 # How-to guides for operators and developers
│   │   └── defining-properties.md  # The one mandatory authoring task
│   ├── api/                    # Generated API reference (OpenAPI spec + rendered)
│   └── superpowers/
│       └── specs/              # Design specs (this file)
│
├── examples/                   # Runnable example deployments with realistic fixtures
│   ├── ai-startup-tracking/    # VC monitoring use case; 10 fictional AI startups, 3 funding rounds each
│   ├── political-research/     # Journalist use case; 5 fictional politicians, votes + donors
│   ├── pharma-trial-monitoring/ # Pharma analyst use case; 3 fictional drugs, Phase II/III trials
│   └── investigative-journalism/ # Cross-entity story use case; acquisition + regulatory chain
│
├── tests/                      # Test suite
│   ├── unit/                   # Unit tests per module
│   ├── integration/            # Integration tests (require live DB; use testcontainers-python)
│   └── e2e/                    # End-to-end pipeline tests using example fixtures
│
├── .github/
│   └── workflows/
│       ├── ci.yml              # Lint, type-check, unit tests on every PR
│       ├── integration.yml     # Integration tests on merge to main
│       └── release.yml         # Container image build + push to GHCR on tag
│
├── k8s/                        # Kubernetes manifests (Deployment, CronJob, Service, PVC, RLS migrations)
├── docker-compose.yml          # Local development stack (postgres+pgvector, api, worker, ollama)
├── pyproject.toml              # Package metadata, dependencies, entry points
├── alembic.ini                 # Alembic migration configuration
└── .gitignore
```

**Top-level directory purposes:**

| Directory              | Purpose                                                                    |
|------------------------|----------------------------------------------------------------------------|
| `factvault/api/`       | FastAPI routes, request/response models, auth middleware                   |
| `factvault/mcp/`       | MCP server and tool definitions for Claude Desktop / agent stack           |
| `factvault/workers/`   | One file per pipeline stage; run as separate processes or K8s CronJobs     |
| `factvault/collectors/`| Pluggable document collectors; each implements `BaseCollector`             |
| `factvault/extractors/`| Deterministic and LLM extractors; excerpt-offset check lives here         |
| `factvault/assembler/` | `bundle.assemble()` and `confidence.compute_confidence()` — single source of truth for output |
| `factvault/db/`        | Alembic migrations, connection pool, RLS session helpers, query functions  |
| `factvault/cli/`       | `factvault doctor`, `factvault props review`, `factvault ingest <url>`    |
| `docs/concepts/`       | Mental model documentation; read before API reference                      |
| `docs/guides/`         | Operator how-tos; `defining-properties.md` is the first authoring task    |
| `examples/`            | Four fully runnable examples with realistic fixture data and expected output |
| `tests/`               | Unit, integration, and end-to-end test suites                             |
| `k8s/`                 | Kubernetes manifests for production deployment                             |

---

## 8. Documentation Strategy

### README

The README covers, in order:
1. The five-minute promise: what factvault is, why LLM hallucination at the retrieval layer is the problem being solved, and what the output looks like.
2. A `docker-compose up` quickstart that gets a working local stack running in under 5 minutes.
3. A screenshot (or ASCII-rendered example) of a story bundle response showing a fact with its verbatim excerpt and archive URL.
4. The dossier-vs-story explainer (the one-sentence framing + the comparison table from §2).
5. Links to `docs/concepts/` for mental model depth and `docs/guides/defining-properties.md` for first authoring task.

### `docs/concepts/`

Mental model documentation comes before API reference. The four concept files are:

| File                              | What it explains                                                                                    |
|-----------------------------------|-----------------------------------------------------------------------------------------------------|
| `facts-and-sources.md`            | The statement model; how a fact differs from a source; what excerpt offsets mean and why they exist |
| `dossiers-vs-stories.md`          | Full treatment of §2 including all three examples of each type                                      |
| `confidence-and-corroboration.md` | The deterministic confidence formula; what independence means; how to read the `v_conflicts` view   |
| `source-existence.md`             | Why `raw_text` + `archive_url` + `content_hash` together; the verification lifecycle; what happens when URLs die |

### `examples/`

Four runnable directories, each containing:
- `README.md`: what the example demonstrates and how to run it.
- `fixtures/`: JSON or CSV fixture files representing realistic (anonymized/fictional) source documents.
- `config/`: collector YAML and property definitions for this domain.
- `expected/`: expected bundle output for a reference query, used by `tests/e2e/`.

| Example directory            | Domain focus                         | Primary retrieval mode |
|------------------------------|--------------------------------------|------------------------|
| `ai-startup-tracking/`       | 10 fictional AI startups, funding    | Dossier (nightly batch)|
| `political-research/`        | 5 fictional politicians              | Dossier + Story        |
| `pharma-trial-monitoring/`   | 3 fictional drugs, Phase II/III      | Dossier                |
| `investigative-journalism/`  | Acquisition chain + SEC inquiry      | Story                  |

### `docs/guides/defining-properties.md`

This is the one mandatory authoring task for a new deployment. The guide covers:
- Why the property vocabulary is controlled (the failure mode it prevents).
- How to audit what facts you want to track before ingesting a single document.
- The `factvault props create` CLI command.
- Examples of property definitions for each of the four example domains.
- Strict vs. permissive mode and when to use each.
- How to merge duplicate properties discovered after ingestion.

---

## 9. Explicit Non-Goals for v1

These decisions were made to constrain scope and prevent architectural complexity that does not pay for itself in v1.

| Non-goal                              | Rationale                                                                                                             |
|---------------------------------------|-----------------------------------------------------------------------------------------------------------------------|
| Apache AGE / Cypher query language    | Recursive CTEs through `relations` cover ~5-hop traversal. AGE adds ops burden without covering a real v1 use case.  |
| Standalone triple store               | Would re-implement Wikidata's ops burden. Postgres + controlled vocabulary + recursive CTEs is sufficient.            |
| Pure EAV                              | Controlled vocabulary prevents the typo-into-new-attribute failure mode that makes pure EAV databases useless at scale.|
| schema.org as structural schema       | schema.org URIs appear only in `entities.type_uri` for interop. The structural schema is owned by factvault.          |
| LLM-computed confidence scores        | Deterministic formula only. LLM confidence guesses are not reproducible, not auditable, and not trustworthy for v1.  |
| Pre-computed GraphRAG community reports | On-demand story assembly is the primary anti-hallucination payload. Community reports are an optimization for a scale problem we don't have in v1. |

---

## 10. Open Questions / Known Unknowns

These are genuine gaps left open after the design session. They are listed here, not silently filled in.

1. **JWT issuer and key rotation strategy.** The design assumes JWT auth with `tenant_id` in claims, but the issuer, key algorithm (RS256 vs. ES256), and key rotation strategy are not specified. Decision needed: self-issued JWTs (factvault-owned JWKS endpoint) vs. external IdP (Auth0, Keycloak). Key rotation policy must be defined before production deployment.

2. **Tenant management interface.** How is a new tenant created? Options: (a) admin CLI (`factvault tenant create --name "Acme Research"`), (b) an admin-only REST endpoint behind a separate auth token, (c) a Kubernetes Job that runs the DDL migration for a new tenant. No decision made.

3. **trafilatura configuration flags.** The archive worker uses trafilatura with `include_comments=False, include_tables=True, no_fallback=False`. The exact flags for handling paywalled content, JavaScript-rendered pages, and PDF documents have not been tested against the target collector corpus. The default flags are a reasonable starting point but may require adjustment per deployment.

4. **Wayback rate limits and retry policy.** The Internet Archive SPN2 API imposes rate limits that vary by access tier. The current design retries up to 3 times over 10 minutes on failure. The exact rate limit for unauthenticated vs. authenticated SPN2 access, and whether to support authenticated access for higher-volume deployments, is unresolved. This may require an API key management story.

5. **`source_snapshots` table.** When `source_verifications` detects `content-changed`, the design references a `source_snapshots` table for storing the re-fetched `raw_text`. This table is not yet fully specified. It needs at minimum: `(source_id, snapshot_at, raw_text, content_hash)`. The retention policy (keep all snapshots? keep last N? compress?) is an open question.

6. **PDF support in the upload collector.** The `collectors/upload.py` accepts HTML or PDF. The PDF-to-text extraction library (pdfminer, pdfplumber, pypdf) has not been selected. Character offsets in PDF-extracted text are less reliable than HTML-extracted text. The excerpt-offset check strategy for PDF sources requires a decision.

7. **Embedding model versioning.** If the embedding model is upgraded from BGE-M3 to a future model, all stored embeddings are invalidated. The migration strategy (re-embed all rows, maintain a `model_version` column, run dual-index during transition) is not specified.

8. **Rate limiting and quotas on the REST API and MCP server.** Per-tenant rate limiting (requests/minute, facts/day) is expected in a multi-tenant SaaS deployment but is not specified. The enforcement layer (API gateway, FastAPI middleware, Redis token bucket) is unresolved.

