# Plan 4 Codex Handoff

**Status:** active
**Plan:** [Bundle and Retrieval Implementation Plan](./2026-05-22-bundle-and-retrieval.md)
**Worktree:** `~/projects/factvault-plan4/` on branch `feat/bundle-and-retrieval`
**Repo:** https://github.com/petersimmons1972/factvault

---

## 1. Overview

Plan 4 builds the retrieval surface that downstream LLMs talk to: a shared `BundleAssembler.assemble()` function produces canonical JSON bundles for both pre-computed per-entity dossiers and on-demand graph-expanded stories; a FastAPI REST API and an MCP server expose three retrieval modes; JWT auth resolves `tenant_id` and the database enforces isolation via RLS. The plan has 22 tasks across `assembler/`, `api/`, `auth/`, `mcp/`, and `workers/` packages plus deploy and CI work.

This document is the contract between the coordinator and Codex. Every Codex task is named, scoped, and bounded here. Codex receives one task at a time, executes against the Plan 4 worktree, returns a commit. The coordinator verifies each commit before the next dispatch.

**Plan 4 depends on Plan 3.** The Plan 4 worktree (`~/projects/factvault-plan4/`) must be branched from `main` after Plan 3 merges. The six Codex tasks in §2.1 (T2, T3, T5, T9, T10, T20) are self-contained and do not call Plan 3 code — they can begin as soon as the worktree is set up. Tasks T4, T6, T7, T8, T11+ require Plan 3's runtime artifacts and are coordinator-owned.

---

## 2. The split

### 2.1 Codex-shaped tasks (6)

| Task | Plan section | Files created | Why Codex-shaped |
|------|-------------|---------------|-------------------|
| [T2](#t2--jwt-verification-module) | Task 2 in bundle-and-retrieval | `factvault/auth/__init__.py`, `factvault/auth/jwt.py`, `tests/auth/__init__.py`, `tests/auth/test_jwt.py` | Single module, RS256-only logic, no DB |
| [T3](#t3--local-dev-key-generation--token-issuance-cli) | Task 3 in bundle-and-retrieval | `factvault/auth/dev_keys.py`, `factvault/cli/auth_commands.py`, `tests/auth/test_jwt.py` (append) | Single file per concern, crypto key gen, no DB |
| [T5](#t5--bundle-serialization-helpers) | Task 5 in bundle-and-retrieval | `factvault/assembler/serialize.py`, `tests/assembler/test_serialize.py` | Pure functions only, no DB calls, namedtuple inputs |
| [T9](#t9--pydantic-requestresponse-schemas) | Task 9 in bundle-and-retrieval | `factvault/api/__init__.py`, `factvault/api/schemas.py`, `tests/api/__init__.py`, `tests/api/test_schemas.py` | Pydantic models only, no I/O |
| [T10](#t10--fastapi-app-skeleton--health-routes) | Task 10 in bundle-and-retrieval | `factvault/api/main.py`, `factvault/api/routes/__init__.py`, `factvault/api/routes/health.py`, `tests/api/test_health.py` | Minimal FastAPI app + two health routes, no DB |
| [T20](#t20--openapi-snapshot-test) | Task 20 in bundle-and-retrieval | `tests/api/test_openapi_snapshot.py` | Single test file, snapshot mechanism, no DB |

### 2.2 Coordinator-shaped tasks (16)

| Task | Why coordinator-handled |
|------|-------------------------|
| T1 (pyproject deps) | Coordinator does directly — touches existing file, installs fastapi/uvicorn/python-jose/mcp |
| T4 (bundle assembler core) | **Load-bearing** — integrates `graph.py` + `serialize.py` + DB queries end-to-end |
| T6 (graph expansion via recursive CTE) | Algorithmic recursive CTE; requires deep understanding of schema and conf thresholds |
| T7 (assembler integrates graph) | Extends T4's BundleAssembler with depth>0 wiring; modifies existing file |
| T8 (conflict surfacing) | Extends T4 again; requires `v_conflicts` view from Plan 1 |
| T11 (auth middleware) | **Load-bearing security** — JWT middleware that gates all non-health routes; mistake = open API |
| T12 (entities route) | Depends on T11 middleware shape + T4 assembler |
| T13 (stories route) | Embedding + ANN integration; requires Plan 3's BGE-M3 wrapper |
| T14 (facts route) | Depends on T11 + T4 assembler |
| T15 (dossier worker) | New worker; integrates BundleAssembler with DB caching layer |
| T16 (MCP server) | Depends on entire retrieval stack; wraps three modes as MCP tools |
| T17 (K8s manifests) | Coordinator handles together with existing deploy/ YAMLs for consistency |
| T18 (E2E integration test) | **Load-bearing** — requires full stack: DB, workers, API, MCP |
| T19 (README) | Coherent narrative across all prior tasks |
| T21 (CI update) | Touches existing CI workflow |
| T22 (CLI smoke test) | Depends on T12-T14 route shapes |

---

## 3. Order constraints

Codex tasks can begin immediately on a clean Plan 4 worktree (no dependency on Plan 3 runtime artifacts):

```
[Plan 3 merged to main → coordinator creates factvault-plan4 worktree]
   ↓
[Coordinator: T1 — pyproject deps]
   ↓
T2 (JWT verifier) — no deps
T9 (Pydantic schemas) — no deps
   ↓
T3 (dev keys CLI) — depends on T2 (imports JWTVerifier from factvault.auth.jwt)
T5 (serialization helpers) — no deps; can run in parallel with T2/T9
   ↓
T10 (FastAPI skeleton + health routes) — no deps; can run any time after T1
   ↓
T20 (OpenAPI snapshot test) — depends on T10 (imports factvault.api.main.create_app)
   ↓
[Coordinator: T4, T6-T8, T11-T19, T21-T22]
```

**Plan 4 gating note:** T4 (bundle assembler) calls `expand_entities` from `assembler/graph.py` (T6). Coordinator must implement T4 and T6 together before T7-T8 can extend them. T12-T14 (routes) require T11 (auth middleware) to be in place first. T15 (dossier worker) requires T4's `BundleAssembler`. The Codex tasks (T2, T3, T5, T9, T10, T20) are intentionally free of these inter-dependencies so work can proceed in parallel while Plan 3 finishes.

---

## 4. The Codex prompt template

Each Codex task uses this brief format. The coordinator constructs one per task and pastes it into Codex's session input:

```
You are implementing Task <N> of the factvault bundle-and-retrieval plan.

Repository: https://github.com/petersimmons1972/factvault
Worktree path: ~/projects/factvault-plan4/
Branch: feat/bundle-and-retrieval
Plan: docs/superpowers/plans/2026-05-22-bundle-and-retrieval.md (read only the Task <N> section)

IMPORTANT: This is Plan 4's worktree. Do NOT touch ~/projects/factvault-plan2/ or
~/projects/factvault-plan3/ — those are separate plans.

Pre-flight:
    cd ~/projects/factvault-plan4
    git status -sb        # must be clean
    git branch --show-current   # must be feat/bundle-and-retrieval
    source .venv/bin/activate
    pytest tests/ -v 2>&1 | tail -3   # all prior tests must pass

If pre-flight fails, STOP and report. Do not improvise.

Implementation:
- Read the Task <N> section of the plan
- Implement every file in the task exactly as written
- Apply the six known plan-bug patterns silently (see section 6 of this Codex Handoff doc):
  1. TIMESTAMPTZ from sqlalchemy.dialects.postgresql does not exist — use TIMESTAMP(timezone=True)
  2. sa.UniqueConstraint / sa.LargeBinary need direct imports
  3. psycopg refuses :param::jsonb / :param::vector — use CAST(:param AS jsonb/vector)
  4. Postgres 15+ unique constraints default to NULLS NOT DISTINCT
  5. The conn fixture is single-tenant superuser (bypasses RLS); use app_engine for RLS-sensitive tests
  6. RLS policies use app.tenant_id GUC (not app.current_tenant_id) in Plan 4 code — match
     the GUC name used in factvault/db/rls.py exactly

Verify:
    pytest <task's test file> -v   # expected pass count is in the plan
    pytest tests/ -v 2>&1 | tail -3   # all prior tests still pass

Commit (exact message from plan):
    git add <files>
    git commit -m "<plan's commit message>"

Constraints:
- DO NOT PUSH. The coordinator pushes after verification.
- DO NOT modify the spec, plan, or any file outside the task's listed files.
- DO NOT bundle commits across tasks. One task, one commit.
- Report DONE / DONE_WITH_CONCERNS / BLOCKED with the commit SHA and pytest pass count.
```

---

## 5. Coordinator responsibilities

Between every Codex task:

1. Pull the Codex commit into the worktree (if Codex worked in a different checkout) or `git log` to verify the commit landed (if same worktree)
2. Inspect the diff: `git diff HEAD~1`
3. Confirm the diff touches only the files the task specified
4. Run the full test suite: `pytest tests/ -v` — total count must grow monotonically; nothing previously passing regresses
5. Confirm commit message matches the plan's specified message
6. Mark the corresponding GitHub Issue closed (or let it auto-close via `Closes #N` in the commit footer)
7. Dispatch the next Codex task

After all 6 Codex tasks complete, coordinator picks up T4, T6-T8, T11-T19, T21-T22 directly.

---

## 6. The six known plan-bug patterns

Surfaced during Plan 1 execution by implementer agents. Codex applies these silently to every task; no commentary in the commit message needed:

1. **`TIMESTAMPTZ` is NOT in `sqlalchemy.dialects.postgresql`.** Use `TIMESTAMP(timezone=True)` from `sqlalchemy` directly.
2. **`sa.UniqueConstraint` / `sa.LargeBinary` need direct imports** when `sa` alias isn't already in scope. Prefer explicit imports.
3. **psycopg refuses `:param::jsonb` / `:param::vector`** named-parameter casts. Use `CAST(:param AS jsonb)` / `CAST(:param AS vector)` instead.
4. **Postgres 15+ unique constraints default to `NULLS NOT DISTINCT`.** Tests with duplicate-NULL behavior need distinct tenant_ids or non-NULL values.
5. **The `conn` fixture is single-tenant + superuser (bypasses RLS).** Use the `app_engine` fixture for RLS-sensitive tests. (None of the Codex Plan 4 tasks need RLS testing — auth, serialization, schemas, and health routes operate above the RLS layer.)
6. **Plan 4 uses `app.tenant_id` GUC (not `app.current_tenant_id`).** Application code calls `tenant_context()` from `factvault/db/rls.py` to set it. Match this GUC name exactly in any raw SQL that sets the tenant context.

---

## 7. Per-task briefs

The plan file is authoritative for code content. These briefs are short pointers + the order-of-operations metadata Codex needs.

### T2 — JWT verification module

**Plan section:** Task 2 in `docs/superpowers/plans/2026-05-22-bundle-and-retrieval.md`
**Files created:** `factvault/auth/__init__.py`, `factvault/auth/jwt.py`, `tests/auth/__init__.py`, `tests/auth/test_jwt.py`
**Depends on:** T1 (python-jose installed in venv)
**Blocks:** T3 (dev_keys imports JWTVerifier), T11 (auth middleware uses JWTVerifier)
**Test command:** `pytest tests/auth/test_jwt.py -v`
**Expected:** 6 passed
**Commit message:** `feat(auth): JWT verification module with RS256-only enforcement`

### T3 — Local dev key generation + token issuance CLI

**Plan section:** Task 3 in `docs/superpowers/plans/2026-05-22-bundle-and-retrieval.md`
**Files created:** `factvault/auth/dev_keys.py`, `factvault/cli/auth_commands.py`; appends 2 tests to `tests/auth/test_jwt.py`
**Depends on:** T2 (imports `JWTVerifier` from `factvault.auth.jwt`); T1 (cryptography installed via python-jose)
**Blocks:** T11 (coordinator will use `issue_dev_token` in test fixtures)
**Test command:** `pytest tests/auth/test_jwt.py -v`
**Expected:** 8 passed (6 from T2 + 2 new dev-key round-trip tests)
**Commit message:** `feat(auth): local dev key generation + issue-token CLI subcommand`

### T5 — Bundle serialization helpers

**Plan section:** Task 5 in `docs/superpowers/plans/2026-05-22-bundle-and-retrieval.md`
**Files created:** `factvault/assembler/serialize.py`, `tests/assembler/test_serialize.py`
**Depends on:** Nothing (pure functions, namedtuple inputs, no DB)
**Blocks:** T4 (coordinator's BundleAssembler calls `serialize_entity`, `serialize_fact`, `serialize_relation`, `serialize_source`)
**Test command:** `pytest tests/assembler/test_serialize.py -v`
**Expected:** 12 passed
**Commit message:** `feat(assembler): serialization helpers for entity/fact/relation/source bundle shapes`

### T9 — Pydantic request/response schemas

**Plan section:** Task 9 in `docs/superpowers/plans/2026-05-22-bundle-and-retrieval.md`
**Files created:** `factvault/api/__init__.py`, `factvault/api/schemas.py`, `tests/api/__init__.py`, `tests/api/test_schemas.py`
**Depends on:** T1 (fastapi/pydantic in venv)
**Blocks:** T10 (main.py imports from schemas), T12-T14 (route handlers use BundleResponse)
**Test command:** `pytest tests/api/test_schemas.py -v`
**Expected:** 9 passed
**Commit message:** `feat(api): Pydantic request/response schemas for StoryQuery, FactQuery, BundleResponse`

### T10 — FastAPI app skeleton + health routes

**Plan section:** Task 10 in `docs/superpowers/plans/2026-05-22-bundle-and-retrieval.md`
**Files created:** `factvault/api/main.py`, `factvault/api/routes/__init__.py`, `factvault/api/routes/health.py`, `tests/api/test_health.py`
**Depends on:** T1 (fastapi + uvicorn in venv); T9 must have created `factvault/api/__init__.py`
**Blocks:** T11 (auth middleware extends `main.py`), T20 (snapshot test imports `create_app`)
**Test command:** `pytest tests/api/test_health.py -v`
**Expected:** 4 passed
**Commit message:** `feat(api): FastAPI app skeleton with /healthz and /readyz routes`

### T20 — OpenAPI snapshot test

**Plan section:** Task 20 in `docs/superpowers/plans/2026-05-22-bundle-and-retrieval.md`
**Files created:** `tests/api/test_openapi_snapshot.py`
**Depends on:** T10 (imports `factvault.api.main.create_app`); best dispatched after coordinator's T11-T14 are done so the snapshot captures the full route set
**Blocks:** Nothing (test-only)
**Test command:** `pytest tests/api/test_openapi_snapshot.py -v` (first run: 1 skipped — snapshot created; second run: 1 passed)
**Expected:** 1 passed (after snapshot file exists)
**Commit message:** `test(api): OpenAPI snapshot test — catches accidental breaking changes`

---

## 8. Tracking

GitHub Issues will be filed one per Codex task. Each Issue title format: `Codex T<N>: <short title>`. Each Issue body links to:
- The plan section for the task
- The corresponding section of this Handoff doc (`#t<n>--<slug>`)
- The expected commit message format

When the coordinator merges a Codex commit, the Issue auto-closes via `Closes #N` in the commit footer (Codex includes this in its commit message per the brief template).

---

## 9. When NOT to use Codex

If the coordinator hits any of these conditions for a future "Codex-shaped" task, escalate it back to the coordinator queue:

- Task requires reading and synthesizing more than one prior task's output
- Task has ambiguous file boundaries (e.g., "extend the existing X module")
- Task involves judgment about existing patterns the plan doesn't fully specify
- Task is load-bearing for downstream pillars and a silent error would be hard to detect

The current 6-task Codex batch was selected explicitly to avoid these conditions.

---

## 10. Status log

- T2: queued
- T3: queued
- T5: queued
- T9: queued
- T10: queued
- T20: queued (dispatch after coordinator's T11-T14 for a complete snapshot)
