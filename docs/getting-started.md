# 5-Minute Getting Started

This guide gets a new operator from clone to a retrievable dossier using the current Go implementation on `main`. It uses Docker for Postgres and the local `factvault` binary for migration, example loading, dossier assembly, and API access.

## Prerequisites

- Docker with the Compose plugin: `docker compose version`
- Go matching `go.mod`: `go version`
- Optional for the full `doctor` pass: an OpenAI-compatible local LLM at `http://localhost:11434/v1`

No hosted API key is required for the default path. Frontier model use is opt-in; see [Frontier Models](guides/frontier-models.md).

## 1. Clone and Configure

```bash
git clone https://github.com/petersimmons1972/factvault.git
cd factvault
cp .env.example .env
```

For host-run commands, override the in-container Postgres hostname. factvault uses two DSNs:
`FACTVAULT_DATABASE_URL` (app_user) is the runtime DSN used by `init`, `api`, `worker`, `mcp`, and
`doctor` — it matches production and exercises the schema's `GRANT`s end-to-end.
`FACTVAULT_MIGRATE_DATABASE_URL` (superuser) is used only by `factvault migrate`, because
`CREATE EXTENSION` requires superuser privileges.

```bash
export FACTVAULT_DATABASE_URL='postgres://app_user:dev_only_local_password@localhost:5432/factvault?sslmode=disable'
export FACTVAULT_MIGRATE_DATABASE_URL='postgres://factvault:factvault@localhost:5432/factvault?sslmode=disable'
export FACTVAULT_DEV_TENANT_ID='11111111-1111-1111-1111-111111111111'
```

## 2. One-Command Setup (recommended)

`make setup` handles everything: starts Postgres and the embedder, waits for database readiness, builds the binary, runs migrations, and calls `factvault init` which generates JWT keys, runs health checks, and loads the `ai-startup-tracking` example.

```bash
make setup
```

`factvault init` is idempotent — re-running will not overwrite existing key files or reload example data.

After `make setup` completes, skip to [step 5](#5-query-the-dossier-through-the-api).

---

## Manual Steps (alternative)

Use these if you prefer to run each step individually.

### 3. Start the Local Services

```bash
docker compose up -d postgres embedder
```

### 4. Build, Migrate, and Initialise

```bash
go build -o bin/factvault ./cmd/factvault
# Migrate runs as the Postgres superuser — CREATE EXTENSION requires superuser
# privileges. It reads FACTVAULT_MIGRATE_DATABASE_URL (exported in step 1).
./bin/factvault migrate
# init (and everything after it) runs as app_user via FACTVAULT_DATABASE_URL —
# matches production, exercises the GRANTs.
./bin/factvault init --tenant "$FACTVAULT_DEV_TENANT_ID"
```

`init` generates JWT keys in `.local/`, runs the doctor health checks, and loads the default example. Keys are written only if they do not exist yet. The awk key-splitting step from older guides is no longer required — `init` writes `private.pem` and `public.pem` directly.

Note on `--dsn`: runtime commands (`init`, `doctor`, `worker`, `api`, `mcp`) reject a `--dsn` value that embeds a password — pass the DSN through `FACTVAULT_DATABASE_URL` instead (as exported in step 1), or use a password-free `--dsn` with `~/.pgpass`/`PGPASSWORD`.

### 4a. Run Readiness Checks

The `init` command runs doctor automatically. You can also invoke it directly:

```bash
./bin/factvault doctor \
  --embedder-url http://localhost:8081 \
  --llm-url http://localhost:11434/v1
```

If you do not have a local LLM running, use `--required-only` to exit 0 when only LLM/embedder/Wayback fail:

```bash
./bin/factvault doctor --required-only
```

Required checks (postgres, migrations, rls, canary) still fail loudly; optional ones show `WARN`.

### 4b. Load an Example and Assemble a Dossier

List the bundled domains:

```bash
./bin/factvault example list
```

Load one domain into the development tenant:

```bash
./bin/factvault example load ai-startup-tracking \
  --tenant "$FACTVAULT_DEV_TENANT_ID"
```

Precompute dossiers for that tenant:

```bash
./bin/factvault worker dossier \
  --tenant "$FACTVAULT_DEV_TENANT_ID" \
  --limit 10
```

The initial example dossier is expected to contain no facts until the full
collect/archive/extract/corroborate pipeline runs. The retrieval path is live and tenant-scoped
immediately; an empty result at this point is not a setup failure.

---

## 5. Query the Dossier Through the API

Keys are in `.local/` (written by `init` or `make setup`). Issue a tenant-scoped token:

```bash
TOKEN=$(./bin/factvault auth token \
  --jwt-private-key .local/private.pem \
  --tenant "$FACTVAULT_DEV_TENANT_ID" \
  --sub local-dev)
```

Start the API:

```bash
./bin/factvault api \
  --jwt-public-key .local/public.pem \
  --addr :8080
```

In another shell, get the example entity ID and fetch its dossier:

```bash
ENTITY_ID=$(docker compose exec -T postgres psql -U factvault -d factvault -At \
  -c "SELECT id FROM entities WHERE tenant_id = '$FACTVAULT_DEV_TENANT_ID' ORDER BY created_at LIMIT 1")

curl -sS \
  -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/entities/$ENTITY_ID/dossier" | jq .
```

A successful response contains an entity bundle for the loaded example entity.

## Full Pipeline Next Steps

After the initial dossier works, run the source pipeline against a tenant.

If you opened a new shell for these commands, re-export `FACTVAULT_DATABASE_URL` with the runtime
`app_user` DSN from step 1. Runtime commands read that environment variable; do not pass its
password-bearing value through `--dsn`.

**Source population** -- choose one or combine:

```bash
# RSS feeds (recommended for ongoing ingestion). --tenant overrides per-feed tenants:
./bin/factvault worker rss \
  --feeds config/feeds.yaml \
  --tenant "$FACTVAULT_DEV_TENANT_ID" \
  --once

# Active research for a specific entity (LLM + web search):
./bin/factvault worker research "Your Entity" \
  --tenant "$FACTVAULT_DEV_TENANT_ID" \
  --searxng-url "$FACTVAULT_SEARXNG_URL" \
  --llm-base-url http://localhost:11434/v1 \
  --llm-model llama3.1:8b

# Pipeline smoke test (static stub only -- not real content):
./bin/factvault worker collect --tenant "$FACTVAULT_DEV_TENANT_ID"
```

For RSS, `--tenant` overrides each feed's configured tenant during collection. Each feed still
needs a YAML `tenant` to enter the current scheduler; tenantless feeds are skipped before the
override is applied. Keep the YAML tenant even when supplying `--tenant`.


**Core pipeline + embedding:**

```bash
./bin/factvault worker archive     --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker extract     --tenant "$FACTVAULT_DEV_TENANT_ID" \
  --llm-model llama3.1:8b --llm-base-url http://localhost:11434/v1
./bin/factvault worker corroborate --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker embed       --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker dossier     --tenant "$FACTVAULT_DEV_TENANT_ID"
```

The dossier will remain empty until the full pipeline stages complete.

See [RSS Ingestion](guides/rss-ingestion.md), [Active Acquisition](guides/active-acquisition.md),
and [Embedding Population](guides/embedding-population.md) for full documentation on the new
pipeline stages. For the full CLI reference, see [docs/reference/cli.md](reference/cli.md).

## Troubleshooting

| Symptom | Check | Fix |
|---|---|---|
| `database DSN required`                       | `echo "$FACTVAULT_DATABASE_URL"`        | Export the localhost DSN (step 1). `--dsn` rejects password-bearing URLs.        |
| `failed to connect to postgres`               | `docker compose ps postgres`            | Start Postgres with `docker compose up -d postgres`.                             |
| `run factvault migrate` from `doctor`         | `./bin/factvault migrate`               | Run migrate with `FACTVAULT_MIGRATE_DATABASE_URL` exported (superuser DSN, not the API/worker DSN). |
| `JWT public key required`                     | API startup args                        | Pass `--jwt-public-key .local/public.pem` or set `FACTVAULT_JWT_PUBLIC_KEY`.     |
| `missing bearer token`                        | `curl` headers                          | Add `Authorization: Bearer $TOKEN`.                                              |
| `llm` shows `FAIL` or `WARN` in doctor       | `curl http://localhost:11434/v1/models` | Start your local LLM, or use `doctor --required-only` to ignore optional checks. |
| `embedder` shows `FAIL` in doctor            | `docker compose ps embedder`            | Start embedder; first model load can take time.                                   |
| `entity not found` from dossier API          | Tenant mismatch                         | Use a token whose `tenant_id` matches the tenant used by `example load`.          |
| Dossier body is empty (no facts)             | Pipeline not run yet                    | Run collect → archive → extract → corroborate → dossier workers in order.        |

## Notes for New Operators

### worker collect is a stub

`worker collect` currently inserts a single static placeholder URL
(`https://example.com/factvault-seed`). The source's historical reference to
[Issue #94](https://github.com/petersimmons1972/factvault/issues/94) is stale: that closed issue
shipped Docker Compose deployment, not collector configurability. The command is useful for
smoke-testing the pipeline but does not ingest real content. For real source ingestion, use:

- `worker rss` -- poll operator-defined RSS/Atom feeds from `config/feeds.yaml`
- `worker research <entity>` -- active acquisition via LLM-generated queries and web search

See [RSS Ingestion](guides/rss-ingestion.md) and [Active Acquisition](guides/active-acquisition.md).

### Embedder first-boot delay (~2GB model download)

The embedder sidecar downloads BAAI/bge-m3 (~2 GB) on first startup. Until the model finishes
loading, the `/healthz` endpoint returns 503 and `worker embed` will fail. Expect a wait of
several minutes on a fresh deployment. `factvault doctor` shows this as `WARN` or `FAIL` for the
embedder check. Once the model is loaded, the health check returns 200 and embedding population
works normally.

### migrate uses a different DSN than everything else

`factvault migrate` requires `CREATE EXTENSION` privileges (for pgvector and pg_trgm) and must
run as the Postgres superuser. Use `FACTVAULT_MIGRATE_DATABASE_URL` (the superuser DSN) for
migrations:

```bash
./bin/factvault migrate
```

All other workers, the API, and the MCP server connect as `app_user` via `FACTVAULT_DATABASE_URL`.
Both DSNs are pre-configured in `.env.example`. The `make setup` target handles this split
automatically.

### worker embed and worker research

After the core pipeline runs (collect/rss/research -> archive -> extract -> corroborate), run
these two new workers:

```bash
# Populate embedding columns (enables cosine story-seeding)
./bin/factvault worker embed \
  --tenant "$FACTVAULT_DEV_TENANT_ID"

# Actively research a new entity (generates sources via LLM + web search)
./bin/factvault worker research "Your Entity Name" \
  --tenant "$FACTVAULT_DEV_TENANT_ID" \
  --llm-base-url http://localhost:11434/v1 \
  --llm-model llama3.1:8b
```

See [Embedding Population](guides/embedding-population.md) and
[Active Acquisition](guides/active-acquisition.md) for full documentation.
