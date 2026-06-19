# Dossiers vs. Stories

A dossier answers "tell me about X." A story answers "tell me what's going on with this idea." Use a dossier when you know the entity you care about; use a story when you have a question that spans an unknown set of entities.

This is the most consequential choice a factvault user makes at query time. It determines whether the system pre-computes and caches the answer (dossier) or executes a recursive graph traversal on demand (story). It determines whether the result has a stable URL you can bookmark (dossier) or a one-time response to a query body (story). It determines whether you are monitoring known things over time or investigating an emerging situation across unknown entities. Getting this choice right shapes both the quality of the output and the compute cost.

---

## The comparison table

| Dimension | Dossier | Story |
|---|---|---|
| **Keyed by** | Entity ID or canonical name | Free-text query |
| **Question answered** | "Tell me everything about entity X" | "What's happening with [concept / event / trend]" |
| **Computation** | Pre-computed nightly; served from cache | On-demand; recursive CTE graph traversal at query time |
| **Scope** | One entity, all statements and qualifiers | Multi-entity subgraph, depth-bounded |
| **Use case** | Monitoring a known set of entities over time | Investigative queries spanning unknown entity sets |
| **Stable URL** | Yes — `GET /entities/{id}/dossier` | No — `POST /stories` with query body |

---

## One assembler, two invocations

Both dossiers and stories are produced by the same Go assembler function in `internal/assembler/bundle.go`:

```go
func Assemble(
    ctx context.Context,
    tx pgx.Tx,
    entityIDs []string,
    depth int,
    tenantID string,
) (*Bundle, error) {
    ...
}
```

The `depth` parameter is the only structural difference:

- **Dossier:** `depth=0` — return statements for the supplied `entity_ids` only.
- **Story:** `depth=1`, `2`, or `3` — execute a recursive CTE through the `relations` graph, collecting all entities within that many hops from the seed set.

Every bundle, regardless of depth, carries the same structure: entities, statements with rank and confidence, qualifiers, sources with `raw_text` excerpts and offsets, `archive_url`, and `verification_status` per source. A downstream LLM receiving either a dossier or a story bundle has complete sourcing for every claim in the output.

---

## Dossier use cases

**VC associate monitoring 200 AI startups.**
The associate pre-registers each company as an entity with `type_uri: "https://schema.org/Organization"`. Each night the dossier worker opens a tenant-scoped `pgx.Tx` and calls `assembler.Assemble(ctx, tx, []string{entityID}, 0, tenantID)` for all 200 entities and caches the result. The bundle for each company contains funding rounds (amount, round type, lead investor, date, source), headcount statements, product announcements, SEC/EDGAR filings, founders, and press coverage. The LLM receives the pre-assembled bundle, has complete sourcing for every claim, and generates a weekly portfolio digest that cites each number back to the verbatim excerpt that produced it.

The dossier pattern works here because the set of entities is known and stable. The nightly cache means the query is instant. The `GET /entities/{id}/dossier` URL is stable and can be linked in Slack or embedded in a dashboard.

**Journalist tracking 50 politicians.**
Each politician is an entity. The dossier bundle contains voting record (bill, vote, date, chamber, source), campaign donors (donor entity, amount, date, FEC filing source), quotes (verbatim text with full character offsets, outlet, date, archive_url), committee memberships (committee, role, start/end dates), and family business ties (related entity, relationship type, confidence, sources). When the journalist asks the LLM to draft a profile, the bundle provides only sourced facts — the verbatim excerpt for every quote eliminates paraphrasing errors, and the FEC citation for every donor figure eliminates unverified claims.

**Pharma analyst tracking Phase II/III drug candidates.**
Each drug compound is an entity. The dossier bundle contains trial registrations (ClinicalTrials.gov NCT ID, phase, indication, sponsor, enrollment dates), primary endpoints (description, met/not met, p-value, source), adverse event summaries (MedDRA code, incidence, severity), publications (DOI, journal, trial result summary, excerpt, archive_url), regulatory correspondence (FDA/EMA document type, date, outcome), and competing drugs (related entity, development stage, confidence). Every claim about endpoints or adverse events traces to the trial registration or publication that established it.

---

## Story use cases

**CFO departures and SEC inquiries.**
A journalist asks: "Which biotechs lost a CFO in the last 18 months, and which of those also had an SEC inquiry in the same period?" factvault does not know in advance which entities satisfy both conditions. The story endpoint receives the query, runs embedding similarity search to identify entities in the biotech sector, then expands the graph two hops: departure events → company entities → regulatory event entities. The returned bundle contains only entities that appear in both subgraphs, with the sourced statements that establish both conditions. The LLM receives a factually grounded entity list with evidence for each inclusion criterion.

This is not a dossier use case because the entity set is not known in advance. The analyst does not have a list of biotechs to pre-register; the point of the query is to discover which entities satisfy the criteria.

**Acquisition chain narrative.**
A researcher asks about a three-party sequence: "Acme acquires DataCo → FTC review → MegaCorp layoffs." These are three entities connected by event-typed relations. The story endpoint calls `assembler.Assemble(ctx, tx, seedIDs, 3, tenantID)` — the recursive CTE traverses: Acme → acquisition relation → DataCo → FTC review relation → regulatory entity, and separately DataCo → related-company relation → MegaCorp → layoff event relation. The bundle stitches the three-entity narrative with full sourcing at every node: the Reuters press release excerpt for the acquisition, the FTC docket source for the review, the Bloomberg news source for the layoffs. The LLM writes a coherent timeline that names sources at each step.

**State AI legislation and PAC donors.**
A policy analyst asks: "Which states passed AI legislation since 2024, and which bill sponsors received donations from AI PACs?" The story endpoint resolves "AI legislation" entities from the bills subgraph (embedding cosine > 0.6), then expands to sponsor entities (depth 2), then expands to donor relationships (depth 3). The bundle contains state entities, bill entities with vote counts and passage dates, sponsor entities with FEC donor statements, and PAC entities identified by description embedding similarity. Every donation figure and vote count is backed by a verbatim excerpt from FEC data or state legislature records.

---

## Choosing between them

Use a **dossier** when:
- You can name the specific entities you care about.
- You want the answer to be available immediately and consistently (nightly cache).
- You are monitoring a known set over time and want a stable URL per entity.
- Your downstream LLM prompt is structured around a single entity's full profile.

Use a **story** when:
- The query defines an idea, event, or pattern and you want to discover which entities are relevant.
- The entity set is unknown in advance (investigative queries).
- You need recursive graph traversal — the answer requires following chains of relationships.
- Latency tolerance exists for on-demand assembly (stories are computed at request time).

When unsure, start with a dossier. Register the key entities for your domain, run a nightly dossier job, and observe which facts are available. If you find yourself wanting to ask cross-entity questions that the dossier structure cannot answer, switch to a story for those specific queries.

---

## How factvault implements this

The `dossiers` table caches pre-assembled bundles for nightly use:

```sql
CREATE TABLE dossiers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL,
    entity_id   UUID NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    assembled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    bundle      JSONB NOT NULL,
    ttl_hours   INTEGER NOT NULL DEFAULT 24,
    UNIQUE (tenant_id, entity_id)
);
```

The dossier worker opens a tenant-scoped `pgx.Tx` and calls `assembler.Assemble(ctx, tx, []string{entityID}, 0, tenantID)` for each entity and upserts the result into `dossiers`. A GET request to `GET /entities/{id}/dossier` returns the cached bundle if `assembled_at > now() - interval '24 hours'`; otherwise, it recomputes on demand.

The story endpoint (`POST /stories`) accepts a query body, runs embedding similarity search against `entities.embedding` to find seed entities (cosine threshold 0.6), and calls `assembler.Assemble(ctx, tx, seedIDs, 2, tenantID)` — adjustable to `depth=3` for deeper traversal. The recursive CTE that expands the graph gates each edge traversal at a minimum confidence of 0.4 to prevent low-confidence synthetic edges from polluting story results.

Both paths return identical bundle JSON structure. The calling LLM does not need to distinguish them.

---

## Cosine Story Seeding: Shipped

Cosine-similarity story seeding is fully implemented in `internal/retrieval/service.go`
(`cosineSeedThreshold = 0.6`). The retrieval service uses a cosine-first strategy:

1. Embed the story query text via the embedder sidecar (BAAI/bge-m3, 1024 dimensions).
2. Call `vectorStore.SearchNearest`, filter results with cosine similarity >= 0.6.
3. If at least one entity meets the threshold, return those as story seeds.
4. If the embedder is unavailable, returns zero cosine results, or the query is empty, fall back
   to ILIKE substring match on `entities.label` and `description`.

The ILIKE fallback is always graceful -- a downed embedder never causes a 500 to API callers.

**Prerequisite:** entities must have populated `embedding` columns for the cosine path to
activate. Run `worker embed` after extraction to backfill NULL embeddings. See
[Embedding Population](../guides/embedding-population.md) for the full procedure.
