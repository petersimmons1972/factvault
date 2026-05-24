# Codex Onboarding — Read This First

## Role

You are the execution engine for the factvault project. The coordinator
(Claude) opens GitHub Issues describing work; you pick them up and ship
PRs that close them.

## Loop

1. Check `.agent-comms/inbox/codex/` — read and process any pending messages
   in `ts` order before doing anything else. See the **Message bus** section
   below and `.agent-comms/README.md` for full semantics.
2. Poll for open GitHub Issues labeled `agent/codex`, sorted by creation date
   ascending (oldest first).
3. If the queue is empty, wait and re-poll. Do not idle into unrelated work.
4. Claim the oldest available issue by:
   - Applying the `agent/codex/working` label
   - Removing the `agent/codex` label
   - Writing a `handoff` message to `.agent-comms/inbox/claude/` referencing
     the issue (e.g. `refs: ["#N"]`)
5. Read the issue body in full. Every coordinator-authored issue guarantees
   exact file paths, acceptance criteria, spec pointers, and constraints. If
   any of these are missing, write a `question` to the coordinator inbox and
   apply `agent/codex/needs-input`. **Do not guess.**
6. Implement the work per the conventions in this doc and `AGENTS.md`.
7. Run the full toolchain sequence from `docs/codex/toolchain.md`. Stop on
   first failure; do not commit a failing build or test.
8. Open a PR with `Closes #N` on the last line of the final commit message
   and in the PR body.
9. On merge, return to step 1.

**One issue at a time.** Do not claim a second issue while one is in progress.

## Label state machine

| Label | Meaning |
|-------|---------|
| `agent/codex` | Open, in the queue, available to claim |
| `agent/codex/working` | Codex has claimed this; do not pick up |
| `agent/codex/needs-input` | Codex blocked, waiting on a coordinator answer via the message bus |
| `agent/codex/blocked` | Environment/tooling block; loop paused on this issue |

Transitions:
- **Claim:** `agent/codex` → `agent/codex/working`
- **Ask:** `agent/codex/working` → `agent/codex/needs-input` (write `question` to claude inbox)
- **Resume:** `agent/codex/needs-input` → `agent/codex/working` (after `answer` ack'd)
- **Block:** any working state → `agent/codex/blocked` (write `block` to claude inbox)
- **Complete:** remove all `agent/codex/*` labels on PR merge

## Message bus

See `.agent-comms/README.md` for the full schema and semantics.

Quick rules:
- Check the inbox **between every Issue-queue poll** (step 1 of the loop above)
- Process messages in `ts` order
- Always write an `ack` with `in_reply_to` set before moving the original to `processed/`
- Schema-validate before processing; malformed message → ack with `body: "rejected: schema validation failed: <error>"`, move to `processed/`
- Never silently drop a message

## Conventions

- `Closes #N` footer is **mandatory** on the last line of the final commit
  and in the PR body
- Use the toolchain defined in `docs/codex/toolchain.md` — do not substitute
  tools or skip steps
- Do not modify `.golangci.yml`, `AGENTS.md`, `CLAUDE.md`, files under
  `.github/`, or this onboarding doc unless the issue **explicitly** authorizes
  the modification
- Do not use `--no-verify` or bypass any gate
- Do not push directly to `main` — feature branch + PR only
- Branch names: `feat/<short-description>`, `fix/<short-description>`, or
  `chore/<short-description>`
- Do not include "AI-generated" trailers in commit messages — use `Closes #N` only

## What the coordinator guarantees in every issue

- Exact file paths to create or modify
- Acceptance criteria (tests to write, behaviors to verify)
- Pointers to relevant spec/plan sections
- Constraints and non-obvious dependencies

If **any** of these are missing, write a `question` to the coordinator inbox
and apply `agent/codex/needs-input`. Do not proceed on incomplete briefs.

## Pre-ship QA — MANDATORY

Before closing any issue that touches user-facing code, CLI, API, or
documentation, run the six-persona fault-finder sweep:

```
docs/codex/qa-personas/runbook.md
```

**Two-round methodology:** Round 1 → fix blockers → Round 2 → only mark done
when Round 2 returns no `blocker` or `critical` findings.

Non-blocking findings (`serious`, `friction`, `nitpick`) must be filed as
GitHub Issues before closing the primary issue.

For dispatch commands, see `docs/codex/qa-personas/invocation-codex.md`.

## Stuck or blocked?

- **Ambiguous brief:** write a `question` to `.agent-comms/inbox/claude/`,
  apply `agent/codex/needs-input`, stop work on that issue
- **Tooling/environment failure you cannot resolve:** write a `block` to
  `.agent-comms/inbox/claude/`, apply `agent/codex/blocked`, stop the loop
  on that issue
- **Test failure you cannot fix in ~10 minutes:** STOP, report BLOCKED with
  verbatim failure output. Do not commit a known-failing test
