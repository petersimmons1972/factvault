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

For host-run commands, override the in-container Postgres hostname:

```bash
export FACTVAULT_DATABASE_URL='postgres://factvault:factvault@localhost:5432/factvault?sslmode=disable'
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
./bin/factvault migrate
./bin/factvault init \
  --dsn "$FACTVAULT_DATABASE_URL" \
  --tenant "$FACTVAULT_DEV_TENANT_ID"
```

`init` generates JWT keys in `.local/`, runs the doctor health checks, and loads the default example. Keys are written only if they do not exist yet. The awk key-splitting step from older guides is no longer required — `init` writes `private.pem` and `public.pem` directly.

### 4a. Run Readiness Checks

The `init` command runs doctor automatically. You can also invoke it directly:

```bash
./bin/factvault doctor \
  --dsn "$FACTVAULT_DATABASE_URL" \
  --embedder-url http://localhost:8081 \
  --llm-url http://localhost:11434/v1
```

If you do not have a local LLM running, use `--required-only` to exit 0 when only LLM/embedder/Wayback fail:

```bash
./bin/factvault doctor --dsn "$FACTVAULT_DATABASE_URL" --required-only
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
  --dsn "$FACTVAULT_DATABASE_URL" \
  --tenant "$FACTVAULT_DEV_TENANT_ID"
```

Precompute dossiers for that tenant:

```bash
./bin/factvault worker dossier \
  --dsn "$FACTVAULT_DATABASE_URL" \
  --tenant "$FACTVAULT_DEV_TENANT_ID" \
  --limit 10
```

The dossier is empty until the full collect/archive/extract/corroborate pipeline runs. The retrieval path is live and tenant-scoped immediately.

---

## 5. Query the Dossier Through the API

Keys are in `.local/` (written by `init` or `make setup`). Issue a tenant-scoped token:

```bash
TOKEN=$(./bin/factvault auth token \
  --private-key .local/private.pem \
  --tenant "$FACTVAULT_DEV_TENANT_ID" \
  --sub local-dev)
```

Start the API:

```bash
./bin/factvault api \
  --dsn "$FACTVAULT_DATABASE_URL" \
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

After the initial dossier works, run the source pipeline against a tenant:

```bash
./bin/factvault worker collect --dsn "$FACTVAULT_DATABASE_URL" --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker archive --dsn "$FACTVAULT_DATABASE_URL" --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker extract --dsn "$FACTVAULT_DATABASE_URL" --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker corroborate --dsn "$FACTVAULT_DATABASE_URL" --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker dossier --dsn "$FACTVAULT_DATABASE_URL" --tenant "$FACTVAULT_DEV_TENANT_ID"
```

The dossier will remain empty until the full pipeline stages complete.

## Troubleshooting

| Symptom | Check | Fix |
|---|---|---|
| `database DSN required`                       | `echo "$FACTVAULT_DATABASE_URL"`        | Export the localhost DSN or pass `--dsn`.                                        |
| `failed to connect to postgres`               | `docker compose ps postgres`            | Start Postgres with `docker compose up -d postgres`.                             |
| `run factvault migrate` from `doctor`         | `./bin/factvault migrate`               | Run migrations against the same DSN used by the API and workers.                 |
| `JWT public key required`                     | API startup args                        | Pass `--jwt-public-key .local/public.pem` or set `FACTVAULT_JWT_PUBLIC_KEY`.     |
| `missing bearer token`                        | `curl` headers                          | Add `Authorization: Bearer $TOKEN`.                                              |
| `llm` shows `FAIL` or `WARN` in doctor       | `curl http://localhost:11434/v1/models` | Start your local LLM, or use `doctor --required-only` to ignore optional checks. |
| `embedder` shows `FAIL` in doctor            | `docker compose ps embedder`            | Start embedder; first model load can take time.                                   |
| `entity not found` from dossier API          | Tenant mismatch                         | Use a token whose `tenant_id` matches the tenant used by `example load`.          |
| Dossier body is empty (no facts)             | Pipeline not run yet                    | Run collect → archive → extract → corroborate → dossier workers in order.        |
