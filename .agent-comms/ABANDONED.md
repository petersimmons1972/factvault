# Abandoned: JSON Mailbox + Watchdog Scaffolding

**Date Archived:** 2026-05-28

## Why Abandoned

The bridge plan (`~/.claude/plans/start-with-the-bridge-flickering-torvalds.md`) standardized the Claude↔Codex coordination on **GitHub Issues + codex-handoff** rather than a custom JSON mailbox + watchdog design. The JSON approach was identified as unnecessary complexity for the decision loop.

The factvault scaffolding represented the last remaining implementation artifacts from this rejected design path. The `codex-runner.sh` was invoking a missing `watchdog.py` module, causing recurring errors in systemd logs (`python3: can't open file '...watchdog.py'`).

## What Was Moved

All scaffolding files moved to `archive/` subdirectory:

| File | Purpose | Status |
|------|---------|--------|
| `codex-runner.sh` | Systemd-triggered runner for JSON queue polling | Unused; replaced by issue-driven dispatch |
| `test-codex-runner.bats` | BATS tests for runner | Unused; tests obsolete design |
| `watchdog.log` | Error log from failed runner invocations | Evidence of failure mode |
| `codex-runner.service` | Systemd unit file (not installed) | Never activated |

## How to Restore

Files are preserved in git history under `.agent-comms/archive/`. If the JSON mailbox design is reconsidered in the future:

```bash
git log --all --full-history -- .agent-comms/archive/
git show <commit>:.agent-comms/archive/<filename> > .agent-comms/<filename>
```

## Systemd Status

No active or enabled systemd units were found for `codex-runner`. No cleanup required.
