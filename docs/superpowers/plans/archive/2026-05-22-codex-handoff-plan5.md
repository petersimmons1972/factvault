# Plan 5 Codex Handoff

**Status:** active
**Plan:** [Deploy and Examples Implementation Plan](./2026-05-22-deploy-and-examples.md)
**Worktree:** `~/projects/factvault-plan5/` on branch `feat/deploy-and-examples`
**Repo:** https://github.com/petersimmons1972/factvault

---

## 1. Overview

Plan 5 closes the project: it wires the full operational stack (docker-compose, Dockerfile, doctor CLI, examples framework) and delivers four runnable domain examples that double as integration tests. After this plan merges, a new adopter can `git clone && docker compose up && factvault doctor && factvault example run <name>` and have a working, sourced, hallucination-resistant research database within 10 minutes. The plan has 22 tasks split across docker, doctor, examples, docs, Helm, and CI. No new Python runtime dependencies — this plan is mostly integration, documentation, and ops glue.

This document is the contract between the coordinator and Codex. Every Codex task is named, scoped, and bounded here. Codex receives one task at a time, executes against the Plan 5 worktree, returns a commit. The coordinator verifies each commit before the next dispatch.

**Plan 5 depends on Plans 1-4 at runtime, but most Codex tasks in this plan are independent of Plans 3-4 shipping.** The four example directories (T10-T13), the documentation (T15-T19), the Dockerfile (T2), the compose override (T3), the env.example (T4), and the doctor checks (T5-T7) all depend only on the SPEC of Plans 3-4, not their runtime artifacts. Codex can work on these tasks now. The full-stack integration test (T20, coordinator-owned) requires Plans 1-4 fully merged.

---

## 2. The split

### 2.1 Codex-shaped tasks (18)

| Task | Plan section | Files created | Why Codex-shaped |
|------|-------------|---------------|-------------------|
| [T2](#t2--application-dockerfile) | Task 2 in deploy-and-examples | `docker/app/Dockerfile` | Single file, Chainguard multi-stage pattern, no logic |
| [T3](#t3--docker-composeoverrideexampleyml) | Task 3 in deploy-and-examples | `docker-compose.override.example.yml`, `.gitignore` (append) | Single YAML, no code |
| [T4](#t4--envexample-final-pass) | Task 4 in deploy-and-examples | `.env.example` (replace) | Single file, all env vars documented, no logic |
| [T5](#t5--factvault-doctor-individual-check-functions) | Task 5 in deploy-and-examples | `factvault/doctor/__init__.py`, `factvault/doctor/checks.py`, `tests/doctor/test_checks.py` | Standalone check functions; external I/O mocked via pytest-httpx |
| [T6](#t6--factvault-doctor-canary-fact-end-to-end) | Task 6 in deploy-and-examples | `factvault/doctor/canary.py`, `tests/doctor/test_canary.py` | Self-contained canary pipeline; no production data dependencies |
| [T7](#t7--factvault-doctor-cli) | Task 7 in deploy-and-examples | `factvault/doctor/cli.py`, `tests/doctor/test_cli.py` | Click CLI wiring with mocked checks |
| [T8](#t8--examples-framework-base-loader) | Task 8 in deploy-and-examples | `factvault/examples/__init__.py`, `factvault/examples/base.py`, `tests/examples/test_base.py` | `Example` class reads YAML/fixture files, no DB code that requires Plans 3-4 runtime |
| [T9](#t9--examples-cli) | Task 9 in deploy-and-examples | `factvault/examples/cli.py`, `tests/examples/test_cli.py` | Click CLI with mocked Example loader |
| [T10](#t10--ai-startup-tracking-example) | Task 10 in deploy-and-examples | `examples/ai-startup-tracking/` directory (README.md, properties.yaml, seeds.yaml, fixtures/*, expected/*, run.sh), `tests/examples/test_ai_startup_tracking.py` | Entirely fictional data, no DB, file content only |
| [T11](#t11--political-research-example) | Task 11 in deploy-and-examples | `examples/political-research/` directory (same structure), `tests/examples/test_political_research.py` | Entirely fictional data, file content only |
| [T12](#t12--pharma-trial-monitoring-example) | Task 12 in deploy-and-examples | `examples/pharma-trial-monitoring/` directory (same structure), `tests/examples/test_pharma_trial_monitoring.py` | Entirely fictional data, file content only |
| [T13](#t13--investigative-journalism-example) | Task 13 in deploy-and-examples | `examples/investigative-journalism/` directory (same structure), `tests/examples/test_investigative_journalism.py` | Entirely fictional data, file content only |
| [T15](#t15--readme-final-pass) | Task 15 in deploy-and-examples | `README.md` (replace), `tests/docs/test_readme.py` | Pure prose + structural tests, no logic |
| [T16](#t16--docsquickstartmd) | Task 16 in deploy-and-examples | `docs/quickstart.md`, `tests/docs/test_quickstart.py` | Pure prose + structural tests, no logic |
| [T17](#t17--docsoperationsmd) | Task 17 in deploy-and-examples | `docs/operations.md`, `tests/docs/test_operations.py` | Pure prose + structural tests, no logic |
| [T18](#t18--docssecuritymd) | Task 18 in deploy-and-examples | `docs/security.md`, `tests/docs/test_security.py` | Pure prose + structural tests, no logic |
| [T19](#t19--docstroubleshootingmd) | Task 19 in deploy-and-examples | `docs/troubleshooting.md`, `tests/docs/test_troubleshooting.py` | Pure prose + structural tests, no logic |
| [T21](#t21--helm-chart--optional-for-v1) | Task 21 in deploy-and-examples | `deploy/helm/factvault/` (Chart.yaml, values.yaml, templates/, README.md) | Mechanical YAML/Helm scaffolding, optional scope |

### 2.2 Coordinator-shaped tasks (4)

| Task | Why coordinator-handled |
|------|-------------------------|
| T1 (docker-compose.yml extension) | Modifies the existing Plan 1 `docker-compose.yml` — coordinator handles to avoid cross-plan file conflicts |
| T14 (top-level CLI aggregator) | Integrates doctor, examples, workers, and auth CLI groups into one `factvault` entry point; requires knowing the exact CLI shape of all prior plans |
| T20 (full-stack integration test) | **Load-bearing** — spins up docker-compose, runs the full pipeline, asserts green; requires Plans 1-4 fully merged |
| T22 (CI + release workflows) | Touches existing CI workflow and adds a tag-triggered release workflow; coordinator handles for consistency |

---

## 3. Order constraints

Most Codex tasks are independent. Parallelism is high — dispatch groups in any order within each tier:

```
[Plan 5 worktree created from main (Plans 1-4 need NOT be merged yet for Codex tasks)]
   ↓
TIER 1 — fully independent (dispatch any time, in any order):
    T2 (Dockerfile)
    T3 (compose override template)
    T4 (.env.example)
    T10, T11, T12, T13 (four example directories — no deps on each other)
    T15, T16, T17, T18, T19 (five docs files — no deps on each other)
    T21 (Helm chart — optional)
   ↓
TIER 2 — depends on T8 (examples base loader):
    T8 first, then T9 (examples CLI imports Example class from base.py)
    T10-T13 tests depend on T8 (tests import Example for fixture loading)
   ↓
TIER 3 — depends on T5:
    T5 first, then T6 (canary imports CheckResult from checks.py)
    T7 (doctor CLI) depends on T5 + T6 (aggregates all checks)
   ↓
[Coordinator: T1 (modifies existing compose file), T14 (CLI aggregator), T20 (integration test), T22 (CI)]
```

**Key independence note:** T10-T13 (example directories) and T15-T19 (docs) depend only on the plan spec, not on Plans 3-4 runtime code. Codex can write all four examples and all five docs files without Plans 3-4 being merged. The `tests/examples/test_ai_startup_tracking.py` etc. tests use the `Example` base loader (T8) to validate the directory structure — they do not run the live pipeline.

---

## 4. The Codex prompt template

Each Codex task uses this brief format. The coordinator constructs one per task and pastes it into Codex's session input:

```
You are implementing Task <N> of the factvault deploy-and-examples plan.

Repository: https://github.com/petersimmons1972/factvault
Worktree path: ~/projects/factvault-plan5/
Branch: feat/deploy-and-examples
Plan: docs/superpowers/plans/2026-05-22-deploy-and-examples.md (read only the Task <N> section)

IMPORTANT: This is Plan 5's worktree. Do NOT touch ~/projects/factvault-plan2/,
~/projects/factvault-plan3/, or ~/projects/factvault-plan4/ — those are separate plans.

NOTE: Plan 5 Codex tasks do NOT require Plans 3-4 to be merged. The example
directories and docs depend only on the plan spec, not on runtime imports.

Pre-flight:
    cd ~/projects/factvault-plan5
    git status -sb        # must be clean
    git branch --show-current   # must be feat/deploy-and-examples
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
  6. RLS policies use app.tenant_id GUC — match the GUC name used in factvault/db/rls.py exactly

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

After all 18 Codex tasks complete, coordinator picks up T1, T14, T20, T22 directly.

---

## 6. The six known plan-bug patterns

Surfaced during Plan 1 execution by implementer agents. Codex applies these silently to every task; no commentary in the commit message needed:

1. **`TIMESTAMPTZ` is NOT in `sqlalchemy.dialects.postgresql`.** Use `TIMESTAMP(timezone=True)` from `sqlalchemy` directly.
2. **`sa.UniqueConstraint` / `sa.LargeBinary` need direct imports** when `sa` alias isn't already in scope. Prefer explicit imports.
3. **psycopg refuses `:param::jsonb` / `:param::vector`** named-parameter casts. Use `CAST(:param AS jsonb)` / `CAST(:param AS vector)` instead.
4. **Postgres 15+ unique constraints default to `NULLS NOT DISTINCT`.** Tests with duplicate-NULL behavior need distinct tenant_ids or non-NULL values.
5. **The `conn` fixture is single-tenant + superuser (bypasses RLS).** Use the `app_engine` fixture for RLS-sensitive tests. (Most Plan 5 Codex tasks have no DB code; this pattern applies if Codex touches DB-touching helpers in T5-T8.)
6. **Plan 5 uses `app.tenant_id` GUC.** The canary in T6 sets this GUC directly via raw SQL. Match the GUC name exactly — it is `app.tenant_id`, not `app.current_tenant_id`.

---

## 7. Per-task briefs

The plan file is authoritative for code content. These briefs are short pointers + the order-of-operations metadata Codex needs.

### T2 — Application Dockerfile

**Plan section:** Task 2 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `docker/app/Dockerfile`
**Depends on:** Nothing
**Blocks:** T1 (coordinator's compose extension references this Dockerfile)
**Test command:** `docker build -t factvault-app:test docker/app/ && docker run --rm factvault-app:test id` (no pytest test)
**Expected:** Image builds cleanly; `id` shows `uid=65532 gid=65532`; image size under 600 MB
**Commit message:** `feat(docker): add multi-stage app Dockerfile (wolfi-base + tini + nonroot 65532)`

### T3 — docker-compose.override.example.yml

**Plan section:** Task 3 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `docker-compose.override.example.yml`; appends `docker-compose.override.yml` to `.gitignore`
**Depends on:** Nothing
**Blocks:** Nothing (opt-in override template for users)
**Test command:** `grep -q 'docker-compose.override.yml' .gitignore && echo OK` (no pytest test)
**Expected:** File created; gitignore entry confirmed
**Commit message:** `feat(compose): add opt-in override template for searxng + ollama services`

### T4 — .env.example final pass

**Plan section:** Task 4 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `.env.example` (replace existing file)
**Depends on:** Nothing
**Blocks:** T16 (quickstart.md references env vars by name)
**Test command:** `grep -c 'FACTVAULT_' .env.example` (no pytest test; expect ≥15 vars)
**Expected:** All 15+ env vars present; each with an inline comment
**Commit message:** `feat(config): extend .env.example with all stack env vars + inline docs`

### T5 — factvault doctor: individual check functions

**Plan section:** Task 5 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `factvault/doctor/__init__.py`, `factvault/doctor/checks.py`, `tests/doctor/test_checks.py`
**Depends on:** Nothing at import time (sentence-transformers and httpx are runtime deps from Plans 3-4; the check functions import them lazily)
**Blocks:** T6 (canary.py imports `CheckResult` from `checks.py`), T7 (CLI aggregates all checks)
**Test command:** `pytest tests/doctor/test_checks.py -v`
**Expected:** 10 passed (DB checks use testcontainers or mocked engine; HTTP checks use pytest-httpx mocks)
**Commit message:** `feat(doctor): individual health check functions (db, pgvector, rls, wayback, embedding, llm)`

### T6 — factvault doctor: canary fact end-to-end

**Plan section:** Task 6 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `factvault/doctor/canary.py`, `tests/doctor/test_canary.py`
**Depends on:** T5 (imports `CheckResult` from `factvault.doctor.checks`)
**Blocks:** T7 (doctor CLI calls `run_canary`)
**Test command:** `pytest tests/doctor/test_canary.py -v`
**Expected:** 5 passed (canary tests use testcontainers postgres with the Plan 1 schema applied)
**Commit message:** `feat(doctor): canary fact ingest + dossier assembly end-to-end check`

### T7 — factvault doctor: CLI

**Plan section:** Task 7 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `factvault/doctor/cli.py`, `tests/doctor/test_cli.py`
**Depends on:** T5 + T6 (imports check functions and `run_canary`)
**Blocks:** T14 (coordinator's CLI aggregator registers the doctor group)
**Test command:** `pytest tests/doctor/test_cli.py -v`
**Expected:** 6 passed (CLI tests use mocked check functions via `unittest.mock.patch`)
**Commit message:** `feat(doctor): factvault doctor CLI — aggregates all checks with exit-code 1 on failure`

### T8 — Examples framework: base loader

**Plan section:** Task 8 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `factvault/examples/__init__.py`, `factvault/examples/base.py`, `tests/examples/test_base.py`
**Depends on:** Nothing (reads YAML and fixture files from disk; no DB calls in the Codex scope)
**Blocks:** T9 (examples CLI imports `Example`), T10-T13 (tests import `Example` to validate directories)
**Test command:** `pytest tests/examples/test_base.py -v`
**Expected:** 8 passed (tests use tmp_path with synthetic YAML/fixture files)
**Commit message:** `feat(examples): base Example loader (properties, seeds, fixtures, golden output diff)`

### T9 — Examples CLI

**Plan section:** Task 9 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `factvault/examples/cli.py`, `tests/examples/test_cli.py`
**Depends on:** T8 (imports `Example` from `factvault.examples.base`)
**Blocks:** T14 (coordinator's CLI aggregator registers the examples group)
**Test command:** `pytest tests/examples/test_cli.py -v`
**Expected:** 5 passed (CLI tests mock Example.run and assert correct output + exit codes)
**Commit message:** `feat(examples): factvault example list|info|run CLI`

### T10 — AI startup tracking example

**Plan section:** Task 10 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `examples/ai-startup-tracking/` (README.md, properties.yaml, seeds.yaml, fixtures/\*, expected/dossier-novaspark.json, run.sh), `tests/examples/test_ai_startup_tracking.py`
**Depends on:** T8 (test imports `Example` to load and validate the directory)
**Blocks:** Nothing
**Test command:** `pytest tests/examples/test_ai_startup_tracking.py -v`
**Expected:** 4 passed (validates directory structure, YAML parseable, fixture files non-empty, golden JSON valid)
**Commit message:** `feat(examples): AI startup tracking example (NovaSpark Technologies, 8 fictional companies)`

### T11 — Political research example

**Plan section:** Task 11 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `examples/political-research/` (README.md, properties.yaml, seeds.yaml, fixtures/\*, expected/\*, run.sh), `tests/examples/test_political_research.py`
**Depends on:** T8 (test imports `Example`)
**Blocks:** Nothing
**Test command:** `pytest tests/examples/test_political_research.py -v`
**Expected:** 4 passed
**Commit message:** `feat(examples): political research example (FEC filings, voting records, fictional legislators)`

### T12 — Pharma trial monitoring example

**Plan section:** Task 12 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `examples/pharma-trial-monitoring/` (same structure), `tests/examples/test_pharma_trial_monitoring.py`
**Depends on:** T8 (test imports `Example`)
**Blocks:** Nothing
**Test command:** `pytest tests/examples/test_pharma_trial_monitoring.py -v`
**Expected:** 4 passed
**Commit message:** `feat(examples): pharma trial monitoring example (fictional NCT IDs, trial outcomes, sponsors)`

### T13 — Investigative journalism example

**Plan section:** Task 13 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `examples/investigative-journalism/` (same structure), `tests/examples/test_investigative_journalism.py`
**Depends on:** T8 (test imports `Example`)
**Blocks:** Nothing
**Test command:** `pytest tests/examples/test_investigative_journalism.py -v`
**Expected:** 4 passed
**Commit message:** `feat(examples): investigative journalism example (financial disclosures, shell companies, fictional entities)`

### T15 — README final pass

**Plan section:** Task 15 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `README.md` (replace), `tests/docs/test_readme.py`
**Depends on:** Nothing (prose only; no runtime imports)
**Blocks:** Nothing
**Test command:** `pytest tests/docs/test_readme.py -v`
**Expected:** 7 passed (structural tests: badge row, quickstart, four examples, dossier-vs-story, source-existence headline, NASA image or alt text, contributing pointer)
**Commit message:** `docs(readme): final pass — source-existence headline, dossier-vs-story, 4 examples, quickstart`

### T16 — docs/quickstart.md

**Plan section:** Task 16 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `docs/quickstart.md`, `tests/docs/test_quickstart.py`
**Depends on:** Nothing
**Blocks:** Nothing
**Test command:** `pytest tests/docs/test_quickstart.py -v`
**Expected:** 7 passed (structural: git clone, .env, docker compose, factvault doctor, curl examples, MCP section, dossier + story endpoints)
**Commit message:** `docs: add quickstart.md — 5-minute first-success guide`

### T17 — docs/operations.md

**Plan section:** Task 17 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `docs/operations.md`, `tests/docs/test_operations.py`
**Depends on:** Nothing
**Blocks:** Nothing
**Test command:** `pytest tests/docs/test_operations.py -v`
**Expected:** 8 passed (structural: scaling, backup/restore, pg_dump, RLS restore verification, monitoring, secret rotation, upgrade procedure, disaster recovery)
**Commit message:** `docs: add operations.md — scaling, backup/restore, monitoring, secret rotation, DR`

### T18 — docs/security.md

**Plan section:** Task 18 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `docs/security.md`, `tests/docs/test_security.py`
**Depends on:** Nothing
**Blocks:** Nothing
**Test command:** `pytest tests/docs/test_security.py -v`
**Expected:** 6 passed (structural: RLS section, JWT section, threat model, source-existence as security property, audit log, IdP integration)
**Commit message:** `docs: add security.md — RLS isolation, JWT auth, threat model, source-existence security property`

### T19 — docs/troubleshooting.md

**Plan section:** Task 19 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `docs/troubleshooting.md`, `tests/docs/test_troubleshooting.py`
**Depends on:** Nothing
**Blocks:** Nothing
**Test command:** `pytest tests/docs/test_troubleshooting.py -v`
**Expected:** 7 passed (structural: Wayback section, trafilatura section, RLS debugging, MCP connection, embedding model, pgvector extension, symptom/diagnostic/fix pattern)
**Commit message:** `docs: add troubleshooting.md — Wayback limits, trafilatura, RLS, MCP, embedding, pgvector`

### T21 — Helm chart (optional for v1)

**Plan section:** Task 21 in `docs/superpowers/plans/2026-05-22-deploy-and-examples.md`
**Files created:** `deploy/helm/factvault/Chart.yaml`, `deploy/helm/factvault/values.yaml`, `deploy/helm/factvault/templates/` (6 template files), `deploy/helm/factvault/README.md`
**Depends on:** Nothing (pure YAML + Helm templating, no Python)
**Blocks:** Nothing (optional scope — docker-compose is the supported v1 path)
**Test command:** `helm lint deploy/helm/factvault/` (if helm is installed; otherwise skip)
**Expected:** Chart lints cleanly; no pytest tests for this task
**Commit message:** `feat(helm): add optional Helm chart for production K8s deployment (v1.1 scope)`

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

The current 18-task Codex batch was selected explicitly to avoid these conditions. T1, T14, T20, and T22 were intentionally excluded because they touch existing cross-plan files, aggregate multi-plan CLI groups, or constitute the load-bearing full-stack test gate.

---

## 10. Status log

- T2: queued
- T3: queued
- T4: queued
- T5: queued
- T6: queued
- T7: queued
- T8: queued
- T9: queued
- T10: queued
- T11: queued
- T12: queued
- T13: queued
- T15: queued
- T16: queued
- T17: queued
- T18: queued
- T19: queued
- T21: queued (optional — defer if v1 release is time-boxed)
