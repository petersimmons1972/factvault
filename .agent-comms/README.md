# Agent Message Bus

Low-latency side-channel between Claude (coordinator) and Codex (executor).
Public state lives in GitHub Issues and PRs. Tactical exchanges — clarifications,
nudges, blocks, handoffs — live here.

## Layout

```
.agent-comms/
  README.md       # this file (committed)
  schema.json     # JSON Schema for messages (committed)
  inbox/
    codex/        # Claude writes here, Codex reads (gitignored)
    claude/       # Codex writes here, Claude reads (gitignored)
  processed/      # archived messages after handling (gitignored)
```

Inboxes are git-ignored: messages are local-only and ephemeral. Both agents
currently run on the same host (`leviathan`). If that changes, this design
needs to be revisited — pushing message JSONL through git creates noise.

## Message format

One message per file, filename `{ISO8601-ts}-{ulid}.json`. The file contains
a single JSON object conforming to `schema.json`:

```json
{
  "id": "01HXYZABCDEF...",
  "from": "claude",
  "to": "codex",
  "ts": "2026-05-23T01:14:00Z",
  "kind": "answer",
  "refs": ["#67", "internal/db/pool.go:42"],
  "in_reply_to": "01HXYWPREVIOUS",
  "body": "Use pgxpool.Config, not pgx.ConnConfig. See internal/db/pool.go:42 for the pattern."
}
```

## Kinds

| Kind | Direction | Semantics |
|------|-----------|-----------|
| `question` | Codex → Claude | Issue ambiguous; need clarification before proceeding. Codex stops work on that issue and applies the `agent/codex/needs-input` label. |
| `answer` | Claude → Codex | Reply to a `question`. Codex removes `needs-input` label and resumes work. |
| `nudge` | Claude → Codex | "Skip Issue #N", "Do #M first", "Stop current work". Non-blocking redirect. |
| `block` | Codex → Claude | Environment/tooling failure Codex can't resolve. Stops the loop until the message is acked. |
| `handoff` | either | "I've claimed Issue #N." Self-applies `agent/codex/working` label on the referenced issue. |
| `ack` | either | Receipt confirmation. The original message is moved to `processed/`. |

## Codex loop integration

Between each Issue-queue poll, Codex:

1. Lists files in `.agent-comms/inbox/codex/`
2. Reads them in `ts` order
3. Processes each according to `kind`
4. Writes an `ack` (with `in_reply_to` set) to `.agent-comms/inbox/claude/`
5. Moves the original file to `.agent-comms/processed/`
6. Continues to the next Issue-queue poll

If schema validation fails on a message, Codex writes an `ack` with
`body: "rejected: schema validation failed: <error>"` and moves the
malformed file to `processed/`. Do not silently drop messages.

## Convention: refs

`refs` is an array of strings. Recognized forms:
- `#<N>` — GitHub Issue
- `pr:<N>` — Pull Request
- `commit:<sha>` — commit SHA (short or full)
- `<file>:<line>` — source location
- `msg:<id>` — another message ID

Free-form strings are allowed; tooling treats unknown forms as opaque.
