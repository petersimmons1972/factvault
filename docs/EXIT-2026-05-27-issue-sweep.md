# Exit Handoff - 2026-05-27 - FactVault Issue Sweep

## Final Outcome
- Full requested GitHub issue sweep completed and merged to `main`.
- Security, deployment, worker pipeline, CLI behavior, CI coverage, and docs inconsistencies were remediated.
- Branch/worktree cleanup completed.

## Durable Lessons
1. Use `gh api repos/<org>/<repo>/issues/<id>` as source of truth when `gh issue list` is stale.
2. Enforce tenant scope server-side and at DB execution boundaries (not caller-supplied).
3. Treat docs as production surface area; stale commands/schema references are operational defects.
4. No-op CLI flags are risk multipliers; fail loudly when not implemented.
5. Keep extraction and verification lifecycles decoupled so one stage cannot silently disable another.
6. Add contract/regression tests in CI for operational scripts (not only unit tests).

## Next-Operator Checklist
1. Before any new issue batch, refresh issue state via direct API reads.
2. Keep issue closure comments tied to explicit verification commands.
3. For deploy changes, verify both Compose and Kubernetes paths remain behaviorally consistent.
4. For CLI changes, update command docs in same PR.

## Repo Exit State
- Branch: `main`
- Sync target: `origin/main`
- Merge/rebase state: none
- Conflicts: none
