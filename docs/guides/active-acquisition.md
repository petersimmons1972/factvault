# Active Acquisition: `worker research`

The `worker research` subcommand is factvault's active acquisition loop. Instead of waiting for RSS
feeds or manual URL submission, it drives web research itself: given a seed entity, it generates
perspective-angled search queries with an LLM, fetches candidate pages, and feeds them into the
standard collect pipeline where they become sources waiting for archival and extraction.

The design is inspired by STORM-style perspective-guided query generation: the system first
generates multiple research angles for an entity, then generates concrete web search queries for
each angle, then fetches the top results.

---

## The Guardrail: LLM Decides What to Research, Never What Is True

This is the product's core thesis, and it is enforced structurally, not by convention.

The `internal/research` package has NO database write path. It imports `internal/collectors` but
is explicitly prohibited from importing `internal/workers`. The only thing `worker research`
produces is URLs with collected HTML, each tagged `status='collected'` and `meta.trust_tier='web'`.

After that point the research package is done. It cannot touch:

- **ExtractOnce** -- which calls the LLM extractor to propose facts. Research selects which source
  documents enter the pipeline, but it cannot invoke extraction or bypass its validation gates.
- **Excerpt-offset verification** -- the deterministic gate that rejects LLM-fabricated excerpts.
- **Confidence** -- computed deterministically from independent source count; never settable by the
  acquisition layer.
- **Corroboration** -- the domain of `CorroborateOnce`, which research cannot reach.

The bridge from acquisition to ingest is exactly `SourcePipeline.CollectOnce`. It writes
`status='collected'` and no fact columns. All subsequent pipeline stages -- archive, extract,
corroborate, verify -- run independently of how the source arrived.

If acquisition could write to the truth layer, a search-planning model could promote its own
unverified output into facts, bypassing excerpt verification and independent-source confidence.
The one-way package boundary prevents that failure mode.

**An LLM decides what to research and proposes extracted facts and excerpts. Deterministic checks
require the excerpt to exist at the claimed offset, and the LLM cannot write truth-layer rows or
set fact confidence directly.**

---

## How It Works

```
entity label (e.g. "Acme Corp")
       |
       v
  [LLM Call 1] entity + type -> N research perspectives
       |        (e.g. "funding history", "executive departures",
       |               "regulatory filings", "product announcements")
       |
       v
  [LLM Call 2] perspectives -> batched search queries
       |        (deduped + normalized; caps enforced deterministically)
       |
       v
  SearchCollector (internal/research/research.go)
       |   SearXNG web search per query
       |   Fetch top-N result pages (up to ResultsPerQuery per query)
       |   Hard stop at --max-fetches
       |
       v
  SourcePipeline.CollectOnce
       |   Writes sources with:
       |     status='collected'
       |     meta.trust_tier='web'
       |
       v
  Normal pipeline continues independently:
    worker archive -> worker extract -> worker corroborate
```

Total LLM calls per run: exactly 2, regardless of scale parameters.

---

## Usage

`worker research` requires a reachable SearXNG instance; factvault does not bundle one. Set
`FACTVAULT_SEARXNG_URL` to your operator-managed base URL and verify its `/search` endpoint before
starting a run. The built-in example-domain value is a placeholder, not an operational service.

```bash
export FACTVAULT_SEARXNG_URL='https://search.example.net'

./bin/factvault worker research "Acme Corp" \
  --tenant "$FACTVAULT_DEV_TENANT_ID" \
  --searxng-url "$FACTVAULT_SEARXNG_URL" \
  --llm-base-url http://localhost:11434/v1 \
  --llm-model llama3.1:8b
```

With environment variables instead of flags:

```bash
export FACTVAULT_LLM_BASE_URL=http://localhost:11434/v1
export FACTVAULT_LLM_MODEL=llama3.1:8b

./bin/factvault worker research "Acme Corp" \
  --tenant "$FACTVAULT_DEV_TENANT_ID"
```

The entity argument is required (exactly one positional argument). The LLM base URL and model are
required; the command exits with an error if either is missing.

---

## Config Bounds

These flags control how much work one run does. All have conservative defaults.

| Flag | Default | Description |
|------|---------|-------------|
| `--perspectives` | `5` | Number of research angles the LLM generates |
| `--questions-per` | `4` | Search queries per perspective |
| `--results-per-query` | `5` | Search results fetched per query |
| `--max-fetches` | `40` | Hard ceiling on page fetches per run |
| `--entity-type` | `""` | Entity type hint (e.g. `Person`, `City`, `Company`) |
| `--searxng-url` | `FACTVAULT_SEARXNG_URL` | Operator-managed SearXNG base URL; configure explicitly |

The `--max-fetches` flag is a hard guarantee, not a soft suggestion. The `SearchCollector` tracks
a live fetch counter and stops mid-query if the ceiling is reached, regardless of how many queries
or results upstream produced.

**Projected ceiling per run (defaults):** 2 LLM calls + up to 20 searches (5 perspectives x 4
queries) + up to 40 page fetches. Actual counts are typically lower due to deduplication and
failed fetches.

---

## Cost Expectations

| Resource | Count (default bounds) |
|----------|----------------------|
| LLM calls | 2 (always exactly 2) |
| Web searches | up to perspectives x questions-per = up to 20 |
| Page fetches | up to max-fetches = up to 40 |
| DB writes | up to 40 rows in `sources` (status='collected') |

For local LLMs (Ollama), cost is compute time only. For hosted endpoints, the 2 LLM calls are
bounded: the prompts are small (entity label + perspective list). Each call requests JSON output
at temperature=0, so results are deterministic.

Downstream pipeline cost (archive/extract/corroborate) is the same as for any other source.
Extraction LLM calls are governed by `worker extract`'s own `--limit` and `--confirm-cost` flags.

---

## trust_tier Tagging

Every source collected by `worker research` gets `meta.trust_tier="web"` in the JSONB `meta`
column (added by migration `00005_sources_meta.sql`). This tag is set by `SearchCollector.Collect`
and is the correct place to add trust tier discrimination for downstream consumers.

The `meta` column is free-form JSONB. Future integrations can add more fields; the current
commitment is that research-collected sources always carry `trust_tier="web"`.

---

## Warm-Entity Gap Steering

When calling `research.GenerateQueries` programmatically, you can pass a `hints` slice of
property slugs that are already well-covered for this entity. The LLM is prompted to steer away
from those facets and focus on gaps. The `worker research` subcommand passes `nil` hints (cold
start), but the API is available for operators building their own research pipelines.

---

## SearchCollector Location

`SearchCollector` lives in `internal/research/research.go`, not in `internal/workers/`. This is
load-bearing: keeping the research package free of any dependency on the workers package enforces
the one-way flow from acquisition to ingest with no feedback path back to the fact layer.

---

## After Research: Continue the Pipeline

`worker research` deposits sources with `status='collected'`. Run the remaining pipeline stages
to move them to verified facts:

```bash
./bin/factvault worker archive --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker extract --tenant "$FACTVAULT_DEV_TENANT_ID" \
  --llm-model llama3.1:8b \
  --llm-base-url http://localhost:11434/v1
./bin/factvault worker corroborate --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker dossier --tenant "$FACTVAULT_DEV_TENANT_ID"
```

See the [Operator Guide](../operator-guide.md) for the full worker order and [Frontier
Models](frontier-models.md) for extraction with hosted LLMs.

---

## Related

- [Embedding Population](embedding-population.md) -- prerequisite for cosine story-seeding
- [RSS Ingestion](rss-ingestion.md) -- alternative source population via feeds
- [CLI Reference](../reference/cli.md) -- full flag reference for all subcommands
- [Operator Guide](../operator-guide.md) -- worker sequencing and operational runbook
