# CLI Reference

Complete subcommand and flag reference for the `factvault` binary. All subcommands are verified
against the cobra definitions in `cmd/factvault/`.

---

## Global

```
factvault [command]
```

The binary does not accept persistent global flags. All flags are per-subcommand or per-group.

---

## migrate

Run goose database migrations (schema up).

```bash
./bin/factvault migrate [--dsn DSN]
```

| Flag | Default | Env var | Description |
|------|---------|---------|-------------|
| `--dsn` | -- | `FACTVAULT_DATABASE_URL` | Postgres DSN |

**Note:** `factvault migrate` requires CREATE EXTENSION privileges (pgvector, pg_trgm). Use the
superuser DSN `FACTVAULT_MIGRATE_DATABASE_URL` for this command. All other workers and the API
use `FACTVAULT_DATABASE_URL` (the `app_user` role), which has restricted privileges.

---

## init

One-shot first-boot initialiser: generates JWT keys, runs doctor health checks, and optionally
loads an example domain.

```bash
./bin/factvault init [flags]
```

| Flag | Default | Env var | Description |
|------|---------|---------|-------------|
| `--dsn` | -- | `FACTVAULT_DATABASE_URL` | Postgres DSN |
| `--tenant` | `11111111-1111-1111-1111-111111111111` | `FACTVAULT_DEV_TENANT_ID` | Tenant UUID |
| `--key-dir` | `.local` | -- | Directory to write `private.pem` / `public.pem` |
| `--example` | `ai-startup-tracking` | -- | Example name to load; set to empty string to skip |
| `--skip-example` | `false` | -- | Skip example data loading |

`init` is idempotent: if keys already exist in `--key-dir`, key generation is skipped. If the
example is already loaded, `example.Insert` is a no-op (INSERT ... WHERE NOT EXISTS).

---

## doctor

Run first-boot and ongoing health checks.

```bash
./bin/factvault doctor [flags]
```

| Flag | Default | Env var | Description |
|------|---------|---------|-------------|
| `--dsn` | -- | `FACTVAULT_DATABASE_URL` | Postgres DSN |
| `--llm-url` | -- | `FACTVAULT_LLM_URL` | LLM base URL for the LLM check |
| `--embedder-url` | -- | `FACTVAULT_EMBEDDER_URL` | Embedder service base URL |
| `--wayback-url` | -- | `FACTVAULT_WAYBACK_URL` | Wayback Machine base URL |
| `--required-only` | `false` | -- | Exit 0 when only optional checks (LLM, embedder, Wayback) fail; show WARN instead of FAIL |

Checks: postgres + pgvector, migrations, RLS enforcement, LLM endpoint, embedder health +
vector probe, Wayback reachability, canary fact assembly.

---

## api

Run the JWT-protected REST API server.

```bash
./bin/factvault api [flags]
```

| Flag | Default | Env var | Description |
|------|---------|---------|-------------|
| `--dsn` | -- | `FACTVAULT_DATABASE_URL` | Postgres DSN |
| `--jwt-public-key` | -- | `FACTVAULT_JWT_PUBLIC_KEY` | Path to PEM public key for JWT verification |
| `--addr` | `:8080` | `FACTVAULT_API_ADDR` | Listen address |

---

## mcp

Run the MCP server over stdio (for Claude Desktop, Cursor, or any MCP-compatible agent).

```bash
./bin/factvault mcp [flags]
```

| Flag | Default | Env var | Description |
|------|---------|---------|-------------|
| `--dsn` | -- | `FACTVAULT_DATABASE_URL` | Postgres DSN |
| `--jwt-public-key` | -- | `FACTVAULT_JWT_PUBLIC_KEY` | Path to PEM public key for JWT verification |
| `--auth-token` | -- | `FACTVAULT_MCP_AUTH_TOKEN` | Optional default Bearer token for MCP clients that cannot set per-tool authorization |

The `--auth-token` / `FACTVAULT_MCP_AUTH_TOKEN` flag is useful when the MCP client cannot inject
an `authorization` parameter into individual tool calls. Set it to a valid tenant-scoped JWT
generated with `auth token`.

---

## auth

Manage development JWT keys and tokens.

### auth keys

Generate a development RSA key pair (prints PEM to stdout; does not write files).

```bash
./bin/factvault auth keys
```

No flags. To write keys to files, use `init` or redirect stdout.

### auth token

Issue a development RS256 JWT.

```bash
./bin/factvault auth token --jwt-private-key PATH --tenant UUID [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--jwt-private-key` | required | PEM private key file path (or `FACTVAULT_JWT_PRIVATE_KEY`) |
| `--tenant` | required | Tenant UUID to embed in the token |
| `--sub` | `dev` | Token subject claim |
| `--ttl` | `24h` | Token TTL (Go duration format) |

### auth verify

Verify an RS256 JWT and print its claims.

```bash
./bin/factvault auth verify --jwt-public-key PATH --token JWT
```

| Flag | Default | Description |
|------|---------|-------------|
| `--jwt-public-key` | required | PEM public key file path (or `FACTVAULT_JWT_PUBLIC_KEY`) |
| `--token` | required | JWT string to verify |

---

## example

Inspect and load example domains.

### example list

```bash
./bin/factvault example list [--root DIR]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--root` | `examples` | Examples root directory |

### example info NAME

```bash
./bin/factvault example info NAME [--root DIR]
```

Prints the example metadata as JSON.

### example load NAME

```bash
./bin/factvault example load NAME --dsn DSN --tenant UUID [--root DIR]
```

| Flag | Default | Env var | Description |
|------|---------|---------|-------------|
| `--dsn` | required | `FACTVAULT_DATABASE_URL` | Postgres DSN |
| `--tenant` | required | `FACTVAULT_DEV_TENANT_ID` | Tenant UUID |
| `--root` | `examples` | -- | Examples root directory |

---

## brief

Generate and read deterministic evidence briefs.

All `brief` subcommands accept `--dsn` and `--tenant` as persistent flags.

### brief generate

```bash
./bin/factvault brief generate [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dsn` | `FACTVAULT_DATABASE_URL` | Postgres DSN |
| `--tenant` | required | Tenant UUID |
| `--input` | stdin | Path to dossier/story bundle JSON |
| `--source-kind` | `dossier` | Brief source kind: `dossier` or `story` |
| `--entity-id` | -- | Entity UUID for dossier-derived brief |
| `--query` | -- | Query text for story-derived brief |

### brief list

```bash
./bin/factvault brief list [--limit N]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--limit` | `100` | Max records to return |

### brief get BRIEF-ID

```bash
./bin/factvault brief get <brief-id>
```

---

## worker

Run source pipeline workers. All `worker` subcommands share these persistent flags:

| Flag | Default | Env var | Description |
|------|---------|---------|-------------|
| `--dsn` | -- | `FACTVAULT_DATABASE_URL` | Postgres DSN |
| `--tenant` | required | -- | Tenant UUID |
| `--limit` | `100` | -- | Max rows per run |
| `--age-days` | `7` | -- | Verify age threshold in days (verify worker only) |
| `--vocabulary-mode` | `strict` | -- | Property vocabulary mode: `strict` or `permissive` |
| `--llm-provider` | -- | -- | LLM provider for extraction: `local` or `openai` |
| `--llm-model` | -- | `FACTVAULT_LLM_MODEL` | LLM model name |
| `--llm-base-url` | -- | `FACTVAULT_LLM_BASE_URL`, `FACTVAULT_LLM_URL` | LLM base URL |
| `--llm-api-key` | -- | `FACTVAULT_LLM_API_KEY` | LLM API key |
| `--confirm-cost` | `false` | -- | Confirm frontier-model extraction batches above the guardrail threshold |
| `--llm-cost-guardrail-threshold` | `1000` | -- | Paid extractions per run that require confirmation |
| `--feeds` | `config/feeds.yaml` | -- | RSS feed config file (rss worker only) |
| `--once` | `false` | -- | Run one RSS polling cycle and exit (rss worker only) |
| `--interval` | `15m` | -- | Default RSS polling interval (rss worker only) |

### worker collect

Insert a seed source into the pipeline. **Note:** Currently inserts a static stub URL
(`https://example.com/factvault-seed`). The source's historical reference to
[Issue #94](https://github.com/petersimmons1972/factvault/issues/94) is stale: that closed issue
shipped Docker Compose deployment, not collector configurability. For real source ingestion, use
`worker rss` or `worker research`.

```bash
./bin/factvault worker collect --tenant UUID
```

### worker archive

Extract `raw_text` via the Go `stripHTML` tag-stripper, submit a Wayback SPN2 snapshot, and
compress `raw_html`.

```bash
./bin/factvault worker archive --tenant UUID [--limit N]
```

### worker extract

Run deterministic extractors then LLM extraction on archived sources. Excerpt-offset
verification rejects hallucinated excerpts before INSERT.

```bash
./bin/factvault worker extract --tenant UUID \
  --llm-provider local \
  --llm-model llama3.1:8b \
  --llm-base-url http://localhost:11434/v1 \
  [--llm-api-key KEY] \
  [--confirm-cost] \
  [--llm-cost-guardrail-threshold N] \
  [--limit N]
```

Pull the model before first use: `ollama pull llama3.1:8b`

See [Frontier Models](../guides/frontier-models.md) for hosted endpoint setup.

### worker corroborate

Recompute confidence scores for all statements from scratch. One independent source: ceiling
0.50. Two: 0.85. Three or more: 0.95.

```bash
./bin/factvault worker corroborate --tenant UUID
```

### worker verify

Re-check source liveness and re-verify excerpt offsets. Logs results to `source_verifications`.

```bash
./bin/factvault worker verify --tenant UUID [--age-days N] [--limit N]
```

### worker rss

Poll RSS/Atom feeds from `config/feeds.yaml` and ingest source items.

```bash
./bin/factvault worker rss [--feeds PATH] [--once] [--interval DURATION]
```

`--tenant` overrides each feed's YAML tenant. Without it, the feed tenant is used, with
`FACTVAULT_DEV_TENANT_ID` as the final fallback. See [RSS Ingestion](../guides/rss-ingestion.md).

### worker embed

Populate NULL embedding columns for entities, statements, and sources.

```bash
./bin/factvault worker embed --tenant UUID [--embedder-url URL] [--limit N]
```

| Flag | Default | Env var | Description |
|------|---------|---------|-------------|
| `--embedder-url` | `http://localhost:8080` | `FACTVAULT_EMBEDDER_URL` | Embedder service base URL |

Idempotent: rows with existing embeddings are skipped. See [Embedding Population](../guides/embedding-population.md).

### worker research ENTITY

Generate perspective-angled search queries for an entity, fetch candidate pages via SearXNG,
and collect them into the pipeline.

```bash
./bin/factvault worker research "Entity Name" --tenant UUID \
  --llm-base-url URL \
  --llm-model MODEL \
  [--perspectives N] \
  [--questions-per N] \
  [--results-per-query N] \
  [--max-fetches N] \
  [--entity-type TYPE]
```

| Flag | Default | Env var | Description |
|------|---------|---------|-------------|
| `--perspectives` | `5` | -- | Number of research perspectives |
| `--questions-per` | `4` | -- | Questions per perspective |
| `--results-per-query` | `5` | -- | Search results per query |
| `--max-fetches` | `40` | -- | Hard ceiling on page fetches |
| `--entity-type` | `""` | -- | Entity type hint (e.g. `Person`, `City`, `Company`) |
| `--llm-base-url` | required | `FACTVAULT_LLM_BASE_URL`, `FACTVAULT_LLM_URL` | LLM base URL |
| `--llm-model` | required | `FACTVAULT_LLM_MODEL` | LLM model name |
| `--llm-api-key` | -- | `FACTVAULT_LLM_API_KEY` | LLM API key |

Exactly one positional argument (the entity label) is required.

See [Active Acquisition](../guides/active-acquisition.md) for full documentation.

### worker dossier

Precompute dossier bundles and cache them in the `dossiers` table.

```bash
./bin/factvault worker dossier --tenant UUID [--limit N]
```
