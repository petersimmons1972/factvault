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

## Kubernetes Credentials

The `app_user` Postgres login role is created by the database init layer, **not** the schema migration.
The migration only places a `NOLOGIN` placeholder so that `GRANT` statements succeed on a cold cluster;
in practice the init layer always runs first and the migration's `IF NOT EXISTS` guard is a no-op.

### Local compose path

`deploy/initdb/01-create-app-user.sh` is mounted into `docker-entrypoint-initdb.d/` and executes
once on first Postgres initialisation (empty `pgdata`). It reads `POSTGRES_APP_USER_PASSWORD` from the
compose environment. The default value (`dev_only_local_password`) is **only** safe for local
development. Override it for any other environment.

The compose DSN connects as `app_user` (matching production), so the script's `GRANT` set is
exercised end-to-end on every dev session. If you previously ran compose with the superuser DSN
(`factvault:factvault`), follow the rotation steps in **Migrating an existing deployment** below
to converge.

### Production / Kubernetes path

1. **Store the DSN in Infisical** (project: `factvault`):
   - key: `FACTVAULT_DATABASE_URL`
   - value: `postgres://app_user:<password>@<host>:5432/factvault?sslmode=require`

2. **Sync to a Kubernetes Secret** named `factvault-db-credentials` via the Infisical operator or
   ExternalSecrets. The template in `deploy/k8s/examples/secret.example.yaml` shows the expected
   key structure. For ad-hoc work, copy it to a working file:
   ```bash
   cp deploy/k8s/examples/secret.example.yaml deploy/k8s/secret.yaml
   # edit deploy/k8s/secret.yaml: replace <REPLACE_WITH_INFISICAL_MANAGED_VALUE>
   kubectl apply -f deploy/k8s/secret.yaml
   ```
   The working copy `deploy/k8s/secret.yaml` is gitignored. The example lives under `examples/`
   so that `kubectl apply -f deploy/k8s/` (non-recursive) cannot accidentally deploy a
   non-functional Secret.

3. **Create `app_user` on first Postgres init.** The role must exist before migrations run. Options:
   - Mount an init script (equivalent to `deploy/initdb/01-create-app-user.sh`) via a ConfigMap into
     the Postgres pod's `docker-entrypoint-initdb.d/`.
   - Run a pre-migration init container that executes:
     ```sql
     CREATE ROLE app_user WITH LOGIN PASSWORD '<password from secret>';
     ```
   - Use your managed Postgres provider's user management API (e.g. Cloud SQL IAM auth, RDS Users).

4. **All Deployments, Jobs, and CronJobs** reference both `factvault-config` (non-secret config) and
   `factvault-db-credentials` (DSN) via `envFrom`. Do not add `FACTVAULT_DATABASE_URL` back to the
   ConfigMap.

Credentials MUST NOT appear in `configmap.yaml` — they are audited by `TestK8sConfigMapContainsNoCredentials`.

### Migrating an existing deployment

If your cluster was provisioned before this change, `app_user` already exists with the legacy
hardcoded password (`changeme_in_production`). Pulling this PR and redeploying alone will NOT
rotate the credential — `CREATE ROLE ... IF NOT EXISTS` is a no-op when the role is present.

**Rotate immediately**, in this order:

1. **Decide on the new password** and store it where the new code expects it:
   - **Compose**: set `POSTGRES_APP_USER_PASSWORD` in `.env` (override the default).
   - **K8s**: update the `FACTVAULT_DATABASE_URL` value in Infisical (or whatever feeds
     `factvault-db-credentials`).

2. **Rotate the Postgres role.** Connect to the database as a superuser and run:
   ```sql
   ALTER ROLE app_user WITH LOGIN PASSWORD '<new password>';
   ```
   The Postgres server uses scram-sha-256 hashing, so the new password is salted and hashed
   server-side — `pg_authid.rolpassword` no longer holds anything attacker-useful once the
   ALTER completes.

3. **Roll the application.**
   - **Compose**: `docker compose up -d --force-recreate factvault-migrate factvault-api factvault-workers factvault-mcp`.
   - **K8s**: trigger a rolling restart so pods pick up the new Secret:
     ```bash
     kubectl rollout restart deployment/factvault-api -n factvault
     kubectl rollout restart deployment/factvault-mcp -n factvault
     # CronJobs pick up the new Secret on their next scheduled run
     ```

4. **Verify.** `factvault doctor` should succeed end-to-end. A failed `postgres` check after
   rotation means the application is still using the old password — re-check that the Secret
   contains the new value and the pods restarted.

**Do NOT** drop and recreate `app_user`. The `GRANT`s from the migration are attached to the
role; dropping it requires re-running the GRANTs, which the migration does not idempotently
re-apply on schema-current databases.

## Security Notes

- Keep private JWT keys out of git; use `.local/`, Docker secrets, Kubernetes secrets, or a real secret manager.
- Default local operation should not send fact content to hosted LLMs or hosted embedding providers.
- RLS is part of the safety model; do not connect production application traffic as a Postgres superuser.
- Source existence is a security property: preserve `raw_text`, `content_hash`, `archive_url`, and `statement_sources` offsets during migration and backup flows.
- `POSTGRES_APP_USER_PASSWORD=dev_only_local_password` is for local development only. Treat any
  environment that uses this default as compromised and rotate the credential immediately.
