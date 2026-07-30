# AGENTS.md — Conventions for AI coding agents working on factvault

This file is read by Codex CLI and other AI coding agents at session start.
Follow these conventions for every task in this repo.

## Project context

factvault is a self-hostable research database where every fact is grounded in a
verifiable, durably-archived source. The codebase is in transition from Python
to Go (see `docs/superpowers/specs/2026-05-22-go-transition.md`).

**Active language:** Go 1.22+ for workers, API, MCP server, and CLI.
**Python only:** the embedder microservice at `services/embedder/` (wraps
sentence-transformers BGE-M3; called from Go via HTTP).

## Locked stack (do NOT re-litigate)

- **Web framework:** `github.com/go-chi/chi/v5`
- **DB driver:** `github.com/jackc/pgx/v5` + `github.com/pgvector/pgvector-go`
- **Typed queries:** `sqlc` (codegen from `internal/db/queries/*.sql`)
- **Migrations:** `github.com/pressly/goose/v3`, SQL files under `migrations/`
- **CLI framework:** `github.com/spf13/cobra`
- **MCP server:** `github.com/modelcontextprotocol/go-sdk`
- **HTML→text:** `github.com/go-shiori/go-readability`
- **HTTP client:** stdlib `net/http`
- **Logging:** `log/slog` from stdlib
- **Testing:** stdlib `testing` + `github.com/ory/dockertest/v3`

## Commit message convention — MANDATORY

Every commit completing work on a GitHub Issue MUST end with `Closes #<N>` on
the last line (where N is the issue number provided in your prompt). This
triggers GitHub's auto-close mechanism on merge. Format:

```
feat(workers): archive worker (Stage 2) — collected -> archived

Implements the polling loop, content_hash computation, Wayback submission,
and status transitions per Plan 2 §4.2.

Closes #42
```

If your task doesn't correspond to a GitHub Issue, omit the `Closes` line.

## Codex Execution Loop

You are the execution engine. The coordinator (Claude) opens GitHub Issues describing work; you pick them up and ship PRs that close them.

**Queue contract:**
- Watch for open issues labeled `agent/codex` in this repo
- Pick one at a time. Do not work multiple issues in parallel.
- Read the issue body in full before starting. If the brief is ambiguous, comment on the issue with your question and stop — do not guess.
- Open a PR that closes the issue. The PR's last commit MUST end with `Closes #<N>` on its own line. The PR body should also reference `Closes #<N>` for auto-close on merge.
- After your PR is merged (or closed), pick the next issue.

## TDD-first protocol

- Before writing implementation code for any issue, construct TDD scenarios first.
- Translate the issue's acceptance criteria into failing test files (red).
- Confirm tests fail for the right reason (assertion failure, not compile error or missing file).
- Write the minimum implementation to make tests pass (green).
- Refactor with tests still green.
- Commits: prefer separate commits for test (first) and implementation (second), or document the red->green progression in the PR description.
- A PR opened without a visible TDD trail (failing-test commit preceding passing implementation, or PR description walking through red->green) will be sent back for rework.

**What the coordinator guarantees in every issue:**
- Exact file paths to create or modify
- Acceptance criteria (tests to write, behaviors to verify)
- Pointers to relevant spec/plan sections
- Any non-obvious dependencies or constraints

**What you do NOT do:**
- Pick up issues without the `agent/codex` label
- Push directly to `main` (use a feature branch + PR)
- Use `--no-verify` or bypass any safety check
- Edit `AGENTS.md`, `CLAUDE.md`, or anything under `.github/` unless the issue explicitly requests it

## Required reading

Before claiming any issue:
1. `docs/codex/onboarding.md` — full execution loop protocol
2. `docs/codex/toolchain.md` — mandatory Go toolchain + invocation order

## Protected files

The following files are part of the coordinator-executor contract. Do
not modify them unless the issue explicitly authorizes it:
- `.golangci.yml`
- `AGENTS.md`
- `CLAUDE.md` (if present)
- `docs/codex/onboarding.md`
- `.github/**`

## Test-before-commit

Before any commit, run:

```bash
go test ./... -count=1
```

If any test fails, DO NOT commit. Fix the failure first. If you cannot fix
it within ~10 minutes, STOP and report BLOCKED with the verbatim failure
output. Do not commit a known-failing test as TODO.

For the Python embedder microservice:

```bash
cd services/embedder
pytest -v
```

## Project layout

```
factvault/
├── cmd/factvault/main.go                # Cobra root
├── internal/
│   ├── api/                             # chi HTTP server
│   ├── workers/                         # archive, verify, extract, corroborate, dossier
│   ├── collectors/                      # rss, http, sitemap, searxng, wayback_cdx, upload
│   ├── archiving/                       # wayback, htmlextract, hash
│   ├── extractors/                      # deterministic + llm
│   ├── vocabulary/                      # property resolver
│   ├── assembler/                       # bundle building
│   ├── mcp/                             # MCP server tools
│   ├── doctor/                          # first-boot health checks
│   ├── examples/                        # example domain loader
│   ├── auth/                            # JWT
│   ├── db/                              # pgx + tenant_context + sqlc queries
│   ├── config/                          # YAML loader
│   └── embedclient/                     # HTTP client for the embedder microservice
├── migrations/                          # goose SQL files
├── services/embedder/                   # Python sentence-transformers microservice
├── deploy/{docker,k8s}/
└── docs/                                # plans, specs, concept docs
```

## Authoritative plan documents

Implementation plans live at `docs/superpowers/plans/`. When a task references
a Plan N (e.g., "Plan 3 T13"), find the corresponding plan file and read the
relevant task section before implementing. Plans are the source of truth for
file paths, function signatures, and test expectations.

Codex handoff docs (one per plan after Plan 2) are archived at
`docs/superpowers/plans/archive/2026-05-22-codex-handoff-plan<N>.md`.

## RLS and tenant_context

The database uses Postgres Row-Level Security on every domain table. All code
that reads or writes domain data MUST run inside a tenant context:

```go
ctx, err := db.TenantContext(ctx, tenantID)
if err != nil { return err }
// ... queries inside ctx are filtered to this tenant
```

The Go GUC name is `app.tenant_id` (NOT `app.current_tenant_id`). Postgres
superusers bypass RLS — production code connects as a non-superuser app role.

## Known-bad patterns

- DO NOT call `Commit()` on a connection inside a `TenantContext` block. The
  `SET LOCAL` GUC is transaction-scoped; committing destroys it and subsequent
  queries silently filter to zero rows.
- DO NOT use `database/sql` directly. Use `pgx` types and methods.
- DO NOT import `github.com/jmoiron/sqlx` or any other DB abstraction layer.
- DO NOT add a new dependency without a comment explaining why no stdlib or
  existing-dep alternative works.

## Branch + PR conventions

- Branch names: `feat/<short-description>` or `fix/<short-description>` or
  `chore/<short-description>`.
- Work in the worktree assigned by the coordinator (typically
  `~/projects/factvault-plan<N>/` for plan-bound work).
- DO NOT push to `main` directly. The coordinator pushes via the PR-mediated
  path (feature branch + `gh pr merge`).
- DO NOT include "AI-generated" trailers in commit messages. Use the
  `Closes #N` footer only.

## Stuck or ambiguous?

If the task description is genuinely ambiguous, STOP and report BLOCKED with
a specific question. Do not improvise architectural decisions. The coordinator
will clarify or escalate to the founder.
