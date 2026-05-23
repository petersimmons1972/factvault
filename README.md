# factvault

> Every fact grounded in a verifiable, durably-archived source.

![Mission Operations Control Room, NASA, Apollo 10, 1969](docs/assets/posters/mission-control-apollo10.jpg)
*Source: NASA, "View of the Mission Control Center during Apollo 10 telecast," S69-34555, 19 May 1969. Public domain (US Government work).*

---

**factvault** is a self-hostable research database where LLM hallucination at the retrieval layer is structurally impossible — not hoped-away. Every fact carries the verbatim excerpt, character offsets, URL, SHA-256 content hash, Wayback Machine snapshot, and ongoing verification status that produced it. An LLM that fabricates an excerpt during extraction gets rejected before the database INSERT, not discovered later in a post-mortem.

The whole project exists because of one non-negotiable commitment:

> **If the original URL dies tomorrow, the captured text, archived snapshot, and content hash remain authoritative forever.**

---

## The Four Pillars (in priority order)

**1. Source Existence** — the point of the project.
Every source captured with `raw_text`, `archive_url`, and `SHA-256 content_hash`. Periodic re-verification logged in an append-only table. Nothing deleted, overwritten, or tombstoned. Verbatim excerpt offsets verified deterministically before any statement touches the database.

**2. Structured Facts** — Wikidata-inspired statement model on Postgres.
Controlled property vocabulary (`founded_in`, `ceo`, `raised_usd`) prevents the silent-divergence failure mode where three synonymous slugs accumulate in an EAV database until nobody trusts any of them. Strict mode (default) queues unknown slugs for human review. Permissive mode available for rapid prototyping.

**3. Cross-Source Corroboration** — deterministic confidence from independent source count.
One independent source: ceiling 0.50. Two: 0.85. Three or more: 0.95. Independence determined by publisher domain and trigram similarity — wire copy republished under a different masthead does not count twice. The LLM never sets confidence.

**4. Story Assembly** — one code path for all output.
`bundle_assembler(entity_ids, depth)` produces both pre-computed entity-keyed **dossiers** (`depth=0`) and on-demand query-keyed **stories** (`depth=2–3`). Every bundle carries full source-existence metadata on every fact.

---

## Quickstart

```bash
git clone https://github.com/petersimmons1972/factvault
cd factvault
cp .env.example .env

# Host-run commands use localhost, not the in-compose service name.
export FACTVAULT_DATABASE_URL='postgres://factvault:factvault@localhost:5432/factvault?sslmode=disable'
export FACTVAULT_DEV_TENANT_ID='11111111-1111-1111-1111-111111111111'

docker compose up -d postgres embedder
go build -o bin/factvault ./cmd/factvault
./bin/factvault migrate

# Load a bundled example and assemble its first dossier.
./bin/factvault example load ai-startup-tracking \
  --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker dossier \
  --tenant "$FACTVAULT_DEV_TENANT_ID" \
  --limit 10

# Verify the stack is ready
./bin/factvault doctor \
  --embedder-url http://localhost:8081 \
  --llm-url http://localhost:11434/v1
```

Current supported Python versions for local development are 3.12 and 3.13. Python 3.14 is temporarily excluded because `pytest-asyncio` still emits an upstream deprecation warning there during test runs.

The `doctor` command runs seven checks — database reachability, RLS policies, Wayback API, embedding model, LLM endpoint, and a canary fact ingest end-to-end — and exits non-zero on any failure with a remediation command.

```
[1/7] Database reachable ...................... OK
[2/7] pgvector extension loaded .............. OK
[3/7] RLS policies applied ................... OK
[4/7] Wayback API reachable .................. OK
[5/7] Embedding model loadable ............... OK (BGE-M3 / 1024d)
[6/7] LLM endpoint responding ................ OK (http://localhost:11434/v1)
[7/7] Canary fact ingest end-to-end .......... OK
```

For the full five-minute path from clone to a JWT-authenticated dossier query, see [docs/getting-started.md](docs/getting-started.md). For day-two operations, see [docs/operator-guide.md](docs/operator-guide.md).

---

## Dossier or Story?

![Dossier vs. Story — Retrieval Modes](docs/assets/svg/dossier-vs-story.svg)

A **dossier** answers *"tell me about X"* — pre-computed nightly for a registered entity, served from cache. A **story** answers *"what's going on with this idea"* — assembled on demand via recursive graph traversal, no entity pre-registration required.

Both share one assembler and one bundle JSON shape. See [docs/superpowers/specs/2026-05-22-factvault-design.md §2](docs/superpowers/specs/2026-05-22-factvault-design.md#2-dossiers-vs-stories) for three worked examples of each type.

---

## The Six-Stage Pipeline

![Factvault Six-Stage Ingestion Pipeline](docs/assets/svg/pipeline-stages.svg)

| # | Stage | Key guarantee |
|---|-------|---------------|
| 1 | **Collect** | Idempotent on `(tenant_id, url)`. Raw HTML stored at INSERT; never re-fetched downstream. |
| 2 | **Archive** | `raw_text` extracted via trafilatura. Wayback SPN2 snapshot submitted. `raw_html` zlib-compressed. |
| 3 | **Extract** | Deterministic extractors run first. LLM runs on uncovered text only. Excerpt-offset check rejects hallucinations before INSERT. |
| 4 | **Corroborate** | Confidence recomputed from scratch on every run. Conflicts surfaced in `v_conflicts`, never silently resolved. |
| 5 | **Verify** | Daily CronJob. Append-only `source_verifications` log. No source or statement ever deleted. |
| 6 | **Relate** | `relations` table kept in sync with entity-valued statements. Embedding-near edges for graph traversal. |

---

## Confidence Formula

![Confidence Formula and Conflict Surface](docs/assets/svg/confidence-formula.svg)

Confidence is computed in `factvault/assembler/confidence.py`. The formula is deterministic and auditable: independence is tested by publisher domain and trigram similarity, not guessed. No statement ever reaches 1.0 through the automated pipeline.

---

## What a Bundle Looks Like

Every fact returned by `GET /entities/{id}/dossier` or `POST /stories` looks like this:

```json
{
  "property": { "slug": "acquired", "label": "Acquired" },
  "value": { "entity": { "label": "Acme Corp" } },
  "confidence": 0.85,
  "sources": [
    {
      "url": "https://www.reuters.com/markets/deals/megacorp-acquires-acme-2025-11-14/",
      "publisher": "reuters.com",
      "content_hash": "a3f2c1d4e5b6a7f8...",
      "archive_url": "https://web.archive.org/web/20251114183000/https://www.reuters.com/...",
      "excerpt": "MegaCorp Inc. announced Tuesday it would acquire Acme Corp for $4.2 billion...",
      "excerpt_offset_start": 1243,
      "excerpt_offset_end": 1396,
      "verification_status": "live"
    }
  ]
}
```

The `excerpt_offset_start` / `excerpt_offset_end` pair is not advisory metadata. It is a load-bearing guarantee: those character offsets are verified against `raw_text` before the row is written and re-verified on every daily verification run. If an LLM extractor fabricates an excerpt, the offsets will not match any text in the source body, and the statement is rejected.

---

## Connecting Your LLM Stack

**REST API** — standard Bearer JWT, RFC 9457 error responses:
```bash
# Dossier for a registered entity
GET /entities/{id}/dossier

# On-demand cross-entity story
POST /stories
{"query": "biotech CFO departures and SEC inquiries", "depth": 2, "max_facts": 500}
```

**MCP server** — works with Claude Desktop, Cursor, or any agent stack supporting MCP:
```python
factvault__entity_lookup(entity_name="Acme Corp", tenant_id="...")
factvault__story_query(query="acquisition chain narrative", depth=2)
factvault__fact_query(property_slug="raised_usd", min_confidence=0.5)
```

LLM backend is pluggable via OpenAI-compatible API. Default: Ollama at `localhost:11434`. Swap to any hosted provider with `FACTVAULT_LLM_BASE_URL` and `FACTVAULT_LLM_API_KEY`.

---

## First Authoring Task

Before ingesting your first document, define your property vocabulary:

```bash
factvault props create --slug raised_usd --type number --label "Raised (USD)"
factvault props create --slug ceo --type entity_ref --label "Chief Executive Officer"
factvault props create --slug founded_in --type date --label "Founded"
```

The controlled vocabulary is not bureaucracy — it prevents `founded_in`, `founding year`, and `yearFounded` from silently diverging into three properties that all mean the same thing. See [docs/guides/defining-properties.md](docs/guides/defining-properties.md) for the full authoring guide including examples for four domains.

---

## Go Deeper

| Document | What it covers |
|----------|---------------|
| [Design Spec](docs/superpowers/specs/2026-05-22-factvault-design.md) | Complete architecture — all four pillars, all six pipeline stages, full DDL, bundle JSON shape, retrieval API, operational requirements |
| [Source Existence](docs/concepts/source-existence.md) | Why `raw_text` + `archive_url` + `content_hash` together; the verification lifecycle; what happens when URLs die |
| [Facts and Sources](docs/concepts/facts-and-sources.md) | The statement model; how a fact differs from a source; what excerpt offsets mean and why they exist |
| [Dossiers vs. Stories](docs/concepts/dossiers-vs-stories.md) | Full treatment of both modes including all worked examples |
| [Confidence and Corroboration](docs/concepts/confidence-and-corroboration.md) | The deterministic confidence formula; what independence means; how to read `v_conflicts` |
| [Defining Properties](docs/guides/defining-properties.md) | The one mandatory authoring task before first ingest |
| [5-Minute Getting Started](docs/getting-started.md) | Clone-to-dossier walkthrough for a fresh local operator |
| [Operator Guide](docs/operator-guide.md) | Runtime components, configuration, health checks, backups, upgrades, and troubleshooting |
| [Frontier Models](docs/guides/frontier-models.md) | Explicit opt-in path and guardrails for hosted LLM extraction |

---

## Status

**Active Go implementation.** The repository now includes the Go CLI, migrations, workers, REST API, MCP server, doctor checks, example loader, Postgres store interfaces, and deploy scaffolding. Open issues continue to track the remaining backend and Tier 1 compose polish.

---

## License

MIT.
