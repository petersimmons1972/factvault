# Factvault Scaffold Convention Contract

**Status:** LOCKED — RFC #228 consensus (Hermes + Codex + Grok). Execute under founder standing auth.
**Date locked:** 2026-06-26
**Authority:** petersimmons1972/factvault · OOBE epic #199

This document is the authoritative scaffold convention contract. Every pull request that touches
configuration, flags, environment variables, health endpoints, or the init/migrate flow MUST conform
to the invariants below. Conventions C1 and C10 specify that **this contract is also machine-checked**:
contract tests assert every row in the [registry](#registry) is read by code and every flag maps to
exactly one row.

---

## C1 — Config Precedence: flag > env > default, universally

**Decision:** Every config concept resolves `flag > env > default` in every command via a shared
**typed resolver** — not a naive `firstNonEmpty` string helper.

**Rationale:** `firstNonEmpty` silently treats the empty string as "not set," which is wrong for
string values the user explicitly set to `""`, and breaks completely for non-string types. A typed
resolver uses `pflag.Changed` to detect explicit flag setting and `os.LookupEnv` to distinguish
unset from empty. Required configs fail explicitly with a clear error; optional ones fall back to
their documented default.

**Invariants (enforced by contract tests, C10):**

- Every flag that maps to a config concept has exactly one documented env var in the [registry](#registry).
- Every documented env var is read by code (`os.LookupEnv` or `os.Getenv`).
- No dead vars, no command-specific divergence from the precedence order.
- Required configs that are neither flagged nor set via env produce a non-zero exit and a human-readable
  error — never a silent zero or a panic.

**Resolver package:** `internal/config` — provides typed resolvers for `string`, `int`, `bool`,
`time.Duration`, and `*url.URL`. Per-command wiring calls these helpers; the resolver owns the
precedence logic.

---

## C2 — One Name Per Concept: `FACTVAULT_<DOMAIN>_<THING>`

**Decision:** `FACTVAULT_LLM_BASE_URL` is the canonical env var for the LLM endpoint.
`FACTVAULT_LLM_URL` is a **deprecated alias** kept for one release (read silently, warn on stderr
when used), then removed. All commands — including `doctor` — read `FACTVAULT_LLM_BASE_URL` first
via the shared resolver. The deprecated alias is the resolver's fallback, not a primary read.

**Rationale:** The scaffold had two names (`LLM_URL`, `LLM_BASE_URL`) for the same concept, with
`doctor` reading only the old name and workers preferring the new one. This produced invisible
divergence at the most user-visible command (`doctor`). "One name per concept" is a 12-factor
invariant. The `_BASE_URL` suffix follows the OpenAI SDK convention that operator documentation
already uses.

**Migration:** `.env.example` documents `FACTVAULT_LLM_BASE_URL` as the live var;
`FACTVAULT_LLM_URL` appears with a deprecation comment only.

---

## C3 — Uniform JWT Flag Names: `--jwt-public-key` / `--jwt-private-key` Everywhere

**Decision:** All commands use `--jwt-public-key` / `--jwt-private-key`. The `auth` command's old
`--public-key` / `--private-key` flags are kept as **hidden aliases** that emit a deprecation
warning on use. `init.go:107`'s next-steps output is fixed to print the real, working flag names.

**Rationale:** A user completing the OOBE flow copies the `init` command's "next steps" output into
their shell. If those next steps reference a flag that doesn't exist on the `auth token` subcommand,
the first post-init command fails. This is an OOBE-breaking copy-paste break at the seam of the
two-step flow (`init` → `auth token`). Standardizing the flag name across all commands eliminates
the failure class.

---

## C4 — Tenant Resolution

**Decision:**

| Command class       | Resolution order                                              | Behavior when neither set |
|---------------------|--------------------------------------------------------------|---------------------------|
| OOBE (`init`, `example`) | `--tenant` > `FACTVAULT_DEV_TENANT_ID` > `11111111-1111-1111-1111-111111111111` | Uses literal dev UUID; no warn |
| Production (`worker`, `brief`, `auth token`) | `--tenant` > `FACTVAULT_DEV_TENANT_ID` | **ERROR**: missing required `--tenant` |
| Production + `FACTVAULT_DEV_TENANT_ID` set | Uses env value | Warns loudly on stderr: "using dev tenant from env" |

**Rationale:** All three fleet AIs converged on rejecting silent defaults for production commands.
Silent tenant fallback in `worker`/`brief`/`auth` fights 12-factor discipline and hides RLS/multi-
tenant mistakes in ops. The dev UUID is intentionally cartoonish to make accidental production use
obvious. OOBE commands exist precisely to work with no config — they get the silent default. Worker
and brief commands are operator-facing; they must fail fast on missing tenancy context.

---

## C5 — Every Documented Env Var Is Live

**Decision:** The [registry](#registry) is the single source of truth. Every row's env var is read
by the binary via `os.Getenv` or `os.LookupEnv` at the Go level — not interpolated by compose only.

**Wired (currently missing, must be added):**

- `FACTVAULT_API_ADDR` → `--addr` in `api` command
- `FACTVAULT_FEEDS_PATH` → `--feeds` in `worker` command
- `FACTVAULT_WORKER_LIMIT` → `--limit` in `worker` command (real Go fallback, not compose-only)
- `FACTVAULT_VERIFY_AGE_DAYS` → `--age-days` in `worker` command (real Go fallback)

**Pruned (removed from `.env.example`, compose, and docs):**

- `FACTVAULT_MCP_TENANT_ID` — never read by code; remove without deprecation period (greenfield)
- `FACTVAULT_MCP_TRANSPORT` — never read by code; remove without deprecation period

**Rationale:** Env vars that appear in `.env.example` but are silently ignored by the binary create
a false sense of configuration. Operators set them, restart the service, and see no effect. This is
a class of ops bug that the contract test (C10) prevents mechanically.

---

## C6 — Health Endpoints: `/healthz` (liveness) + `/readyz` (readiness), Canonical

**Decision:**

- `/healthz` is the canonical liveness endpoint on all HTTP services (API, embedder).
- `/readyz` is the canonical readiness endpoint; returns **HTTP 503** when not ready (not 200 with a
  `ready: false` body — clients cannot distinguish the latter from a ready service without parsing).
- The `/health` alias is **removed now** — this is a greenfield scaffold with no external consumers.
  Removing it today avoids a deprecation period with zero benefit.
- Compose `healthcheck`, `doctor`, operator-guide, and OpenAPI spec all probe `/healthz` only.
- The embedder's `/health` endpoint is removed or aliased as a redirect to `/healthz` (implementer
  chooses the cheaper option; both are acceptable for a greenfield).

**Rationale:** The scaffold already used `/healthz` in the Go API and operator-guide; compose and
doctor probed `/health`. This divergence meant `doctor` passed while the real probe path (`/healthz`)
was never exercised. Codex's needs-input response confirmed: `/readyz` returning 503 is correct k8s
norm; "200 with ready:false" is a broken pattern that breaks `kubectl rollout status`.

---

## C7 — Port Map: No Collisions

**Decision:**

| Service    | Canonical port | Scope         | Notes                                    |
|------------|---------------|---------------|------------------------------------------|
| API        | `:8080`       | host + network | Go binary default; compose host-exposed  |
| Embedder   | `:8081`       | host-exposed  | compose host port; in-network service may use `:8080` internally |
| Ollama     | `:11434`      | host + network |                                          |
| Postgres   | `:5432`       | network only  | not host-exposed in compose by default   |

`doctor`'s embedder localhost default changes from `:8080` to `:8081`. The old default collided with
the API and caused every clean `doctor` run to probe the wrong service.

**Rationale:** Operator confusion from a port collision at the `doctor` step is an OOBE failure.
Host-exposed ports and in-network service ports are separated in the table above; compose and k8s
service definitions must respect this split.

---

## C8 — `init` Is One-Shot OOBE

**Decision:**

- `init` runs `migrate` first (idempotent DDL), then keygen + `doctor` + `example`.
- A single `factvault init` takes a fresh clone to a working first query.
- `--skip-migrate` flag escapes the auto-migrate for operators who manage migrations separately.
- `serve` / `worker` / `mcp` **NEVER** auto-migrate on startup — only `init` does.
- The separate `migrate` command is preserved for CI and ops workflows.
- Key directory is unified under `FACTVAULT_AUTH_DIR` (default `.local`) in both binary and compose.

**Rationale:** The prior flow required two commands before anything worked (`migrate` then `init`),
but `init`'s doc implied one. Idempotent migrate as the first `init` step makes the promise real
without risk — the scaffold has no data, so a repeated DDL run is always safe. Auto-migrate in
service commands is an ops hazard; it stays out of serve/worker/mcp.

---

## C9 — Secret `*_FILE` Path Variants

**Decision:** Every secret env var has a companion `*_FILE` variant that reads the secret from a
file path. This enables k8s `secretKeyRef` and Docker secrets without exposing values in the
process environment.

| Secret var                    | File variant                         |
|-------------------------------|--------------------------------------|
| `FACTVAULT_JWT_PUBLIC_KEY`    | `FACTVAULT_JWT_PUBLIC_KEY_FILE`      |
| `FACTVAULT_JWT_PRIVATE_KEY`   | `FACTVAULT_JWT_PRIVATE_KEY_FILE`     |
| `FACTVAULT_DATABASE_URL`      | `FACTVAULT_DATABASE_URL_FILE`        |
| `FACTVAULT_LLM_API_KEY`       | `FACTVAULT_LLM_API_KEY_FILE`         |
| `FACTVAULT_MCP_AUTH_TOKEN`    | `FACTVAULT_MCP_AUTH_TOKEN_FILE`      |

Resolution order for `*_FILE` variants: the typed resolver checks `*_FILE` first (reads and trims
the file contents), then falls back to the direct env var. The `_FILE` variant is **not** listed in
the registry as a separate row; it is a property of the resolver for secret-class vars.

---

## C10 — Registry + Contract Tests

**Decision:** The [registry table](#registry) below is the single machine-checkable source of truth.
Contract tests (PG-independent, pure-Go unit tests) assert:

1. Every row's env var is read by code (grep `os.Getenv` or `os.LookupEnv` for each name).
2. Every flag maps to exactly one registry row (no unregistered flags; no registry rows without a flag).
3. No dead vars: every row's env var appears in at least one `.go` source file.

**Rationale:** Prose conventions rot. Tests don't. The contract test package (`internal/config/contract_test.go`)
is the mechanical enforcement of C1 and C5. It runs without Postgres or network access and is part
of the standard `go test ./...` suite.

---

## Registry

This table is the authoritative map of every documented config concept. Contract tests assert it
against the codebase on every PR. "Deprecated alias" rows exist only during a one-release transition;
they are removed on the next minor bump.

| concept            | flag                        | env var                           | default                                    | alias (deprecated)      | notes                                      |
|--------------------|-----------------------------|-----------------------------------|--------------------------------------------|-------------------------|--------------------------------------------|
| database URL       | `--dsn`                     | `FACTVAULT_DATABASE_URL`          | *(required)*                               | `DATABASE_URL`          | superuser DSN for migrate; app DSN for serve/worker |
| migrate DSN        | `--dsn` (migrate cmd)       | `FACTVAULT_MIGRATE_DATABASE_URL`  | falls back to `FACTVAULT_DATABASE_URL`     | —                       | superuser DSN for `CREATE EXTENSION`       |
| LLM base URL       | `--llm-base-url`            | `FACTVAULT_LLM_BASE_URL`          | `http://localhost:11434/v1`                | `FACTVAULT_LLM_URL`     | alias readable for 1 release, then removed |
| LLM model          | `--llm-model`               | `FACTVAULT_LLM_MODEL`             | `llama3.1:8b`                              | —                       |                                            |
| LLM API key        | `--llm-api-key`             | `FACTVAULT_LLM_API_KEY`           | `""` (optional)                            | —                       | `_FILE` variant supported                 |
| embedder URL       | `--embedder-url`            | `FACTVAULT_EMBEDDER_URL`          | `http://localhost:8081`                    | —                       |                                            |
| Wayback URL        | `--wayback-url`             | `FACTVAULT_WAYBACK_URL`           | `https://web.archive.org`                  | —                       |                                            |
| JWT public key     | `--jwt-public-key`          | `FACTVAULT_JWT_PUBLIC_KEY`        | *(required)*                               | `--public-key` (auth)   | path to PEM file; `_FILE` variant = read from path |
| JWT private key    | `--jwt-private-key`         | `FACTVAULT_JWT_PRIVATE_KEY`       | *(required for init/auth)*                 | `--private-key` (auth)  | path to PEM file; `_FILE` variant = read from path |
| MCP auth token     | *(no flag; header only)*    | `FACTVAULT_MCP_AUTH_TOKEN`        | *(required for MCP)*                       | —                       | `_FILE` variant supported                 |
| dev tenant ID      | `--tenant`                  | `FACTVAULT_DEV_TENANT_ID`         | `11111111-1111-1111-1111-111111111111` (OOBE only) | —              | C4: production cmds error if neither set  |
| API listen addr    | `--addr`                    | `FACTVAULT_API_ADDR`              | `:8080`                                    | —                       |                                            |
| feeds config path  | `--feeds`                   | `FACTVAULT_FEEDS_PATH`            | `config/feeds.yaml`                        | —                       |                                            |
| worker limit       | `--limit`                   | `FACTVAULT_WORKER_LIMIT`          | `100`                                      | —                       |                                            |
| verify age (days)  | `--age-days`                | `FACTVAULT_VERIFY_AGE_DAYS`       | `7`                                        | —                       |                                            |
| auth key directory | *(no flag; init resolves)*  | `FACTVAULT_AUTH_DIR`              | `.local`                                   | —                       | unified between binary and compose        |
| confirm cost       | `--confirm-cost`            | `FACTVAULT_CONFIRM_COST`          | `false`                                    | —                       | guards frontier-model extraction batches  |

**Pruned (removed — never read by code; no deprecation period on a greenfield):**

| var                        | reason                     |
|----------------------------|----------------------------|
| `FACTVAULT_MCP_TENANT_ID`  | never read by any Go file  |
| `FACTVAULT_MCP_TRANSPORT`  | never read by any Go file  |

---

## Follow-On Work (non-blocking, tracked separately)

The following items emerged from fleet consensus but are **not** part of this contract. They are
tracked as individual issues:

- Log level / log format env vars (`FACTVAULT_LOG_LEVEL`, `FACTVAULT_LOG_FORMAT`)
- Startup fail-fast validation (check all required vars before opening DB connections)
- Explicit deprecation policy (semver-keyed, one-minor-release window)
- Dev-mode detection (e.g. `FACTVAULT_DEV=true` shorthand)
- DB connection-string naming (`DATABASE_URL` vs `DSN` consistency)
- Flag inheritance model (persistent vs local flags per subcommand tree)
- Compose-vs-k8s liveness/readiness probe mapping in operator-guide
