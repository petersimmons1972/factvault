# Factvault Forward Plan — 2026-05-23

## 1. Position & Momentum

**Tonight's ship:** Plans 1–2 complete (schema/migrations merged #78, Python v1 CLI in #86–#88), Plan 3 partial (workers + offset gate + spec merged #86), Plan 4 spec + scaffold landed (#89, #90 architecture spec merged). Agent-comms protocol v1.0, Python v1, and Go v2 CLI unified in #88; watchdog merged alongside. MCP server architecture v1 spec published (#90); retrieval API auth + dossiers complete (#96). **Migration status:** Postgres interface pinned, SQLite abstract backend pending extraction, assembler TDD scaffold in place, local-first architecture locked. Codex execution loop ready; next queue item unblocks six parallel Phase A–D work streams.

## 2. Codex Queue (Priority-Ordered)

| # | Title | Priority | Blocked By | Est. Effort |
|---|-------|----------|-----------|-------------|
| #91 | Phase A — Store + VectorStore interface extraction | P1 | — | 6h (foundational, unblocks #92) |
| #93 | Phase C — LLMClient + EmbeddingClient interfaces | P1 | — | 4h (independent) |
| #98 | Plan 3 deferred: extractors + LLM net/http + DB integration + clustering | P1 | — | 8h (independent scope) |
| #97 | Plan 4 impl: Assemble() + MCP server + concept doc rewrite | P1 | #98 (DB integration) | 6h (design complete; code follows #98) |
| #92 | Phase B — SQLite + sqlite-vec Store backend | P1 | #91 | 8h (implementation after interfaces) |
| #94 | Phase D — docker-compose Tier 1 single-command deploy | P1 | #93 | 5h (folds #76 Plan 5 scope; ready to ship with basic env) |
| #95 | Phase E — 5-minute Getting Started + operator docs | P1 | #94 | 3h (quick-start + operations guide) |
| #76 | Plan 5 deferred: deploy/doctor/examples scope re-eval | P2 | #94 | TBD (re-scope after Tier 1 lands) |
| #71 | Codex execution-loop docs | P2 | — | 2h (low-priority backfill) |

## 3. Founder-Input Decisions Queue

**Architecture Spec §12 — Six Key Questions:**

1. **Frontier model cost guardrails** — RECOMMENDED: Warn before 1000 paid extractions per session (configurable slider 100–10k). Prevents runaway token burn; founders can adjust per deployment risk appetite.

2. **SQLite schema sync** — RECOMMENDED: Hand-port migration files initially (no autogen). Drift risk is low; manual control lets us audit every schema change. Revisit auto-migration if hand-porting becomes >15 min per release.

3. **Embedding-space migration path** — RECOMMENDED: Defer until BGE-M3 is actually deprecated (6+ months out). Current plan: shard old embeddings to "legacy" table if model version changes; new extractions use new model. No user-facing cutover required.

4. **Tier 3 multi-tenant enforcement** — RECOMMENDED: Don't support multi-tenant in Tier 3 (single-tenant only). Simplifies isolation, avoids schema bloat. Multi-tenant moves to Tier 4 or external services if needed.

5. **Wayback + frontier interaction** — RECOMMENDED: Always submit to local Wayback regardless of LLM backend (local or frontier). Decouples availability — works even if frontier is down; consistent audit trail.

6. **Relations worker scaling** — RECOMMENDED: Sequential for v1 (single worker, ~100 ms/relation). Shard to N workers and queue if #relations/session > 1000. Revisit after first scaling deadline.

**Protocol v1.0 — Five Still-Open Questions (Coordinator Resolved):**

- Agent heartbeat interval: 5s (tunable per deployment)
- Max queue depth before backpressure: 1000 (configurable)
- Ack timeout before auto-retry: 30s (per agent class)
- Archive retention: 7 days (then drop from queue)
- Health check on startup (sync or async): Async to avoid blocking deploy

## 4. Operational Backlog (Coordinator-Owned)

- **#81** — `.golangci.yml` v2 config (linter rules refresh, trivial)
- **#82** — Go toolchain bump to 1.26.3+ (founder action; unblocks `govulncheck`)
- **thushan/olla#150** — Awaiting upstream review (olla cold-start trap mitigation); blocks Plan 3 LLM extractor
- **PR #88 "Extractors" commits investigation** — Verify no unexpected code merged in commit history (pre-merge checklist spot-check)
- **3 stashed artifacts** — Local main from earlier worker runs; drop if no ongoing context

## 5. Two-Week Ship Targets

**Week 1 (May 23–29):**
- #91 (Phase A interfaces) + #93 (LLMClient/Embedding) — Postgres abstracted; local/frontier pluggable
- #98 (Plan 3 deferred extractors + DB integration) — Deterministic extraction + source clustering engine ready
- #97 (Plan 4 Assemble) — Bundle assembler + MCP server skeleton + rewritten concept doc

**Outcome:** Factvault feature-complete for the migration. All core extraction, bundling, and retrieval paths locked. DB schema abstraction in place (Postgres still primary); ready for SQLite backend swap.

**Week 2 (May 30–Jun 5):**
- #92 (Phase B SQLite + sqlite-vec) — Swappable backend working; docker-compose can pick at deploy time
- #94 (Phase D docker-compose Tier 1) — Single `docker-compose up` spins full stack in <5 min; env-driven Postgres-or-SQLite
- #95 (Phase E docs) — Getting Started (5 min from git clone to first extraction) + operator runbook

**Outcome:** Factvault single-machine deployable. DB backend swappable at compose time. Minimal ops overhead; ready for pilot testing.

## 6. Aifleet Parallel Track

Open issues (separate cadence, non-blocking to factvault core):
- **#86, #93, #97, #98, #101, #104** — Spanning controller improvements, DNS SRV registration, vLLM cold-start, and olla upstream work
- **Handoff:** Codex picks these up when factvault queue idles or founder re-prioritizes. No coordination required; independent build systems.

## 7. Risk Register

| Risk | Likelihood | Mitigation |
|------|-----------|-----------|
| Plan 4 MCP server (go-sdk maturity) | Medium | Engine bump recommended for that ticket (#97); if SDK blocks, fallback to REST + gRPC skeleton |
| Plan 3 LLM extractor + olla cold-start | Medium | Depends on thushan/olla#150 landing; if upstream stalls >1 week, implement workaround queue |
| Plan 5 scope creep (#76 bouncing) | Medium | CLAUDE.md update just locked it; #94 (docker-compose Tier 1) is the only Plan 5 debt; re-scope after #94 |
| Sonnet 1M-context credit ceiling | Low | Keep Sonnet dispatches narrow; default Haiku for routine Codex work |
| Migration regression (Python→Go) | Low | #88 and #87 shipped with e2e tests; protocol v1 validated across both CLI versions |

---

**Generated:** 2026-05-23 | **Horizon:** Two-week sprint → pilot-ready single-machine factvault
