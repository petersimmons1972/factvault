# Plan 3 Codex Handoff

**Status:** active
**Plan:** [Fact Pipeline Implementation Plan](./2026-05-22-fact-pipeline.md)
**Worktree:** `~/projects/factvault-plan3/` on branch `feat/fact-pipeline`
**Repo:** https://github.com/petersimmons1972/factvault

---

## 1. Overview

Plan 3 builds the fact-extraction pillar: archived source text moves through a deterministic regex/gazetteer pass, then an LLM extractor whose proposed facts are rejected unless their claimed character offsets into `sources.raw_text` actually contain the claimed excerpt (the anti-hallucination gate). A corroborate worker then assigns confidence based on independent source counts. The plan has 22 tasks across three new packages (`extractors/`, `embeddings/`, `vocabulary/`) plus two new workers, CLI, deploy, and CI work.

This document is the contract between the coordinator and Codex. Every Codex task is named, scoped, and bounded here. Codex receives a single task at a time, executes against the Plan 3 worktree, returns a commit. The coordinator verifies each commit before the next task is dispatched. The Plan 3 worktree is separate from the Plan 2 worktree (`~/projects/factvault-plan2/`) — do not mix them.

---

## 2. The split

### 2.1 Codex-shaped tasks (9)

| Task | Plan section | Files created | Why Codex-shaped |
|------|-------------|---------------|-------------------|
| [T1](#t1--dependency-additions) | Task 1 in fact-pipeline | `pyproject.toml` (edit) | Single-file mechanical edit, no logic |
| [T2](#t2--extractedfact-dataclass--extractor-abc) | Task 2 in fact-pipeline | `factvault/extractors/__init__.py`, `factvault/extractors/base.py`, `tests/extractors/__init__.py`, `tests/extractors/test_base.py` | Single module, dataclass + ABC, no DB |
| [T3](#t3--identifier-extractor) | Task 3 in fact-pipeline | `factvault/extractors/deterministic/__init__.py`, `factvault/extractors/deterministic/identifiers.py`, `tests/extractors/deterministic/__init__.py`, `tests/extractors/deterministic/test_identifiers.py` | Single class, regex only, no I/O |
| [T4](#t4--money-extractor) | Task 4 in fact-pipeline | `factvault/extractors/deterministic/money.py`, `tests/extractors/deterministic/test_money.py` | Single class, regex + Decimal, no I/O |
| [T5](#t5--date-extractor) | Task 5 in fact-pipeline | `factvault/extractors/deterministic/dates.py`, `tests/extractors/deterministic/test_dates.py` | Single class, regex + datetime, no I/O |
| [T6](#t6--gazetteer-entity-matcher) | Task 6 in fact-pipeline | `factvault/extractors/deterministic/gazetteer.py`, `tests/extractors/deterministic/test_gazetteer.py`, `data/gazetteer/sp500_companies.csv`, `data/gazetteer/us_politicians.csv` | Single class + two CSV fixtures, no DB |
| [T7](#t7--deterministic-runner) | Task 7 in fact-pipeline | `factvault/extractors/deterministic/runner.py`, `tests/extractors/deterministic/test_runner.py` | Composes T3-T6 in one file, no DB |
| [T11](#t11--starter-properties-yaml--loader) | Task 11 in fact-pipeline | `factvault/vocabulary/starter_properties.yaml`, `factvault/vocabulary/__init__.py` (update), `tests/vocabulary/test_loader.py` | YAML file + idempotent loader, no complex logic |
| [T12](#t12--bge-m3-embedding-wrapper) | Task 12 in fact-pipeline | `factvault/embeddings/__init__.py`, `factvault/embeddings/bge_m3.py`, `tests/embeddings/test_bge_m3.py` | Single class, thin model wrapper, no DB |

### 2.2 Coordinator-shaped tasks (13)

| Task | Why coordinator-handled |
|------|-------------------------|
| T8 (LLM extractor base) | Requires integrating OpenAI-compatible client with covered-span masking; coordinator does it directly |
| T9 (offset verification gate) | **Load-bearing anti-hallucination check** — modifies T8's `_verify_offset` with precision that determines correctness of the entire extraction pipeline |
| T10 (vocabulary resolver) | DB writes + RLS-sensitive tests using `app_engine` fixture; requires understanding existing schema |
| T13 (extract worker) | **Load-bearing** — integrates T2-T12 end-to-end; requires holistic context across all packages |
| T14 (corroborate worker) | **Load-bearing** — complex confidence-scoring formula; requires understanding statement model |
| T15 (CLI subcommand) | Extends existing CLI from Plan 2; coordinator handles cross-plan CLI wiring |
| T16 (integration test) | **Load-bearing** — full pipeline e2e; requires entire stack assembled |
| T17 (K8s CronJob for extract) | Coordinator handles together with Plan 2 K8s YAML for consistency |
| T18 (K8s Deployment for extract worker) | Coordinator handles together with Plan 2 K8s YAML |
| T19 (K8s Deployment for corroborate) | Coordinator handles together with Plan 2 K8s YAML |
| T20 (README) | Coherent narrative across all 19 prior tasks |
| T21 (CI update) | Touches existing CI workflow |
| T22 (smoke test) | Depends on T15 CLI shape |

---

## 3. Order constraints

Codex tasks have dependencies. Execute them in this order:

```
[Coordinator: verify worktree clean, pyproject deps installed]
   ↓
T1 (pyproject.toml — deps) — coordinator may do this directly
   ↓
T2 (ExtractedFact + Extractor ABC) — foundation for T3-T7
   ↓
T3, T4, T5 — identifier / money / date extractors (all depend on T2, no deps between them; run sequentially in one Codex session)
   ↓
T6 (gazetteer) — depends on T2; can run after T3-T5 or in parallel if separate Codex session
   ↓
T7 (deterministic runner) — depends on T3, T4, T5, T6 all being implemented
   ↓
T11 (starter properties YAML + loader) — no extractor deps; depends on T1 (pyyaml installed); can run any time after T1
   ↓
T12 (BGE-M3 wrapper) — no deps on extractors; depends on T1 (sentence-transformers installed)
   ↓
[Coordinator: T8, T9, T10, T13, T14, T15-T22]
```

**Parallelism rule:** Codex CAN work on T3, T4, T5, T6 simultaneously across different sessions because each is a separate file. However, two parallel Codex sessions MUST use different git worktrees to avoid cross-contamination. **For now, route all Codex tasks through ONE session, sequential.** T11 and T12 have no extractor dependencies and can be interleaved after T1.

---

## 4. The Codex prompt template

Each Codex task uses this brief format. The coordinator constructs one per task and pastes it into Codex's session input:

```
You are implementing Task <N> of the factvault fact-pipeline plan.

Repository: https://github.com/petersimmons1972/factvault
Worktree path: ~/projects/factvault-plan3/
Branch: feat/fact-pipeline
Plan: docs/superpowers/plans/2026-05-22-fact-pipeline.md (read only the Task <N> section)

IMPORTANT: This is a NEW worktree. Do NOT touch ~/projects/factvault-plan2/ (Plan 2 is separate).

Pre-flight:
    cd ~/projects/factvault-plan3
    git status -sb        # must be clean
    git branch --show-current   # must be feat/fact-pipeline
    source .venv/bin/activate   # venv exists from earlier setup
    pytest tests/ -v 2>&1 | tail -3   # all prior tests must pass

If pre-flight fails, STOP and report. Do not improvise.

Implementation:
- Read the Task <N> section of the plan
- Implement every file in the task exactly as written
- Apply the six known plan-bug patterns silently (see section 6 of this Codex Handoff doc):
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
- DO NOT touch ~/projects/factvault-plan2/ — that is Plan 2's worktree.
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

After all 9 Codex tasks complete, coordinator picks up T8-T10 and T13-T22 directly.

---

## 6. The six known plan-bug patterns

Surfaced during Plan 1 execution by implementer agents. Codex applies these silently to every task; no commentary in the commit message needed:

1. **`TIMESTAMPTZ` is NOT in `sqlalchemy.dialects.postgresql`.** Use `TIMESTAMP(timezone=True)` from `sqlalchemy` directly.
2. **`sa.UniqueConstraint` / `sa.LargeBinary` need direct imports** when `sa` alias isn't already in scope. Prefer explicit imports.
3. **psycopg refuses `:param::jsonb` / `:param::vector`** named-parameter casts. Use `CAST(:param AS jsonb)` / `CAST(:param AS vector)` instead.
4. **Postgres 15+ unique constraints default to `NULLS NOT DISTINCT`.** Tests with duplicate-NULL behavior need distinct tenant_ids or non-NULL values.
5. **The `conn` fixture is single-tenant + superuser (bypasses RLS).** Use the `app_engine` fixture for RLS-sensitive tests.
6. **RLS policies wrap `current_setting('app.current_tenant_id', true)` with `NULLIF(..., '')` before `::uuid` cast.** Already in the DB schema; Codex tasks that query the schema can rely on this guard.

---

## 7. Per-task briefs

The plan file is authoritative for code content. These briefs are short pointers + the order-of-operations metadata Codex needs.

### T1 — Dependency additions

**Plan section:** Task 1 in `docs/superpowers/plans/2026-05-22-fact-pipeline.md`
**Files created:** `pyproject.toml` (edit only — adds `sentence-transformers`, `openai`, `pyyaml` to deps; adds `pytest-httpx` to dev deps)
**Depends on:** Nothing
**Blocks:** T12 (sentence-transformers), T8 (openai), T11 (pyyaml)
**Test command:** `python -c "import sentence_transformers, openai, yaml; print('OK')"`
**Expected:** OK printed; no pytest count (dependency install, no test file)
**Commit message:** `chore(deps): add sentence-transformers, openai, pyyaml + pytest-httpx for fact-pipeline`

### T2 — ExtractedFact dataclass + Extractor ABC

**Plan section:** Task 2 in `docs/superpowers/plans/2026-05-22-fact-pipeline.md`
**Files created:** `factvault/extractors/__init__.py`, `factvault/extractors/base.py`, `tests/extractors/__init__.py`, `tests/extractors/test_base.py`
**Depends on:** T1 (venv must be active)
**Blocks:** T3, T4, T5, T6, T7 (all import `factvault.extractors.base`)
**Test command:** `pytest tests/extractors/test_base.py -v`
**Expected:** 7 passed
**Commit message:** `feat(extractors): ExtractedFact dataclass + Extractor ABC`

### T3 — Identifier extractor

**Plan section:** Task 3 in `docs/superpowers/plans/2026-05-22-fact-pipeline.md`
**Files created:** `factvault/extractors/deterministic/__init__.py`, `factvault/extractors/deterministic/identifiers.py`, `tests/extractors/deterministic/__init__.py`, `tests/extractors/deterministic/test_identifiers.py`
**Depends on:** T2 (imports `factvault.extractors.base`)
**Blocks:** T7 (runner imports IdentifierExtractor)
**Test command:** `pytest tests/extractors/deterministic/test_identifiers.py -v`
**Expected:** 9 passed
**Commit message:** `feat(extractors): deterministic identifier extractor (CIK, CUSIP, DOI, NCT, ISBN-13)`

### T4 — Money extractor

**Plan section:** Task 4 in `docs/superpowers/plans/2026-05-22-fact-pipeline.md`
**Files created:** `factvault/extractors/deterministic/money.py`, `tests/extractors/deterministic/test_money.py`
**Depends on:** T2 (imports `factvault.extractors.base`); T3 must have created `deterministic/__init__.py`
**Blocks:** T7 (runner imports MoneyExtractor)
**Test command:** `pytest tests/extractors/deterministic/test_money.py -v`
**Expected:** 11 passed
**Commit message:** `feat(extractors): USD money extractor with multiplier suffix normalisation`

### T5 — Date extractor

**Plan section:** Task 5 in `docs/superpowers/plans/2026-05-22-fact-pipeline.md`
**Files created:** `factvault/extractors/deterministic/dates.py`, `tests/extractors/deterministic/test_dates.py`
**Depends on:** T2 (imports `factvault.extractors.base`); T3 must have created `deterministic/__init__.py`
**Blocks:** T7 (runner imports DateExtractor)
**Test command:** `pytest tests/extractors/deterministic/test_dates.py -v`
**Expected:** 12 passed
**Commit message:** `feat(extractors): date extractor (ISO-8601, Month DD YYYY, DD Month YYYY)`

### T6 — Gazetteer entity matcher

**Plan section:** Task 6 in `docs/superpowers/plans/2026-05-22-fact-pipeline.md`
**Files created:** `factvault/extractors/deterministic/gazetteer.py`, `tests/extractors/deterministic/test_gazetteer.py`, `data/gazetteer/sp500_companies.csv`, `data/gazetteer/us_politicians.csv`
**Depends on:** T2 (imports `factvault.extractors.base`); T3 must have created `deterministic/__init__.py`
**Blocks:** T7 (runner imports GazetteerExtractor)
**Test command:** `pytest tests/extractors/deterministic/test_gazetteer.py -v`
**Expected:** 10 passed (two CSV-load tests will skip if CSV not present until IMPLEMENT step runs)
**Commit message:** `feat(extractors): gazetteer entity extractor + starter S&P 500 + US senator CSVs`

### T7 — Deterministic runner

**Plan section:** Task 7 in `docs/superpowers/plans/2026-05-22-fact-pipeline.md`
**Files created:** `factvault/extractors/deterministic/runner.py`, `tests/extractors/deterministic/test_runner.py`
**Depends on:** T3, T4, T5, T6 (runner imports all four extractors)
**Blocks:** T13 (coordinator's extract worker calls `DeterministicRunner`)
**Test command:** `pytest tests/extractors/deterministic/test_runner.py -v`
**Expected:** 9 passed
**Commit message:** `feat(extractors): deterministic runner composing all 4 extractors + covered-span tracking`

### T11 — Starter properties YAML + loader

**Plan section:** Task 11 in `docs/superpowers/plans/2026-05-22-fact-pipeline.md`
**Files created:** `factvault/vocabulary/starter_properties.yaml`, `factvault/vocabulary/__init__.py` (updated with `load_starter_properties` + `_insert_property`), `tests/vocabulary/test_loader.py`
**Depends on:** T1 (pyyaml in venv); T10 must have created `factvault/vocabulary/resolver.py` and `tests/vocabulary/__init__.py` (coordinator-owned T10)
**Blocks:** T13 (extract worker calls loader to seed property vocab)
**Test command:** `pytest tests/vocabulary/test_loader.py -v`
**Expected:** 5 passed
**Commit message:** `feat(vocabulary): starter property YAML (40 entries) + idempotent loader`

### T12 — BGE-M3 embedding wrapper

**Plan section:** Task 12 in `docs/superpowers/plans/2026-05-22-fact-pipeline.md`
**Files created:** `factvault/embeddings/__init__.py`, `factvault/embeddings/bge_m3.py`, `tests/embeddings/test_bge_m3.py`
**Depends on:** T1 (sentence-transformers in venv); no extractor dependencies
**Blocks:** T13 (extract worker calls `BGEEmbedder` for statement embedding)
**Test command:** `pytest tests/embeddings/test_bge_m3.py -v -m slow` (tests are marked `@pytest.mark.slow`; CI uses a small model override via env var)
**Expected:** 4 passed
**Commit message:** `feat(embeddings): BGE-M3 wrapper with lazy load + batch + dim normalisation`

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

The current 9-task Codex batch was selected explicitly to avoid these conditions.

---

## 10. Status log

- T1: queued
- T2: queued
- T3: queued
- T4: queued
- T5: queued
- T6: queued
- T7: queued
- T11: queued
- T12: queued
