# Exit Handoff - 2026-05-27 - FactVault Issue Sweep

## Outcome
- Completed full issue sweep requested for `petersimmons1972/factvault/issues`.
- All issues in the sweep were closed and linked to implementation comments.
- PR #153 merged to `main`.

## High-Value Lessons (Wisdom)
1. Trust `gh api repos/<org>/<repo>/issues/<id>` for issue truth when `gh issue list` lags.
2. Tenant isolation is safer when enforced at execution boundaries (MCP server-side tenant binding + worker `tenant_context` transactions), not via caller input.
3. Deployment safety requires role separation by default: runtime app role (`app_user`) and no broad JWT private-key exposure.
4. Pipeline status coupling matters: extraction and verification states must not disable each other.
5. Security validation belongs in both ingress and execution paths (URL SSRF guard at collect and verify).
6. CLI flags that are not implemented must fail loudly; silent no-ops create operational drift.
7. CI should run behavior tests (Bats/contract tests), not only unit tests.
8. Docs must be treated as part of runtime correctness; stale commands/schemas are production bugs.

## What Was Stabilized
- P0/P1/P2 fixes across auth, deploy, worker pipeline, CLI behavior, and docs.
- Added/updated tests to lock regressions.
- Added Kubernetes migration gate and corrected worker tenant wiring.

## Operator Playbook Going Forward
1. When closing issue batches, comment evidence + verification command before close.
2. Re-check issue state with direct API reads after close operations.
3. Keep docs and CLI behavior in lockstep; if command semantics change, patch docs in same PR.
4. Prefer exact ID validation (ULID) for mailbox/archive operations.

## Exit State
- Branch: `main`
- Sync: `main` == `origin/main`
- Worktrees: old FactVault worktrees removed during cleanup
- Local workspace: clean
