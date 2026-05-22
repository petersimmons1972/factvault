# Source Existence

> "If the original URL dies tomorrow, the captured text, archived snapshot, and content hash remain authoritative forever."

The source existence layer is Pillar 1 of factvault's four architectural pillars — and the reason the project exists. Every other pillar (structured facts, corroboration, bundle assembly) is only useful if the evidence underlying each fact cannot be fabricated or silently erased. This document explains what source existence means in practice, how factvault captures and protects it, and what the system guarantees when sources degrade or disappear.

---

## What "source existence" means

A source is not a URL. A URL is a pointer. Pointers rot: the original article moves, the paywall goes up, the site is acquired and redirected, the company folds. In any system where a fact is tied only to a URL, every fact is one infrastructure failure away from being unverifiable.

Source existence means the system holds the content itself — the raw text of the source at the moment it was fetched — along with enough cryptographic and archival evidence to prove that the content is authentic and unchanged. Three components, each insufficient alone, combine to deliver this guarantee:

1. **`raw_text`** — the full body of the source document, stored in the database permanently at ingest time. It is never re-fetched; queries that need source content read `raw_text` directly.
2. **`content_hash`** — a SHA-256 hex digest of the raw HTTP response body at first fetch. It establishes a tamper-evident baseline: if the content ever changes, the new hash diverges from the stored one.
3. **`archive_url`** — a Wayback Machine Save Page Now snapshot captured at ingest. Supplementary insurance: if a reader wants to verify the capture independently, the Wayback URL provides a second copy under a neutral third-party domain.

All three are stored in the `sources` table. The `raw_text` column is the primary durability guarantee; `archive_url` is best-effort and a missing value does not block ingestion.

---

## Full-body capture and the sources table

At Stage 1 (Collect), a `sources` row is created with the URL, a `content_hash`, compressed `raw_html`, and `status = 'collected'`. At Stage 2 (Archive), the worker populates `raw_text` from the HTML, captures the Wayback snapshot, and advances the status to `'archived'`. From this point forward, the source body is durable — no worker re-fetches the URL to answer a question about this source.

```sql
CREATE TABLE sources (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL,
    url              TEXT NOT NULL,
    fetched_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    content_hash     TEXT NOT NULL,        -- SHA-256 hex of raw body at fetch time
    raw_html         BYTEA,               -- zlib-compressed raw HTML
    raw_text         TEXT,                -- populated at Stage 2; permanent thereafter
    archive_url      TEXT,                -- Wayback Save Page Now URL
    publisher        TEXT,
    title            TEXT,
    published_at     TIMESTAMPTZ,
    embedding        vector(1024),        -- BGE-M3 embedding of raw_text
    last_verified_at TIMESTAMPTZ,
    status           TEXT NOT NULL DEFAULT 'collected'
                     CHECK (status IN (
                         'collected', 'archived', 'extracted',
                         'verified', 'link-rot', 'content-changed'
                     )),
    UNIQUE (tenant_id, url)
);
```

The `status` column tracks the source through its lifecycle. A `source` with `status = 'link-rot'` is a source whose URL returned an error on re-verification — but the `raw_text` is unaffected and remains the evidentiary record. No statements are deleted when a URL goes dead. The `content_hash` that was computed at first fetch is never updated in place; content changes are recorded in `source_verifications`, not by overwriting the original hash.

---

## Excerpt offsets: the anti-hallucination gate

Every fact stored in factvault is grounded in a verbatim excerpt from a source. The excerpt is stored in the `statement_sources` junction table alongside character-level offsets into `sources.raw_text`:

```sql
CREATE TABLE statement_sources (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    statement_id          UUID NOT NULL REFERENCES statements(id) ON DELETE CASCADE,
    source_id             UUID NOT NULL REFERENCES sources(id),
    excerpt               TEXT NOT NULL,
    excerpt_offset_start  INTEGER NOT NULL,
    excerpt_offset_end    INTEGER NOT NULL,
    extraction_method     TEXT NOT NULL,
    extracted_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    confidence            NUMERIC(4,3)
);
```

Before any `statement_sources` row is written, the extraction worker runs a deterministic offset check:

```python
actual = source.raw_text[offset_start:offset_end]
if actual != excerpt:
    raise ExcerptMismatch(
        f"Claimed excerpt does not match source body at [{offset_start}:{offset_end}]. "
        f"Expected: {excerpt!r}. Got: {actual!r}."
    )
```

An LLM that fabricates an excerpt will produce offsets that point to a different passage or no passage at all. The check catches this before the INSERT and rejects the statement entirely. The statement is never written to the database with a fabricated excerpt. This is the primary anti-hallucination mechanism at the extraction boundary — a structural check, not a prompt instruction.

---

## The source_verifications append-only log

Source content can change over time. A URL may go dead (404), redirect to unrelated content, or silently serve a different article body. The `source_verifications` table is an append-only audit log of every periodic re-check:

```sql
CREATE TABLE source_verifications (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id        UUID NOT NULL REFERENCES sources(id),
    tenant_id        UUID NOT NULL,
    verified_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    status           TEXT NOT NULL
                     CHECK (status IN (
                         'live', 'link-rot', 'content-changed', 'excerpt-missing'
                     )),
    new_content_hash TEXT,
    notes            TEXT
);
```

The four verification statuses have precise meanings:

| Status | Meaning |
|---|---|
| `live` | URL returned a 200 response; SHA-256 of response body matches the stored `content_hash`. All excerpts verified at their stored offsets (±20 character tolerance for whitespace drift). |
| `link-rot` | URL returned a non-200 status or connection error. The stored `raw_text` and `archive_url` remain the evidentiary record. |
| `content-changed` | URL returned a 200 response but the SHA-256 hash of the new body differs from `content_hash`. The new hash is stored in `new_content_hash`. The original `content_hash` is unchanged. |
| `excerpt-missing` | URL is live and hash matches, but one or more excerpts no longer appear at their stored offsets (beyond the 20-character tolerance). Indicates post-hoc editorial changes to the source body. |

Rows in `source_verifications` are never updated or deleted. A Postgres trigger enforces this:

```sql
CREATE TRIGGER trg_source_verifications_no_update
    BEFORE UPDATE ON source_verifications
    FOR EACH ROW EXECUTE FUNCTION deny_source_verifications_mutation();

CREATE TRIGGER trg_source_verifications_no_delete
    BEFORE DELETE ON source_verifications
    FOR EACH ROW EXECUTE FUNCTION deny_source_verifications_mutation();
```

The `verify` worker runs daily as a Kubernetes CronJob. Any source with `last_verified_at` older than 7 days, or `last_verified_at IS NULL`, is re-fetched. Sources with `last_verified_at` older than 30 days are always re-verified regardless of prior status.

---

## How factvault implements this: a worked example

**Day 0 — Ingest.** A financial news article is submitted: `https://www.reuters.com/business/acme-acquires-beta-inc-for-450m-2023-04-12/`. The collect worker fetches the URL, computes `content_hash = "a3f7c2..."`, stores compressed `raw_html`, and creates a `sources` row with `status = 'collected'`. The archive worker extracts `raw_text`, triggers Wayback Save Page Now (`archive_url = "https://web.archive.org/web/20230412.../https://..."`), and advances `status = 'archived'`.

**Day 0 — Extract.** The extract worker identifies the claim: "Acme Corp acquired Beta Inc for $450M." The LLM returns the excerpt `"Acme Corp agreed to acquire Beta Inc for approximately $450 million"` with `excerpt_offset_start = 1842`, `excerpt_offset_end = 1905`. The worker verifies: `source.raw_text[1842:1905] == "Acme Corp agreed to acquire Beta Inc for approximately $450 million"`. Match confirmed. The `statement_sources` row is written.

**Day 0 — Corroborate.** A Bloomberg article covering the same acquisition is ingested. It has a different publisher domain and its first 2000 characters have trigram similarity 0.31 against the Reuters lede. The two sources are independent. `compute_confidence()` returns `min(per_source_max, 0.85)` — the statement now has confidence 0.85.

**Week 6 — Verification.** The Reuters article URL now returns a 404 (the outlet restructured its archive). The verify worker writes a `source_verifications` row with `status = 'link-rot'`. `sources.status` is updated to `'link-rot'`. The `raw_text`, `content_hash`, and `archive_url` are untouched. The acquisition statement remains fully verifiable: the bundle assembler surfaces `verification_status: "link-rot"` alongside the verbatim excerpt, the original URL, and the Wayback snapshot URL. A downstream LLM or human reviewer can still confirm the exact words that grounded the fact.

The URL is dead. The evidence is not.
