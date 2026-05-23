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

## 2. Start the Local Services

Start Postgres first. The embedder can also be started if you want the `doctor` embedder check to pass, but the worked dossier example below only needs Postgres.

```bash
docker compose up -d postgres embedder
```

Current note: issue #94 is tracking the final Tier 1 single-command compose polish. Until that lands, run the API from the host in step 6 so you can provide the JWT public key directly.

## 3. Build and Migrate

```bash
go build -o bin/factvault ./cmd/factvault
./bin/factvault migrate
```

The migration installs the Postgres schema, pgvector extension, RLS policies, and indexes.

## 4. Run Readiness Checks

```bash
./bin/factvault doctor \
  --dsn "$FACTVAULT_DATABASE_URL" \
  --embedder-url http://localhost:8081 \
  --llm-url http://localhost:11434/v1
```

Expected result when Postgres, embedder, Wayback, and the local LLM are reachable: all seven checks return `OK`.

If you are only validating the dossier path and do not have a local LLM running yet, the LLM check can fail while the following example still works. The API and dossier worker do not need an LLM for preloaded example entities.

## 5. Load an Example and Assemble a Dossier

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

Expected output is a successful exit. The worker writes one `dossiers` row per stale or missing tenant entity dossier.

## 6. Query the Dossier Through the API

Generate a development RSA keypair:

```bash
mkdir -p .local
./bin/factvault auth keys > .local/dev-keys.pem
awk 'BEGIN{pub=0} /BEGIN PUBLIC KEY/{pub=1} pub{print}' .local/dev-keys.pem > .local/public.pem
awk 'BEGIN{priv=0} /BEGIN RSA PRIVATE KEY/{priv=1} priv{print} /END RSA PRIVATE KEY/{exit}' .local/dev-keys.pem > .local/private.pem
```

Issue a tenant-scoped token:

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

A successful response contains an entity bundle for the loaded example entity. In the current example fixtures, the bundle may have no statement facts until you run the full collect/archive/extract/corroborate pipeline, but the dossier retrieval path is live and tenant-scoped.

## Full Pipeline Next Steps

After the initial dossier works, run the source pipeline against a tenant:

```bash
./bin/factvault worker collect --dsn "$FACTVAULT_DATABASE_URL" --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker archive --dsn "$FACTVAULT_DATABASE_URL" --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker extract --dsn "$FACTVAULT_DATABASE_URL" --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker corroborate --dsn "$FACTVAULT_DATABASE_URL" --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker dossier --dsn "$FACTVAULT_DATABASE_URL" --tenant "$FACTVAULT_DEV_TENANT_ID"
```

The extract and corroborate CLI entries are present, but the current compose-first operator path is still being tightened under #94. Treat this section as the next operational path rather than the shortest first-run path.

## Troubleshooting

| Symptom | Check | Fix |
|---|---|---|
| `database DSN required` | `echo "$FACTVAULT_DATABASE_URL"` | Export the localhost DSN shown in step 1 or pass `--dsn`. |
| `failed to connect to postgres` | `docker compose ps postgres` | Start Postgres with `docker compose up -d postgres`. |
| `run factvault migrate` from `doctor` | `./bin/factvault migrate` | Run migrations against the same DSN used by the API and workers. |
| `JWT public key required` | API startup args | Pass `--jwt-public-key .local/public.pem` or set `FACTVAULT_JWT_PUBLIC_KEY`. |
| `missing bearer token` | `curl` headers | Add `Authorization: Bearer $TOKEN`. |
| `LLM endpoint` fails in `doctor` | `curl http://localhost:11434/v1/models` | Start your local OpenAI-compatible LLM, or skip full doctor while testing the preloaded dossier path. |
| `embedder health` fails | `docker compose ps embedder` | Start the embedder with `docker compose up -d embedder`; first model load can take time. |
| `entity not found` from dossier API | Tenant mismatch | Use a token whose `tenant_id` matches the tenant used by `example load`. |
