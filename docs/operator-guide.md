# Operator Guide

This runbook covers the current operational surface for factvault on the Go implementation. It assumes a single-machine deployment with Postgres and the bundled embedder service.

## Runtime Components

| Component | Purpose | Default command |
|---|---|---|
| Postgres + pgvector | Durable source, fact, vector, dossier, and audit storage | `docker compose up -d postgres` |
| Embedder | BGE-M3 embedding HTTP service | `docker compose up -d embedder` |
| API | JWT-protected REST retrieval surface | `factvault api --addr :8080 --jwt-public-key .local/public.pem` |
| MCP server | Stdio MCP tools backed by the same retrieval service | `factvault mcp --jwt-public-key .local/public.pem` |
| Workers | One-shot pipeline stages | `factvault worker <stage>` |
| Doctor | First-boot and health diagnostics | `factvault doctor` |

Issue #94 owns the final single-command Docker Compose deployment polish. Until it lands, prefer explicit host-run API and worker commands for predictable key and tenant setup.

## Configuration

Required for most commands:

```bash
export FACTVAULT_DATABASE_URL='postgres://factvault:factvault@localhost:5432/factvault?sslmode=disable'
export FACTVAULT_DEV_TENANT_ID='11111111-1111-1111-1111-111111111111'
```

Common optional settings:

| Variable | Default or example | Used by |
|---|---|---|
| `FACTVAULT_JWT_PUBLIC_KEY`   | `/run/secrets/factvault-jwt-public.pem`                   | API JWT verification                                      |
| `FACTVAULT_LLM_URL`          | `http://localhost:11434/v1`                               | Doctor and LLM extraction clients (fallback)              |
| `FACTVAULT_LLM_BASE_URL`     | unset — when set, takes precedence over `FACTVAULT_LLM_URL` | LLM extraction clients; use for non-default Ollama paths |
| `FACTVAULT_LLM_MODEL`        | `llama3.1:8b` (override for prod, e.g. `qwen3:32b`)      | LLM extraction clients                                    |
| `FACTVAULT_LLM_API_KEY`      | empty                                                     | Frontier or protected OpenAI-compatible endpoints         |
| `FACTVAULT_EMBEDDER_URL`     | `http://localhost:8080` or `http://localhost:8081` (host) | Doctor and embedding clients                              |
| `FACTVAULT_WAYBACK_URL`      | `https://web.archive.org`                                 | Doctor and archive checks                                 |

Use `.env.example` as the compose-oriented baseline. Use localhost hostnames when running `factvault` from the host and service names when running inside Compose or Kubernetes.

## First Boot Checklist

1. Copy `.env.example` to `.env`.
2. Start Postgres and embedder: `docker compose up -d postgres embedder`.
3. Build the binary: `go build -o bin/factvault ./cmd/factvault`.
4. Run migrations: `./bin/factvault migrate`.
5. Generate JWT keys with `./bin/factvault auth keys`.
6. Run `./bin/factvault doctor` and resolve every failing check that applies to your deployment.
7. Load an example with `./bin/factvault example load <name>`.
8. Run `./bin/factvault worker dossier`.
9. Start `./bin/factvault api --jwt-public-key .local/public.pem` and query `/entities/{id}/dossier` with a tenant-scoped bearer token.

For MCP clients that cannot set per-tool `authorization`, set `FACTVAULT_MCP_AUTH_TOKEN` (or `--auth-token`) so the server has a default bearer token.

The default binary uses the Postgres store and does not require CGO or SQLite
development headers. The experimental SQLite store is opt-in: build with
`CGO_ENABLED=1 go build -tags sqlite ./...` on a host with SQLite development
headers installed.

## Health Checks

`factvault doctor` runs seven checks:

| Check | What it proves | Common remediation |
|---|---|---|
| Postgres + pgvector | Database is reachable and vector extension is loaded | Start Postgres; verify DSN; run migrations. |
| Goose migrations | Schema version is current enough | Run `factvault migrate`. |
| RLS enforced | Tenant isolation policies hide cross-tenant rows | Verify migrations and app role setup. |
| LLM endpoint | OpenAI-compatible `/models` endpoint responds | Start local Ollama/olla or configure frontier endpoint. |
| Embedder health | BGE-M3 service responds on `/healthz` (200 when model loaded; 503 while loading) AND returns a real non-zero 1024-dim vector for a probe text (stub detection) | Start embedder; wait for model initialization (cold model download can take several minutes). |
| Wayback reachable | Archive endpoint can be contacted | Check outbound network or set alternate `FACTVAULT_WAYBACK_URL`. |
| Canary fact | Assembler can produce a bundle from a tenant-scoped entity | Check migrations and RLS context. |

The command exits non-zero if any check fails. For a minimal dossier smoke test, Postgres, migrations, RLS, and canary are the load-bearing checks.

Use `doctor --required-only` to exit 0 when only optional checks (LLM, embedder, Wayback) fail. Optional failures are shown as `WARN` instead of `FAIL` and their remedy lines are suppressed. Required checks (postgres, migrations, rls, canary) still cause a non-zero exit on failure.

## Worker Order

Run workers in this order for a tenant:

```bash
./bin/factvault worker collect --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker archive --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker extract --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker corroborate --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker verify --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker dossier --tenant "$FACTVAULT_DEV_TENANT_ID"
```

Use `--limit` to bound batch size and `--dsn` to override `FACTVAULT_DATABASE_URL`. `verify` also accepts `--age-days`.

Operational invariant: all domain data is tenant-scoped. The tenant in the worker command, the token used against the API, and the records in Postgres must match.

## API Operations

Unauthenticated endpoints:

```bash
curl -sS http://localhost:8080/healthz
curl -sS http://localhost:8080/readyz
```

Authenticated endpoints require an RS256 JWT with a `tenant_id` claim:

```bash
curl -sS -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/entities/$ENTITY_ID/dossier

curl -sS -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"query":"Example Entity","depth":1,"max_facts":50}' \
  http://localhost:8080/stories
```

## Backups

The default compose deployment stores Postgres data in the `pgdata` named volume. Do not delete or recreate that volume unless you intend to destroy the fact store.

Safe backup pattern:

```bash
docker compose exec -T postgres pg_dump -U factvault -d factvault > factvault-$(date +%Y%m%d-%H%M%S).sql
```

Restore into a fresh database only after verifying the target DSN and volume. Avoid `docker compose down -v`, `docker volume rm`, and database `DROP` commands unless data loss is explicitly intended.

## Upgrades

1. Read release notes and migrations.
2. Back up Postgres.
3. Pull or build the new image/binary.
4. Run `factvault migrate` once.
5. Restart API/workers.
6. Run `factvault doctor`.
7. Query a known dossier and compare shape/counts against pre-upgrade expectations.

## Troubleshooting Matrix

| Failure | Likely cause | Action |
|---|---|---|
| API starts then authenticated calls return `401` | Wrong keypair or expired token | Regenerate keys and token; verify with `factvault auth verify`. |
| `readyz` returns false | API has no DB pool | Check `FACTVAULT_DATABASE_URL` and API startup logs. |
| Dossier is empty | Entity exists but no statements are linked yet | Run extraction/corroboration or inspect example seed data expectations. |
| Dossier returns 404 | Entity ID absent for token tenant | Query `entities` for the same tenant used in the token. |
| Worker exits with tenant error | Missing `--tenant` | Pass a UUID tenant ID explicitly. |
| Doctor RLS check fails | Migrations or role setup incomplete | Re-run migrations; inspect `app_user` role and RLS policies. |
| Embedder is slow on first request | Model cold start | Wait for container health and keep the service warm. |
| LLM costs appear unexpectedly | Frontier endpoint configured | See [Frontier Models](guides/frontier-models.md) and remove remote API env vars for local-only mode. |

## Security Notes

- Keep private JWT keys out of git; use `.local/`, Docker secrets, Kubernetes secrets, or a real secret manager.
- Default local operation should not send fact content to hosted LLMs or hosted embedding providers.
- RLS is part of the safety model; do not connect production application traffic as a Postgres superuser.
- Source existence is a security property: preserve `raw_text`, `content_hash`, `archive_url`, and `statement_sources` offsets during migration and backup flows.
