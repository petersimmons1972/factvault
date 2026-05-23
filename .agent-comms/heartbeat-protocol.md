# Heartbeat Protocol Amendment

**Status**: Draft | **Author**: Claude | **Date**: 2026-05-23

## Problem

Codex's polling loop terminates when no active `agent/codex/working` claim exists on the Issues queue. Orders placed in `.agent-comms/inbox/codex/` during dormancy remain unread until external intervention. Observed: 50+ minute gaps with 4+ accumulated unread orders (2026-05-22 04:35Z–05:25Z). This amendment enforces bidirectional keep-alive to surface stalled sessions.

## Heartbeat Schema

Add `kind: heartbeat` to `schema.json` enum. New messages must conform:

```json
{
  "id": "<ulid>",
  "from": "codex",
  "to": "claude",
  "ts": "2026-05-23T01:14:00Z",
  "kind": "heartbeat",
  "refs": [],
  "body": "idle" | "claimed-#N-0%" | "claimed-#N-75%" | "session start" | "session shutdown"
}
```

Required fields: `id`, `from`, `to`, `ts`, `kind`. Body encodes agent state: idle, progress on claimed issue, or lifecycle event.

## Codex Cadence (Emit Side)

1. **Session start**: emit heartbeat with `body: "session start"` immediately upon queue loop initialization.
2. **Idle state**: emit heartbeat every 5 minutes while session runs and no issue is claimed.
3. **Active work**: emit heartbeat every 10 minutes with progress (e.g., `body: "claimed-#42-60%"`).
4. **Graceful shutdown**: emit `kind: ack` with `body: "session shutdown"` before loop exit.

## Claude Watchdog (Detect Side)

1. **Dormancy detection**: scan `.agent-comms/inbox/claude/` every 5 minutes. If no `heartbeat` from Codex within 7 minutes, mark session `DORMANT` and surface to founder with message path and last heartbeat timestamp.
2. **Stall detection**: if heartbeat present but no progress on the most recent outbound order (message age vs. Codex state) exceeds 20 minutes, send `kind: block` query to Codex.
3. **Unacked orders**: if Claude sends `nudge` or `answer` and receives no `ack` within 5 minutes of Codex's next heartbeat, escalate to `DORMANT` after 15 minutes.

## Acknowledgment Requirement

Every Claude-originated `nudge`, `answer`, or `block` must receive an `ack` from Codex within 5 minutes of his next heartbeat emission. If unacked after 15 minutes, Claude marks the session dormant and notifies the founder.

## Backward Compatibility

Messages predating this amendment are not retroactively flagged. Rule activation begins with the next confirmed Codex session start post-amendment.

## Implementation

Claude is authorized to deploy a heartbeat-monitoring sub-agent on a 5-minute recurring schedule via `mcp__infisical-personal__schedule` or similar CronJob mechanism. Codex is responsible for all emit-side logic. This amendment does not modify `README.md`, `schema.json`, or `onboarding.md`; those remain coordinator-controlled.

---

**Next**: Update schema.json to add `"heartbeat"` to kind enum, then deploy monitoring agent.
