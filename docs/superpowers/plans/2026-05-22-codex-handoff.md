# Plan 2 Codex Handoff

**Status:** active
**Plan:** [Source Pipeline Implementation Plan](./2026-05-22-source-pipeline.md)
**Worktree:** `~/projects/factvault-plan2/` on branch `feat/source-pipeline`
**Repo:** https://github.com/petersimmons1972/factvault

---

## 1. Overview

Factvault is a self-hostable research database that tracks whether claimed facts still exist at their source URLs. Plan 2 builds the source-existence pillar: pluggable collectors ingest URLs into `sources` rows; an archive worker captures full body, computes content hash, and submits to Wayback Machine; a periodic verify worker re-fetches and confirms excerpts still exist. The plan has 22 tasks across three packages (`collectors/`, `archiving/`, `workers/`) plus config, deploy, and CI work. Execution is split: tasks that are single-file and mechanical go to Codex, tasks requiring multi-module judgment stay with the coordinator.

This document is the contract between the coordinator and Codex. Every Codex task is named, scoped, and bounded here. Codex receives a single task at a time, executes against the worktree, returns a commit. The coordinator verifies each commit before the next task is dispatched.

---

## 2. The split

### 2.1 Codex-shaped tasks (10)

| Task | Plan section | Files created | Why Codex-shaped |
|------|-------------|---------------|-------------------|
| [T2](#t2--collector-abc-and-rawdocument) | Collector ABC + RawDocument | `factvault/collectors/base.py`, `factvault/collectors/__init__.py`, `tests/collectors/__init__.py`, `tests/collectors/test_base.py` | Single module, dataclass + ABC, no DB integration |
| [T3](#t3--http-collector) | HTTP collector | `factvault/collectors/http.py`, `tests/collectors/test_http.py` | Single class, httpx_mock'd tests |
| [T4](#t4--rss-collector) | RSS collector | `factvault/collectors/rss.py`, `tests/collectors/test_rss.py`, `tests/fixtures/articles/sample.rss`, `tests/fixtures/__init__.py`, `tests/fixtures/articles/.gitkeep` | Single file, feedparser wrapper |
| [T5](#t5--sitemap-collector) | Sitemap collector | `factvault/collectors/sitemap.py`, `tests/collectors/test_sitemap.py`, `tests/fixtures/articles/sample_sitemap.xml`, `tests/fixtures/articles/sample_sitemap_index.xml` | Single file, XML parsing |
| [T6](#t6--searxng-collector) | SearXNG collector | `factvault/collectors/searxng.py`, `tests/collectors/test_searxng.py`, `tests/fixtures/wayback_responses/searxng_response.json` | Single file, mocked HTTP |
| [T7](#t7--wayback-cdx-collector) | Wayback CDX collector | `factvault/collectors/wayback_cdx.py`, `tests/collectors/test_wayback_cdx.py`, `tests/fixtures/wayback_responses/cdx_response.json` | Single file, mocked HTTP |
| [T9](#t9--wayback-spn-client) | Wayback Save Page Now client | `factvault/archiving/wayback.py`, `tests/archiving/__init__.py`, `tests/archiving/test_wayback.py` | Single file, retry/backoff logic |
| [T10](#t10--trafilatura-wrapper) | trafilatura extract wrapper | `factvault/archiving/extract.py`, `tests/archiving/test_extract.py`, `tests/fixtures/articles/article.html`, `tests/fixtures/articles/paywall.html` | Thin wrapper |
| [T11](#t11--sha-256-hash-helper) | SHA-256 hash helper | `factvault/archiving/hash.py`, `tests/archiving/test_hash.py` | Trivial single function |
| [T18](#t18--kubernetes-cronjob-yaml) | K8s CronJob YAML | `deploy/k8s/verify-worker-cronjob.yaml` | Single YAML file, no code |

### 2.2 Coordinator-shaped tasks (12)

| Task | Why coordinator-handled |
|------|-------------------------|
| T1 (pyproject deps) | Trivial; coordinator does it directly before dispatching any Codex task |
| T8 (upload collector) | DB writes + transaction handling + model imports |
| T12 (Worker ABC + CLI) | Click integration + entry point wiring + cross-module registry |
| T13 (archive worker) | **Load-bearing** — integrates T2-T11 outputs end-to-end; needs holistic context |
| T14 (verify worker) | **Load-bearing** — complex business logic for hash diff + excerpt presence |
| T15 (YAML config) | Extends existing `factvault/config.py`; Pydantic model design |
| T16 (collector CLI subcommand) | Depends on T12's CLI shape |
| T17 (integration e2e test) | **Load-bearing** — requires entire pipeline assembled |
| T19 (Wayback rate-limit hardening) | Refines T9 informed by T13's actual call pattern |
| T20 (source-pipeline README) | Coherent narrative across all 21 prior tasks |
| T21 (CI update) | Touches existing CI workflow |
| T22 (CLI smoke test) | Depends on T16's `--dry-run` flag |

---

## 3. Order constraints

Codex tasks have dependencies. Execute them in this order, with the coordinator's T1 and T8 interleaved as noted:

```
[Coordinator: T1 — pyproject deps]
   ↓
T2 (Collector ABC)
   ↓
T3, T4, T5, T6, T7 — collectors (parallelizable; all depend on T2)
   ↓
T11 (SHA-256 hash helper) — no deps, can also run in parallel with T3-T7
   ↓
T10 (trafilatura wrapper) — no deps on collectors, can run in parallel with T3-T7
   ↓
T9 (Wayback SPN client) — no hard deps on T2-T8, but logically follows
   ↓
[Coordinator: T8 — upload collector — needs DB models from Plan 1]
   ↓
T18 (K8s CronJob YAML) — no code deps; can run any time
   ↓
[Coordinator: T12-T17, T19-T22]
```

**Parallelism rule:** Codex CAN work on T3-T7 simultaneously across different sessions because each is a separate file. However, two parallel Codex sessions MUST use different git worktrees to avoid the cross-contamination disaster the coordinator hit earlier in this project. **For now, route all Codex tasks through ONE session, sequential.** Parallel Codex is a follow-up optimization.

---

## 4. The Codex prompt template

Each Codex task uses this brief format. The coordinator constructs one per task and pastes it into Codex's session input:

```
You are implementing Task <N> of the factvault source-pipeline plan.

Repository: https://github.com/petersimmons1972/factvault
Worktree path: ~/projects/factvault-plan2/
Branch: feat/source-pipeline
Plan: docs/superpowers/plans/2026-05-22-source-pipeline.md (read only the Task <N> section)

Pre-flight:
    cd ~/projects/factvault-plan2
    git status -sb        # must be clean
    git branch --show-current   # must be feat/source-pipeline
    source .venv/bin/activate   # venv exists from earlier setup
    pytest tests/ -v 2>&1 | tail -3   # all prior tests must pass

If pre-flight fails, STOP and report. Do not improvise.

Implementation:
- Read the Task <N> section of the plan
- Implement every file in the task exactly as written
- Apply the six known plan-bug patterns silently (see section 6 of the Codex Handoff doc):
  1. TIMESTAMPTZ from sqlalchemy.dialects.postgresql does not exist — use TIMESTAMP(timezone=True) from sqlalchemy
  2. sa.UniqueConstraint / sa.LargeBinary need direct imports
  3. psycopg refuses :param::jsonb / :param::vector — use CAST(:param AS jsonb/vector)
  4. Postgres 15+ unique constraints default to NULLS NOT DISTINCT
  5. The conn fixture is single-tenant superuser (bypasses RLS); use app_engine for RLS-sensitive tests
  6. RLS policies wrap current_setting(...) with NULLIF(..., '') before ::uuid cast

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

1. Pull the Codex commit into the worktree (if Codex worked in a different checkout) or just `git log` to verify the commit landed (if same worktree)
2. Inspect the diff: `git diff HEAD~1`
3. Confirm the diff touches only the files the task specified
4. Run the full test suite: `pytest tests/ -v` — total count must grow monotonically; nothing previously passing regresses
5. Confirm commit message matches the plan's specified message
6. Mark the corresponding GitHub Issue closed (or let it auto-close via commit message footer `Closes #N`)
7. Dispatch the next Codex task

After all 10 Codex tasks complete, coordinator picks up T12-T17 and T19-T22 directly.

---

## 6. The six known plan-bug patterns

Surfaced during Plan 1 execution by implementer agents. Codex applies these silently to every task; no commentary in the commit message needed:

1. **`TIMESTAMPTZ` is NOT in `sqlalchemy.dialects.postgresql`.** Use `TIMESTAMP(timezone=True)` from `sqlalchemy` directly.
2. **`sa.UniqueConstraint` / `sa.LargeBinary` need direct imports** when `sa` alias isn't already in scope. Prefer explicit imports.
3. **psycopg refuses `:param::jsonb` / `:param::vector`** named-parameter casts. Use `CAST(:param AS jsonb)` / `CAST(:param AS vector)` instead.
4. **Postgres 15+ unique constraints default to `NULLS NOT DISTINCT`.** Tests with duplicate-NULL behavior need distinct tenant_ids or non-NULL values.
5. **The `conn` fixture is single-tenant + superuser (bypasses RLS).** Use the `app_engine` fixture for RLS-sensitive tests. (None of the Codex Plan 2 tasks need RLS testing — collectors and archivers operate above the RLS layer — but flagged for awareness.)
6. **RLS policies wrap `current_setting('app.tenant_id', true)` with `NULLIF(..., '')` before `::uuid` cast.** (Already in the DB schema; Codex tasks that query the schema can rely on this.)

---

## 7. Per-task briefs

The plan file is authoritative for code content. These briefs are short pointers + the order-of-operations metadata Codex needs.

### T2 -- Collector ABC and RawDocument

**Plan section:** Task 2 in `docs/superpowers/plans/2026-05-22-source-pipeline.md`
**Files created:** `factvault/collectors/base.py`, `factvault/collectors/__init__.py`, `tests/collectors/__init__.py`, `tests/collectors/test_base.py`
**Depends on:** Coordinator's T1 (pyproject deps must be installed in the venv)
**Blocks:** T3, T4, T5, T6, T7 (all collectors import the base)
**Test command:** `pytest tests/collectors/test_base.py -v`
**Expected:** 10 passed
**Commit message:** `feat(collectors): Collector ABC + RawDocument dataclass + registry`

### T3 -- HTTP collector

**Plan section:** Task 3 in `docs/superpowers/plans/2026-05-22-source-pipeline.md`
**Files created:** `factvault/collectors/http.py`, `tests/collectors/test_http.py`
**Depends on:** T2 (imports `factvault.collectors.base`)
**Blocks:** nothing (T8 upload collector is coordinator-owned)
**Test command:** `pytest tests/collectors/test_http.py -v`
**Expected:** 8 passed
**Commit message:** `feat(collectors): generic HTTP collector with httpx + title extraction`

### T4 -- RSS collector

**Plan section:** Task 4 in `docs/superpowers/plans/2026-05-22-source-pipeline.md`
**Files created:** `factvault/collectors/rss.py`, `tests/collectors/test_rss.py`, `tests/fixtures/articles/sample.rss`, `tests/fixtures/__init__.py`, `tests/fixtures/articles/.gitkeep`
**Depends on:** T2 (imports `factvault.collectors.base`)
**Blocks:** nothing
**Test command:** `pytest tests/collectors/test_rss.py -v`
**Expected:** 8 passed
**Commit message:** `feat(collectors): RSS/Atom feed collector with feedparser + GUID dedup`

### T5 -- Sitemap collector

**Plan section:** Task 5 in `docs/superpowers/plans/2026-05-22-source-pipeline.md`
**Files created:** `factvault/collectors/sitemap.py`, `tests/collectors/test_sitemap.py`, `tests/fixtures/articles/sample_sitemap.xml`, `tests/fixtures/articles/sample_sitemap_index.xml`
**Depends on:** T2 (imports `factvault.collectors.base`)
**Blocks:** nothing
**Test command:** `pytest tests/collectors/test_sitemap.py -v`
**Expected:** 7 passed
**Commit message:** `feat(collectors): XML sitemap crawler with index follow + lastmod filter`

### T6 -- SearXNG collector

**Plan section:** Task 6 in `docs/superpowers/plans/2026-05-22-source-pipeline.md`
**Files created:** `factvault/collectors/searxng.py`, `tests/collectors/test_searxng.py`, `tests/fixtures/wayback_responses/searxng_response.json`
**Depends on:** T2 (imports `factvault.collectors.base`)
**Blocks:** nothing
**Test command:** `pytest tests/collectors/test_searxng.py -v`
**Expected:** 8 passed
**Commit message:** `feat(collectors): SearXNG query collector with snippet metadata`

### T7 -- Wayback CDX collector

**Plan section:** Task 7 in `docs/superpowers/plans/2026-05-22-source-pipeline.md`
**Files created:** `factvault/collectors/wayback_cdx.py`, `tests/collectors/test_wayback_cdx.py`, `tests/fixtures/wayback_responses/cdx_response.json`
**Depends on:** T2 (imports `factvault.collectors.base`)
**Blocks:** nothing
**Test command:** `pytest tests/collectors/test_wayback_cdx.py -v`
**Expected:** 8 passed
**Commit message:** `feat(collectors): Wayback CDX archive replay collector`

### T9 -- Wayback SPN client

**Plan section:** Task 9 in `docs/superpowers/plans/2026-05-22-source-pipeline.md`
**Files created:** `factvault/archiving/wayback.py`, `tests/archiving/__init__.py`, `tests/archiving/test_wayback.py`
**Depends on:** T1 (httpx in venv); no dependency on T2-T7
**Blocks:** T19 (coordinator-owned rate-limit hardening extends this file)
**Test command:** `pytest tests/archiving/test_wayback.py -v`
**Expected:** 6 passed
**Commit message:** `feat(archiving): Wayback SPN client with exponential backoff, returns None on failure`

### T10 -- trafilatura wrapper

**Plan section:** Task 10 in `docs/superpowers/plans/2026-05-22-source-pipeline.md`
**Files created:** `factvault/archiving/extract.py`, `tests/archiving/test_extract.py`, `tests/fixtures/articles/article.html`, `tests/fixtures/articles/paywall.html`
**Depends on:** T1 (trafilatura in venv); `tests/archiving/__init__.py` created by T9
**Blocks:** T13 (coordinator's archive worker calls `extract_text`)
**Test command:** `pytest tests/archiving/test_extract.py -v`
**Expected:** 7 passed
**Commit message:** `feat(archiving): trafilatura extract_text wrapper with pinned config`

### T11 -- SHA-256 hash helper

**Plan section:** Task 11 in `docs/superpowers/plans/2026-05-22-source-pipeline.md`
**Files created:** `factvault/archiving/hash.py`, `tests/archiving/test_hash.py`
**Depends on:** T1; `tests/archiving/__init__.py` created by T9 (dispatch T11 after T9)
**Blocks:** T13 (coordinator's archive worker calls `compute_hash`)
**Test command:** `pytest tests/archiving/test_hash.py -v`
**Expected:** 8 passed
**Commit message:** `feat(archiving): canonical SHA-256 compute_hash helper (promotes Task 8 stub)`

### T18 -- Kubernetes CronJob YAML

**Plan section:** Task 18 in `docs/superpowers/plans/2026-05-22-source-pipeline.md`
**Files created:** `deploy/k8s/verify-worker-cronjob.yaml`
**Depends on:** nothing (pure YAML, no imports)
**Blocks:** nothing in Plan 2; referenced by operations runbooks
**Test command:** `kubectl --dry-run=client apply -f deploy/k8s/verify-worker-cronjob.yaml` (optional lint only; no pytest test)
**Expected:** file validates cleanly; no pytest tests for this task
**Commit message:** `deploy(k8s): CronJob for verify-worker (daily 03:00 UTC, Chainguard, nonroot)`

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

The current 10-task Codex batch was selected explicitly to avoid these conditions.

---

## 10. Status log

- T2: queued
- T3: queued
- T4: queued
- T5: queued
- T6: queued
- T7: queued
- T9: queued
- T10: queued
- T11: queued
- T18: queued
