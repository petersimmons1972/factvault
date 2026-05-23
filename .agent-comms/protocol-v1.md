# Claude↔Codex Communication Protocol v1.0

**Author**: Claude (Coordinator)  
**Date**: 2026-05-23  
**Status**: Authoritative Specification  
**Supersedes**: `heartbeat-protocol.md` (merged into this spec)

---

## 1. Roles & Invariants

**Claude** (Principal, v0 deployment)
- Issues orders, owns the work queue definition
- Owns `.agent-comms/bin/` (watchdog + CLI)
- Writes to `.agent-comms/inbox/codex/`
- Reads from `.agent-comms/inbox/claude/`
- Maintains audit log

**Codex** (Agent, v0 deployment)
- Executes orders, reports status and blockers
- Owns `.agent-comms/cli/` (his CLI implementation)
- Writes to `.agent-comms/inbox/claude/`
- Reads from `.agent-comms/inbox/codex/`
- Emits keep-alive signals

**Protocol generalization** (design; v1+ implementations)
- The bilateral Claude↔Codex relationship is one instance of a **principal↔agent** pattern
- Protocol supports N peers, any agent → any agent messaging (no hierarchy required)
- Future: `hermes` and other agents join as peers (see §14 — Onboarding)

**Invariants**
- Transport abstraction: message format is transport-agnostic (see §13)
- v0: Filesystem (`.agent-comms/`) is the sole bus; no hidden assumptions about liveness
- Neither agent trusts the other's claim of liveness — only fresh, properly-formatted messages count
- All messages are **immutable** once written; processing happens downstream (archive to `processed/`)
- Sequence numbers are **monotonic per sender** — gaps indicate transport loss or restart
- Message IDs are **globally unique** — re-delivery of the same ID is idempotent

---

## 2. Message Envelope (v1.0 Schema)

Every message is a JSON object with the following structure:

```json
{
  "id": "01HXYZABCDEFGHIJKLMNOPQRST",
  "proto_version": "v1.0",
  "from": "claude",
  "to": "codex",
  "ts": "2026-05-23T14:30:45.123Z",
  "seq": 42,
  "kind": "order",
  "in_reply_to": null,
  "refs": ["#67", "commit:a1b2c3d"],
  "body": "Run test suite for #67 before merge.",
  "hmac": null
}
```

**v0 transport (filesystem):** message written to file named `{ISO-8601-Z-timestamp}-{ULID}.json`

**Field Reference**

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `id` | string | YES | ULID (Crockford base32, 26 chars). Must match pattern `^[0-9A-HJKMNP-TV-Z]{26}$`. |
| `proto_version` | string | YES | Protocol version, e.g. `"v1.0"`. Mismatches trigger `nack` with reason `proto_version_mismatch`. |
| `from` | string | YES | Agent identifier. v0: `"claude"` or `"codex"`. v1+: `<agent>@<host>` (e.g., `"claude@leviathan"`, `"hermes@artemis"`). Single-host v0 allows both formats; `@<host>` is optional. |
| `to` | string \| array | YES | Recipient(s). v0: `"claude"` or `"codex"` (single string). v1+: string or array of agent identifiers. Multicast optional in v0. |
| `ts` | ISO-8601-Z | YES | RFC 3339 format, UTC, optional fractional seconds (e.g., `2026-05-23T14:30:45Z` or `2026-05-23T14:30:45.123Z`). Unix-epoch timestamps are **rejected**. |
| `seq` | integer | YES | Monotonic per sender, starting at 1. Receiver tracks `last_seen_seq[from]` and detects gaps. |
| `kind` | enum | YES | See §3 for exhaustive list. |
| `in_reply_to` | ULID \| null | NO | ID of the message this replies to. Required for `ack`, `nack`, optional for others. |
| `refs` | array[string] | YES | May be empty. Recognized: `#<N>` (issue), `pr:<N>` (PR), `commit:<sha>` (commit), `<file>:<line>` (source), `msg:<id>` (message). Opaque strings allowed. |
| `body` | string | YES | Free-form content, ≤ 8 KiB. |
| `hmac` | string \| null | NO | Reserved for v1.1+ signing (SHA256 over canonical JSON). Set to `null` for v1.0. |

**Filename Convention**
- Format: `{ISO-8601-Z-timestamp}-{ULID}.json`
- Example: `2026-05-23T14-30-45Z-01HXYZABCDEFGHIJKLMNOPQRST.json`
- **ISO-8601-Z-timestamp** uses colons replaced with hyphens for filesystem safety
- Unix-epoch prefixes (e.g., `1716468645-...`) are **rejected**

---

## 3. Message Kinds (Exhaustive)

| Kind | Direction | Purpose | Requires `in_reply_to` | Expected Response | Response Deadline |
|------|-----------|---------|----------------------|-------------------|-------------------|
| `order` | Claude → Codex | Work directive (issue claim, action, runbook execution) | NO | `ack` or `question` | 5 min after next heartbeat |
| `ack` | Either → Either | Receipt confirmation; original moves to `processed/` | YES | None | — |
| `nack` | Either → Either | Message rejected (schema invalid, version mismatch, etc.) | YES | None | — |
| `heartbeat` | Either → Either | Liveness pulse; includes session state | NO | None (unsolicited) | — |
| `claim` | Codex → Claude | Codex claims issue #N; applies `agent/codex/working` label | NO | `ack` | 5 min |
| `progress` | Codex → Claude | Status update during active work (issued in addition to heartbeat) | NO | None (unsolicited) | — |
| `handoff` | Codex → Claude | Work complete; includes commit SHA, summary, close recommendation | NO | `ack` | 5 min |
| `block` | Codex → Claude | Cannot proceed without coordinator intervention (tooling error, environment issue) | NO | `order` (resolution) | Urgent, immediate |
| `question` | Codex → Claude | Scope/spec ambiguity; Codex halts work on issue until resolved | NO | `order` or `nudge` (clarification) | Urgent, within 5 min of next heartbeat |
| `nudge` | Claude → Codex | Non-blocking redirect ("skip #N", "do #M first", "stop work on current") | NO | `ack` | 5 min after next heartbeat |
| `error` | Any agent → principal | Report failure or warning (see §16.2); does not auto-imply `block` | NO | `ack` if `severity: fatal`, else optional | 5 min for fatal |
| `suggestion` | Any → any | Propose routing/method change (see §17) | NO | `ack`, `negotiate`, or principal `decision` | 10 min |
| `negotiate` | Any → any | Multi-turn back-and-forth on task placement (see §17) | YES (threads via `in_reply_to`) | `negotiate`, `ack`, or principal `decision` | 10 min |
| `decision` | Principal → all involved | Authoritative outcome of a negotiation (see §17) | NO | `ack` from each involved agent | 5 min |
| `capability_publish` | Any agent → registry | Publish agent's capability profile (see §15) | NO | None (unsolicited) | — |
| `capability_query` | Principal → agent | Request fresh profile or filtered agent list | NO | `capability_response` | 5 min |
| `capability_response` | Agent → principal | Reply to `capability_query` | YES | None | — |
| `registry_snapshot` | Principal → all | Broadcast current registry state (v0: implicit via shared file) | NO | None (unsolicited) | — |

---

## 4. Liveness Contract

### Codex Emit Side

**Idle state** (no claimed issue)
- Emit `heartbeat` every 5 minutes
- Body: `"idle"` or free-form session state (e.g., `"queue empty, ready for work"`)

**Active work state** (claimed issue)
- Emit `heartbeat` every 10 minutes in addition to `progress` messages
- Body: `"claimed-#N-{percent}%"` (e.g., `"claimed-#67-60%"`)
- Also emit dedicated `progress` message mid-heartbeat if significant state change

**Session lifecycle**
- **Start**: emit `heartbeat` immediately with body `"session start, last_seq={N}"` (where N is highest seq seen from Claude in previous session, or 0 if new). MUST be followed (or preceded) by a `capability_publish` (see §15).
- **Graceful shutdown**: emit `ack` with body `"session shutdown, last_seq={N}"` before loop exit

**Heartbeat extension fields** (optional, recommended)
- `profile_hash` (string, sha256 hex of canonical capability profile JSON) — lets principal detect capability drift without round-trip
- `load` (object, compact) — current utilization snapshot: `{"cpu": 23, "ram": 41, "gpu": 0, "queue_depth": 0}` (percentages 0-100; gpu omitted if no GPU)

Heartbeat with extensions:
```json
{
  "id": "...", "proto_version": "v1.0", "from": "codex", "to": "claude",
  "ts": "...", "seq": 5, "kind": "heartbeat",
  "refs": [], "body": "idle",
  "profile_hash": "a1b2c3...",
  "load": {"cpu": 12, "ram": 38, "queue_depth": 0}
}
```
If principal's last-known `profile_hash` for the sender differs, principal SHOULD emit `capability_query` to refresh.

### Claude Watchdog Detect Side

**Dormancy detection** (every 5 minutes)
- Scan `.agent-comms/inbox/claude/` for Codex messages with `kind: heartbeat`
- If no heartbeat from Codex within **7 minutes** of current time ⇒ DORMANT
- Action: surface to founder with message path and timestamp of last heartbeat

**Stall detection** (during active work)
- If heartbeat present **but no `progress` message** within **20 minutes** during claimed work ⇒ STALLED
- Action: emit `block` query to Codex asking for status

**Unacked orders**
- Track all Claude-originated messages (`order`, `nudge`, `answer`) requiring `ack`
- If unacked at **15 minutes** after emission ⇒ UNACKED-ORDER
- Action: escalate to founder with order ID and timeout reason

---

## 5. Order Acknowledgment Contract

Every Claude message of kind `order`, `nudge`, or blocking coordination (`answer` with `in_reply_to: <question>`) requires Codex to respond with `ack` within **5 minutes of Codex's next scheduled heartbeat** (i.e., ≤10 minutes total from emission).

**Ack message structure**
```json
{
  "id": "<new-ulid>",
  "proto_version": "v1.0",
  "from": "codex",
  "to": "claude",
  "ts": "2026-05-23T14:35:30Z",
  "seq": 43,
  "kind": "ack",
  "in_reply_to": "01HXYZABCDEFGHIJKLMNOPQRST",
  "refs": [],
  "body": "Acknowledged; starting work on #67.",
  "hmac": null
}
```

If unacked at **15 minutes**, Claude logs the order as UNACKED-ORDER and notifies the founder with the order ID for manual intervention.

---

## 6. Idempotency & Ordering

**Per-sender sequence numbers**
- Each sender maintains a monotonic `seq` counter, starting at 1 and incrementing per message
- Receiver tracks `last_seen_seq[from]` and **SHOULD** detect gaps (⇒ potential message loss)
- On gap detection: receiver MAY emit `question` requesting resync (see §7)

**Idempotent delivery**
- The same `id` may appear multiple times (retransmit after crash, network delay)
- Receiver: if `id` already in `.agent-comms/processed/`, skip re-processing and emit `ack` if it was ack-requiring
- No duplicate side effects (orders processed twice, etc.)

**Out-of-order messages**
- Receiver **MAY** process messages immediately or buffer until seq is contiguous
- Correctness **must not depend on order** — semantics of each kind are independent
- Exception: `answer` assumes preceding `question` was already processed (causal link via `in_reply_to`)

---

## 7. Recovery & Continuity

### Codex Restart

1. Emit `heartbeat` with body `"session start, last_seq=<N>"` (N = highest seq from Claude before crash)
2. Claude watchdog detects restart from session-start heartbeat
3. Claude re-emits any unacked `order` or `nudge` with fresh `id` and incremented `seq`
4. Codex acknowledges each order with matching `in_reply_to`

### Claude Restart (Rare)

1. Replay all messages in `.agent-comms/inbox/claude/` (unread Codex messages)
2. Replay all messages in `.agent-comms/processed/` (audit trail, prior context)
3. Reconstruct state: active claims, pending orders, last heartbeat time
4. If Codex is still active (heartbeat age < 7 min), resume normal operation
5. If Codex is dormant, wake via immediate `order` or manual intervention

### Drift Detection

If either side detects a gap in `seq` or missing context:
- Emit `question` with kind `"question"`, body: `"Seq gap detected: expected <N>, saw <M>. Requesting resync."`
- Recipient responds with `order` or `nudge` clarifying state, or both sides archive problematic messages and reset counters (rare)

---

## 8. Audit Log

**Processing workflow**
1. Receiver reads message from `.agent-comms/inbox/<from>/`
2. Validates schema; if invalid, emit `nack` and move to `.agent-comms/processed/`
3. Process message (execute order, acknowledge, respond to question, etc.)
4. Move original file to `.agent-comms/processed/`, preserving filename
5. Append entry to `.agent-comms/audit.jsonl` (file-locked append)

**Audit log entry** (one line per message handled)
```json
{"ts": "2026-05-23T14:35:30Z", "msg_id": "01HXYZ...", "kind": "order", "from": "claude", "to": "codex", "action": "processed", "notes": ""}
```

**Audit actions**
- `written` — message created and written to inbox
- `read` — message read by recipient
- `processed` — message acted upon (order executed, question answered, etc.)
- `nacked` — message rejected (validation error, version mismatch)
- `archived` — moved to processed/ after handling

**File locking for audit.jsonl**
- Both agents use shared file locking (e.g., `flock` on Unix) before appending
- Prevents interleaved writes and corruption
- Timeout: 30 seconds; hard fail if lock cannot be acquired

---

## 9. CLI Surface (Both Sides)

Both Claude and Codex expose a CLI command `agentcomms` with the following interface:

```bash
# Send a message
agentcomms send --kind <KIND> --to <RECIPIENT> [--reply <MSG_ID>] [--refs <LIST>] --body <TEXT>
  # Returns: message ID (ULID) on success
  # Exit: 0 (success), 1 (validation error), 2 (FS error)

# List messages
agentcomms read [--unread] [--kind <KIND>] [--from <SENDER>] [--raw]
  # Returns: JSON array of messages (or raw JSON lines if --raw)
  # Exit: 0 (success), 2 (FS error)

# Acknowledge a message
agentcomms ack <MSG_ID> [--body <TEXT>]
  # Shorthand for: agentcomms send --kind ack --reply <MSG_ID> --to <sender-of-MSG_ID> --body <TEXT>
  # Exit: 0 (success), 1 (msg not found), 2 (FS error)

# Emit heartbeat
agentcomms heartbeat [--body <TEXT>]
  # Shorthand for: agentcomms send --kind heartbeat --to <other> --body <TEXT>
  # Exit: 0 (success), 2 (FS error)

# Archive (move to processed/)
agentcomms archive <MSG_ID> [--reason <TEXT>]
  # Moves message to processed/; appends audit row
  # Exit: 0 (success), 1 (msg not found), 2 (FS error)

# Health check
agentcomms health
  # Validates schema on all messages in inbox/ and processed/
  # Reports: last heartbeat from each agent, seq gap detection, unacked orders
  # Exit: 0 (all green), 1 (protocol violations detected), 2 (FS error)
```

**Exit codes (standard)**
- `0` — Success
- `1` — Protocol violation (schema invalid, version mismatch, message not found)
- `2` — Transport error (FS permission denied, lock timeout, etc.)

---

## 10. Boundaries & File Ownership

**Coordinator-protected** (Claude owns; Codex MUST NOT modify)
- `.agent-comms/README.md`
- `.agent-comms/schema.json`
- `.agent-comms/protocol-v1.md`
- `.agent-comms/heartbeat-protocol.md` (legacy, archived for reference)

**Codex-owned**
- `.agent-comms/cli/` (his CLI implementation)
- Messages he writes to `.agent-comms/inbox/claude/`
- No permission to read/write Claude's protected files

**Claude-owned**
- `.agent-comms/bin/` (Claude CLI + watchdog + monitoring)
- Messages she writes to `.agent-comms/inbox/codex/`
- `.agent-comms/audit.jsonl` (both write, file-locked)
- `.agent-comms/processed/` (both archive, file-locked)

---

## 11. Versioning & Forward Compatibility

**Proto version embedding**
- Every message includes `proto_version: "v1.0"`
- Recipient validates: if `proto_version` not recognized, emit `nack` with body: `"proto_version_mismatch: expected v1.0, got {version}"`
- Do not silently downgrade or upgrade; fail loudly

**v1.1 placeholder**
- Field `hmac` is reserved for SHA256-HMAC signing (shared secret TBD)
- v1.0 sets `hmac: null`
- v1.1 will populate HMAC for identity/integrity guarantee
- v1.0 recipients ignore non-null HMAC (no validation)

**Future migrations**
- New message kinds: add to enum, bump proto_version to v1.1
- Schema changes: new required fields ⇒ new version
- Breaking behavior: new version number required

---

## 12. Test Plan (End-to-End Scenarios)

Both sides' implementations must pass the following test suite:

1. **Cold start handshake**: Codex emits `heartbeat` with `"session start, last_seq=0"`. Claude detects it, state resets. ✓
2. **Missed heartbeat (dormancy)**: Codex silent for 8 minutes. Claude watchdog detects DORMANT after 7-min window. Founder notified. ✓
3. **Unacked order escalation**: Claude emits `order`. Codex never acks (e.g., crash before ack). At 15 min, Claude escalates UNACKED-ORDER. ✓
4. **Restart recovery**: Codex crashes mid-work. On restart, emits `heartbeat` with session-start. Claude replays unacked orders. Codex acks them. ✓
5. **Schema rejection**: Codex sends malformed JSON (missing `seq`). Claude emits `nack`. Message archived. Audit log records rejection. ✓
6. **Sequence gap detection**: Codex emits seq 1, 2, 5 (gap at 3, 4). Claude detects and emits `question` for resync. ✓
7. **Idempotent re-delivery**: Claude emits `order` with ID X. Network delay ⇒ Codex never receives ack. Claude retransmits order with same ID but new seq. Codex acks once; re-delivery is no-op. ✓
8. **Audit log append correctness**: Both agents emit messages; audit.jsonl grows monotonically, no corruption under concurrent appends. ✓
9. **Out-of-order resilience**: Messages arrive out of seq order. Receiver processes immediately without deadlock; semantics remain sound. ✓
10. **Progress tracking**: Codex claims issue #67. Emits `progress` at 10-min intervals; heartbeat every 10 min. Claude sees both. ✓

---

## Appendix: Refs Convention

- `#<N>` — GitHub Issue (e.g., `#67`)
- `pr:<N>` — Pull Request (e.g., `pr:42`)
- `commit:<sha>` — commit SHA, short or full (e.g., `commit:a1b2c3d` or `commit:a1b2c3def...`)
- `<file>:<line>` — source location (e.g., `internal/db/pool.go:42`)
- `msg:<id>` — message ID reference (e.g., `msg:01HXYZABCDEF...`)
- Opaque strings are allowed and tooling treats unknown forms as-is

---

## 13. Transport Abstraction (Design for Multi-Host)

**v0 Implementation (current, single host)**
- Transport: Filesystem bus at `.agent-comms/` (NFS-mountable or local)
- Message persistence: JSON files named `{ISO-8601-Z}-{ULID}.json`
- Directories: `inbox/claude/`, `inbox/codex/`, `processed/`, `audit.jsonl`
- Protocol fields: `from` and `to` are simple strings (`claude`, `codex`)

**v1+ Design (multi-host, not tonight)**
- Message format remains **identical**; only transport mechanism changes
- Agent identity upgrades to `<agent>@<host>` format (e.g., `claude@leviathan`, `hermes@artemis`)
- Candidate transport options (mutually exclusive, decision deferred):
  - **(a) HTTP+JSON POST**: Each agent runs a listener. Sender POSTs message JSON to recipient's endpoint (e.g., `http://codex-host:9876/inbox`). Registry contains `{agent@host → listener_url}` mapping.
  - **(b) NATS/MQTT**: Publish-subscribe broker on shared network. Each agent subscribes to subject `agent-comms.<agent>`. Sender publishes to recipient subject.
  - **(c) Remote object store (S3-like)**: Shared S3-compatible bucket. Message format unchanged; written to `s3://bucket/agent-comms/{date}/{ULID}.json`. Notifications via SNS or polling.

**CLI abstraction**
- Both agents expose `--bus` flag or `AGENTCOMMS_BUS` env var
- v0 default: `AGENTCOMMS_BUS=fs:///path/to/.agent-comms` or `file:///path/to/.agent-comms`
- v1 examples: `AGENTCOMMS_BUS=https://bus.example.com/`, `AGENTCOMMS_BUS=nats://nats-broker:4222`, `AGENTCOMMS_BUS=s3://bucket-name/agent-comms`
- All CLI commands (`send`, `read`, `ack`, `archive`) route through transport abstraction; command surface unchanged

**Backward compatibility**
- v0 messages with simple `from`/`to` (no `@host`) remain valid on single-host deployments
- v1+ implementations SHOULD accept both formats; v0 clients MAY omit `@host`

---

## 14. Onboarding New Agents (Design for Multi-Agent)

The current protocol is designed for bilateral Claude↔Codex messaging. It is extensible to support N peers without modification. Future onboarding of agents like `hermes` will follow this pattern:

1. **Registration**: New agent registers identity in `.agent-comms/registry.json` (TBD format) or hardcoded in both sides' config
   - Entry: `{"agent": "hermes", "host": "artemis", "role": "executor|broker|observer"}`
   - v0: no registry; agents hard-coded in config

2. **Role clarity**: Agents declare role (executor, principal, broker, observer) in registration or heartbeat body
   - Determines message routing expectations and liveness SLAs
   - Claude = principal, Codex = executor (current v0 pattern)
   - Hermes = executor (future, like Codex)

3. **Directory structure**: New agent gets `inbox/<agent>/` directory
   - Any peer can write to `inbox/hermes/`, Hermes reads from it
   - Hermes writes to `inbox/<target>/` for each message recipient

4. **Heartbeat and liveness**: New agent follows §4 liveness contract (heartbeat every 5-10 min, watchdog detects dormancy at 7 min)
   - Principal tracks all agent heartbeats in single watchdog sweep

5. **Coordination semantics**: Principal (Claude) retains queue ownership; new executor agents (`hermes`) claim work the same way Codex does
   - `claim` message → `agent/hermes/working` label (analogous to Codex)
   - Ordering, idempotency, recovery unchanged

**v0 constraint**: This section is design only. Hermes is not onboarded tonight. All v0 code remains Claude↔Codex bilateral.

---

## 15. Capability Discovery & Registry

The bus must support a **heterogeneous cluster** where workers run on different hosts with different hardware, language stacks, served models, and special skills. Principals route work by **matching capabilities to requirements** — not by assuming every agent can do every job.

### 15.1 Registry

A canonical file `.agent-comms/registry.json` is the cluster's source of truth for agent capabilities. Each top-level key is an `agent_id` (e.g., `codex@leviathan`); each value is that agent's last-published capability profile.

- v0 (filesystem bus): registry is a shared JSON file, updated atomically (write-rename) by the principal or by `capability_publish` handlers
- v1+ (network bus): registry becomes a service (HTTP endpoint, KV store, or shared object); shape unchanged

The registry is **eventually consistent** — agents publish, principal reconciles. A stale entry is not an error, but the principal SHOULD treat profiles older than 1 hour as suspect and re-query.

### 15.2 Capability Profile Schema

```json
{
  "agent_id": "codex@leviathan",
  "host": "leviathan",
  "hardware": {
    "cpu_model": "Intel Xeon E5-2680 v4",
    "ram_gb": 128,
    "gpus": [
      {"model": "NVIDIA RTX A6000", "vram_gb": 48, "count": 1}
    ]
  },
  "languages": [
    {"name": "go", "version": "1.23.4"},
    {"name": "python", "version": "3.12"},
    {"name": "rust", "version": "1.83"},
    {"name": "shell", "version": "bash-5.2"}
  ],
  "models": [
    {"name": "qwen3-32b", "role": "consume", "endpoint": "http://localhost:8000/v1"},
    {"name": "nomic-embed-text", "role": "serve", "endpoint": "http://localhost:11434"}
  ],
  "skills": ["factvault-migration", "go-implementation", "python-implementation"],
  "endpoints": [
    {"name": "agentcomms-cli", "url": "local", "protocol": "fs"}
  ],
  "load": {"cpu": 12, "ram": 38, "gpu": 0, "queue_depth": 0},
  "last_published_ts": "2026-05-23T14:30:45Z",
  "health": "ok",
  "trusted": true
}
```

**Field reference**

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `agent_id` | string | YES | Canonical identifier (`<agent>@<host>` in v1+; bare name OK in v0) |
| `host` | string | YES | Hostname (e.g., `leviathan`, `oblivion`, `precision`, `artemis`) |
| `hardware` | object | YES | `{cpu_model, ram_gb, gpus: [{model, vram_gb, count}]}` |
| `languages` | array | YES | Execution stacks with versions when meaningful |
| `models` | array | NO | LLM endpoints served OR consumed by this agent; `role: serve\|consume` |
| `skills` | array[string] | YES | Tagged free-form capabilities; principal matches these to work requirements |
| `endpoints` | array | NO | Network services exposed (`{name, url, protocol}`) |
| `load` | object | YES | Resource snapshot (percentages + `queue_depth`); same shape as heartbeat `load` |
| `last_published_ts` | ISO-8601-Z | YES | When this profile was last emitted |
| `health` | enum | YES | `ok` \| `degraded` \| `down` |
| `trusted` | bool | YES | Default `false` for newly-discovered agents; upgraded by principal or human. v0: all locally-provisioned agents on `leviathan` start `true`. |

### 15.3 Publish Cadence

Each agent publishes its profile via `capability_publish`:
1. **On session start** — immediately, before claiming any work
2. **On capability change** — model loaded/unloaded, GPU hot-added, new skill enabled
3. **Implicitly via heartbeat hash** — if `profile_hash` in heartbeat differs from principal's last-known value, principal pulls a fresh profile via `capability_query`

The principal merges incoming `capability_publish` into `.agent-comms/registry.json` and updates the `last_published_ts`.

### 15.4 Routing Logic (Principal Side)

When the principal has work to assign:
1. **Filter by skill**: read registry for agents whose `skills` include the required tag
2. **Filter by health**: keep only `health: ok`
3. **Filter by trust**: skip `trusted: false` for sensitive work (founder or principal-gated)
4. **Filter by load**: prefer agents with headroom (`queue_depth == 0`, `cpu < 80`, GPU available if work needs it)
5. **Select**:
   - v0: random among eligible (or single agent `codex` by default)
   - v1+: least-loaded, cheapest, or geographically nearest — heuristic deferred
6. **Send `order`** to selected `<agent>@<host>`

### 15.5 Discovery & Trust

When an unknown agent posts `capability_publish`:
- Registry adds the entry with `trusted: false`
- Principal does NOT route sensitive work (skill tag prefix `sensitive:` or explicit `requires_trusted: true` on the order) to untrusted agents
- Trust upgrade requires either:
  - Founder action (manual edit of registry.json, or CLI command `agentcomms trust <agent_id>`)
  - Principal upgrade after N successful handoffs (deferred to v1.1)

**v0 default**: all agents locally provisioned on `leviathan` are trusted on first publish. The `trusted: false` gate matters when remote hosts join (Hermes on `artemis`, future workers).

### 15.6 v0 Implementation Surface (Tonight)

Narrow scope — do not exceed:
- **Codex CLI**: emits one `capability_publish` on session start. Profile is best-effort: Go version (from `go version`), Python version (from `python3 --version`), GPU visible (from `nvidia-smi` if present, else `null`), skills hardcoded to `["factvault-migration", "go-implementation", "python-implementation"]`. Sets `trusted: true` because local.
- **Claude watchdog**: reads `.agent-comms/registry.json` on each scan tick (5 min). Surfaces to founder if Codex's entry is missing or `last_published_ts` is > 1 hour stale.
- **Routing**: NOT implemented. All work goes to Codex by default. Skill matching is logged for observability but does not gate assignment.
- **`capability_query` round-trip**: NOT implemented. Defer to v1.1.
- **Trust upgrades**: NOT implemented. v0 defaults sufficient.

### 15.7 Open Design Questions (Needs Founder Input)

1. **Registry write ownership**: When multiple agents publish concurrently, who is authoritative writer to `.agent-comms/registry.json`?
   - **Option A**: Each agent writes its own entry under `.agent-comms/registry/<agent_id>.json`, principal aggregates on read. Simpler, no lock contention.
   - **Option B**: Single `registry.json` file; all writers use `flock`. Matches §8 audit pattern but creates a contention point.
   - **Recommendation**: Option A for v0 (per-agent files), aggregate at read time. Switch to a service in v1+.

2. **Trust bootstrap for first remote agent**: When Hermes on `artemis` first publishes, no human is watching. Should the principal auto-trust based on a pre-shared secret (HMAC, see §11 v1.1) or always require explicit founder upgrade?
   - **Recommendation**: Require explicit founder upgrade until v1.1 HMAC ships.

3. **Capability staleness threshold**: Currently set at 1 hour. Should an agent be marked `health: degraded` if its `last_published_ts` is stale even though heartbeats are fresh? (Capability rot vs. liveness are distinct.)
   - **Recommendation**: Yes — surface as `degraded` after 1 hour stale profile, even if heartbeats are current. Logged warning, not a routing exclusion.

---

## 16. Work Queues, Errors, and Status

The cluster shares work across multiple agents and must surface failures + status visibly between them. This section defines the queue model, error reporting contract, and status streaming.

### 16.1 Shared Work Queue Model

**Canonical queue**: GitHub Issues, labeled by intended worker class (e.g., `agent/codex`, `agent/hermes`, `agent/any`). Multi-agent topology partitions the queue via **per-skill labels**:

- `skill/factvault-migration` — work requiring Codex's migration expertise
- `skill/k8s-admin` — work requiring cluster administration
- `skill/embedding-server` — work requiring an agent that serves embeddings
- (etc., extensible)

Principal matches an issue's `skill/*` labels against the registry (§15) to identify eligible agents.

**Claim ownership encoding**

When agent A claims issue #N:
1. Agent A applies label `agent/<A>/working` to issue #N via the GitHub API
2. Agent A emits a `claim` message to principal: `{kind: "claim", from: "<A>@<host>", refs: ["#N"], body: "claiming #N"}`
3. Principal updates internal state and acks the claim

**Conflict resolution**

If two agents try to claim the same issue simultaneously:
- **First `claim` message** (by `ts`, tiebreak by `id` lexicographic order) wins
- **Second `claim`** receives `nack` with `body: "already_claimed_by:<agent_id>"` and `in_reply_to: <second-claim-id>`
- Losing agent removes its `agent/<name>/working` label (if applied) and selects a different issue

**Re-queue after agent failure**

If an agent holding a claim goes DORMANT (per §4) mid-claim:
1. Principal waits a configurable **cooldown** (default **30 minutes**) past dormancy detection
2. Principal removes the `agent/<failed-agent>/working` label from the issue
3. Issue returns to the queue and becomes claimable by any matching agent
4. Principal emits `nudge` to all eligible agents announcing re-availability: `body: "re-queued: #N after <agent> dormancy"`

### 16.2 Error Message Kind

The `error` kind reports failures or warnings from agent to principal. It does NOT automatically imply a `block` — work may continue at lower severity levels.

**Required body structure** (JSON-encoded string in the `body` field, or top-level fields by convention):

```json
{
  "severity": "error",
  "code": "compile_failed",
  "summary": "Go build failed for internal/workers/archive.go",
  "detail": "internal/workers/archive.go:42:3: undefined: pgxpool.Config\n  stack trace:\n  ...",
  "context": {
    "ticket": "#74",
    "phase": "green-implementation",
    "file": "internal/workers/archive.go"
  }
}
```

**Severity ladder**

| Severity | Meaning | Agent action | Principal action |
|----------|---------|--------------|------------------|
| `info` | Notable but non-actionable event | Continue | Log only |
| `warn` | Recoverable anomaly | Continue with caution | Log; surface in dashboard |
| `error` | Operation failed but agent recovered or moved on | May continue | Log; consider re-routing or assistance |
| `fatal` | Agent cannot proceed; session may exit | Stop work, emit `block` if applicable | Ack required; re-route work; check liveness |

**Error codes (well-known, extensible)**
- `schema_load_failed` — message schema validation failed
- `tool_missing` — required tool (binary, library) not found
- `network_unreachable` — cannot reach required endpoint
- `oom` — out of memory
- `compile_failed` — build/compile failure
- `test_failed` — test suite failed (note: not always fatal; agent may iterate)
- `permission_denied` — filesystem or API permission error
- `proto_version_mismatch` — message proto version unsupported (also emitted as `nack`)

Unknown codes are allowed; principal treats them as opaque but logs and surfaces.

### 16.3 Status Streaming (Progress Extension)

The `progress` kind (defined in §3) MUST carry the following body fields during active work:

```json
{
  "ticket": "#74",
  "phase": "green-implementation",
  "percent_estimate": 60,
  "last_commit_sha": "a1b2c3d",
  "notes": "Pool init done; archive worker compile passing; running tests next."
}
```

**Field reference**

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `ticket` | string | YES | Issue reference (e.g., `#74`); MUST match a claimed issue |
| `phase` | string | YES | Free-form work phase; recommended values: `red-tests-writing`, `green-implementation`, `refactor`, `verify`, `handoff-prep` |
| `percent_estimate` | int | YES | 0-100, best-effort; not load-bearing for scheduling |
| `last_commit_sha` | string | NO | Most recent commit SHA on this work (short or full); omit if no commits yet |
| `notes` | string | NO | Free text; ≤ 2 KiB recommended |

**Cadence**: every 10 minutes during active work (in addition to heartbeats per §4).

### 16.4 Status Aggregation (Principal Side)

The principal's watchdog aggregates messages per active ticket into an in-memory view:

```text
TicketView {
  ticket_id: "#74"
  claimed_by: "codex@leviathan"
  claimed_at: ISO-8601-Z
  last_heartbeat: ISO-8601-Z
  last_progress: ISO-8601-Z
  current_phase: "green-implementation"
  percent_estimate: 60
  errors: [list of error messages, newest first]
  state: ACTIVE | STALLED | DORMANT | BLOCKED | COMPLETED
}
```

**State derivation**:
- `ACTIVE` — heartbeat fresh (< 7 min), progress fresh (< 20 min)
- `STALLED` — heartbeat fresh but no progress in 20 min (per §4)
- `DORMANT` — no heartbeat in 7 min (per §4)
- `BLOCKED` — most recent message from agent is `block`
- `COMPLETED` — `handoff` received and acked

**v0 implementation surface (tonight)**

Narrow scope:
- Watchdog detects DORMANT, UNACKED, STALLED (per §4) — already specified
- Per-ticket aggregation: NOT required tonight. Watchdog may log per-message events without building the full TicketView
- Re-queue automation: NOT implemented; cooldown logic deferred. Founder manually un-labels issues for now
- Full TicketView assembly: v1.1

### 16.5 Error Fan-Out (Future, v1.1+)

When the principal receives an `error`, it MAY rebroadcast to other agents under specific conditions:
- Other agents share the affected ticket (collaborative work, not yet supported in v0)
- Other agents share the affected capability (e.g., `error` from one Go agent might warn another Go agent of a tooling regression)

**v0 behavior**: log only. No rebroadcast. Error is visible in `audit.jsonl` and surfaced to founder if `severity: fatal`.

### 16.6 Open Design Questions (Needs Founder Input)

1. **Re-queue cooldown default**: 30 minutes was chosen as a reasonable default. Should this be configurable per-skill (e.g., a `skill/k8s-admin` task might need longer cooldown than `skill/factvault-migration`)?
   - **Recommendation**: Single global default for v0 (30 min). Per-skill overrides in v1.1.

2. **`error` body encoding**: Should the structured fields (`severity`, `code`, etc.) live at the message envelope level or stay nested inside `body` as JSON?
   - **Option A**: Top-level fields on the message envelope (requires schema bump to v1.1 or optional extension)
   - **Option B**: Encoded inside `body` as a JSON string (current draft; backward-compatible with v1.0 schema)
   - **Recommendation**: Option B for v1.0 to avoid schema churn. Promote to envelope fields in v1.1.

3. **Test-failure severity**: `test_failed` is currently severity-flexible. Should a failing test in the red phase of TDD be `info` (expected) vs. a failing test in the green phase be `error`?
   - **Recommendation**: Yes — agent uses phase context to set severity. Red-phase test failures are `info`; green-phase failures are `error`.

---

## 17. Collaborative Optimization & Capability Negotiation

Agents should reason together about optimal task placement, not just execute orders mechanically. Either side can observe an inefficiency and propose a better routing. The principal arbitrates; the outcome becomes a routing rule and a lesson (§18).

### 17.1 Negotiation Message Kinds

Defined in §3, repeated here for context:

- **`suggestion`** (any → any): proposes a routing or method change
- **`negotiate`** (any → any): multi-turn position exchange, threaded via `in_reply_to`
- **`decision`** (principal → all involved): authoritative outcome, supersedes prior thread

### 17.2 Body Schemas

**`suggestion` body** (JSON-encoded string in `body`):
```json
{
  "observation": "Codex used Sonnet ($X) to rename 200 variables in PR #82",
  "alternative": "Route bulk-rename tasks through local Qwen3-32B via olla (free)",
  "expected_benefit": "Save ~$0.40/PR on mechanical refactors; latency similar"
}
```

**`negotiate` body** (JSON-encoded string in `body`):
```json
{
  "topic": "routing:bulk-rename",
  "position": "Local Qwen3-32B is not reliable for renames touching public API surface — risk of import path miscount",
  "evidence": ["msg:01HXYZ...", "commit:a1b2c3d (where Qwen missed 3 sites)"]
}
```

**`decision` body** (JSON-encoded string in `body`):
```json
{
  "topic": "routing:bulk-rename",
  "decided": "Route mechanical renames (no public API) to local Qwen3-32B; renames touching exported identifiers stay on Sonnet",
  "rationale": "Cost savings on the easy 80%; correctness preserved on the risky 20%",
  "supersedes": ["msg:01HXYZ-suggestion...", "msg:01HXYA-negotiate..."]
}
```

A `decision` is authoritative. Once committed, both agents update behavior and the principal appends the decision to `lessons.jsonl` (§18).

### 17.3 Cost Model (Capability Profile Extension)

Each agent's capability profile (§15) gains a `cost_model` field. Values are intentionally rough heuristics, not actuarial estimates.

```json
"cost_model": {
  "paid_tokens_per_kchars": {
    "input_usd_per_kchar": 0.0008,
    "output_usd_per_kchar": 0.004,
    "model_class": "sonnet"
  },
  "free_local_compute_available": true,
  "gpu_minutes_available": 1440,
  "human_supervision_cost": "low"
}
```

| Field | Type | Notes |
|-------|------|-------|
| `paid_tokens_per_kchars` | object | Approx cost per 1000 chars in/out, and model class label |
| `free_local_compute_available` | bool | Has free local LLM inference (e.g., olla, ollama) |
| `gpu_minutes_available` | int \| null | Available GPU-minutes/day for served models; null if not a model server |
| `human_supervision_cost` | enum | `low` \| `medium` \| `high` — does this task tend to require founder intervention? |

### 17.4 Negotiation Flow

When agent A observes agent B doing something expensively:

1. **A sends `suggestion` to B** with `{observation, alternative, expected_benefit}`
2. **B responds** with one of:
   - `ack` — acknowledged, will adopt
   - `negotiate` — counter-position with evidence (threaded via `in_reply_to`)
   - Escalation: B emits `question` to principal requesting `decision`
3. If multi-turn `negotiate` exchange stalls (>3 round-trips with no convergence), either side escalates to principal via `question`
4. **Principal emits `decision`** referencing the thread; both agents `ack` the decision
5. **Decision is appended to `lessons.jsonl`** by the principal (§18)

Negotiation messages do NOT block work. The current routing remains in effect until a `decision` lands; agents may continue work under existing rules while the negotiation runs in parallel.

### 17.5 Authority

The principal has final say on cross-agent decisions. Workers cannot bind each other; only the principal's `decision` is authoritative.

Either side MAY request founder escalation by emitting `question` with body tagged `founder-escalation: true` when a negotiation reaches a meaningful tradeoff (correctness vs. cost, security vs. latency, etc.). Principal forwards founder-escalation questions immediately instead of attempting arbitration.

### 17.6 Worked Examples

**Example A — Cost suggestion**

Claude observes Codex using paid Sonnet API for a trivial text-transform task (variable renames).

```
Claude → Codex:
  kind: suggestion
  body: {
    "observation": "PR #82: 200 variable renames executed via Sonnet ($0.42 total)",
    "alternative": "Route mechanical renames through local Qwen3-32B via olla endpoint",
    "expected_benefit": "Zero marginal cost; ~similar latency on leviathan's RTX A6000"
  }

Codex → Claude:
  kind: negotiate
  in_reply_to: <suggestion-id>
  body: {
    "topic": "routing:bulk-rename",
    "position": "Acceptable for internal refactors. Risk on exported identifiers where import sites must update transitively.",
    "evidence": ["commit:9f8e7d6 (Qwen missed 3 of 47 sites)"]
  }

Claude → both (principal decision):
  kind: decision
  body: {
    "topic": "routing:bulk-rename",
    "decided": "Mechanical renames (no public API) → Qwen3-32B. Exported identifiers → Sonnet.",
    "rationale": "Split by blast radius. Cheap on the easy 80%, safe on the risky 20%.",
    "supersedes": ["msg:<suggestion-id>", "msg:<negotiate-id>"]
  }
```

**Example B — Context efficiency suggestion**

Codex observes Claude reading a 5000-line spec twice for one ticket.

```
Codex → Claude:
  kind: suggestion
  body: {
    "observation": "Claude re-read .agent-comms/protocol-v1.md (5000+ lines) twice on ticket #74 in a single session",
    "alternative": "Cache the relevant slice; pass file:line refs in subsequent prompts",
    "expected_benefit": "~30 KB token savings per repeat read; faster turnaround"
  }

Claude → Codex:
  kind: ack
  in_reply_to: <suggestion-id>
  body: "Adopted. Will use file:line refs after first read; will cache content slices for ticket scope."
```

No principal `decision` needed — Claude (as principal here) directly acks the suggestion.

---

## 18. Shared Learning Store (Lessons Log)

Agents accumulate decisions and observations over time. Without a shared log, every negotiation must be re-litigated. The lessons log gives both sides a queryable memory of past `decision` outcomes and confirmed best practices.

### 18.1 File

`.agent-comms/lessons.jsonl` — append-only JSON Lines log of agreed lessons.

- **Read access**: both agents
- **Propose access**: either agent (via `suggestion` → resolved by `decision`)
- **Commit access**: principal only (writes the entry after `decision` lands)

File-locked appends (same pattern as §8 `audit.jsonl`). Lines are immutable once written; corrections happen via `superseded_by` references, not edits.

### 18.2 Lesson Schema

One JSON object per line:

```json
{
  "id": "01HXYZ_LESSON_ULID_...",
  "ts": "2026-05-23T15:42:00Z",
  "proposed_by": "claude@leviathan",
  "confirmed_by": "claude@leviathan",
  "subject": "routing-cost",
  "lesson": "Bulk variable renames touching only internal (non-exported) identifiers route to local Qwen3-32B via olla. Renames touching exported identifiers stay on Sonnet to avoid transitive import-site miscount.",
  "evidence_refs": ["msg:01HXYZ-suggestion...", "msg:01HXYA-negotiate...", "msg:01HXYB-decision...", "commit:9f8e7d6", "#82"],
  "superseded_by": null
}
```

**Field reference**

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `id` | ULID | YES | Crockford base32, 26 chars |
| `ts` | ISO-8601-Z | YES | When the lesson was committed |
| `proposed_by` | agent_id | YES | Agent who originated the `suggestion` or surfaced the pattern |
| `confirmed_by` | agent_id | YES | Principal agent who finalized the `decision` |
| `subject` | string | YES | Short tag for grouping. Recommended values: `routing-cost`, `tool-preference`, `failure-mode`, `boundary`, `cadence`, `safety` |
| `lesson` | string | YES | One paragraph (≤ 1 KiB) capturing the rule in agent-actionable form |
| `evidence_refs` | array[string] | YES | Message IDs, commit SHAs, issue numbers; same ref grammar as §2 |
| `superseded_by` | ULID \| null | NO | If a later lesson replaces this one, ID of the replacement. Default `null`. |

### 18.3 Discovery & Use

Either agent's CLI exposes:

```bash
agentcomms lessons [--subject <TAG>] [--since <ISO-8601-Z>] [--include-superseded]
  # Returns: matching lessons as JSONL or pretty-printed
  # Exit: 0 (success), 2 (FS error)
```

Suggested usage:
- **Start of task**: agent queries `agentcomms lessons --subject routing-cost` to recall past routing decisions before re-litigating
- **Before sending `suggestion`**: agent queries lessons on the relevant subject to avoid duplicating a past `decision`
- **During code review**: principal queries `agentcomms lessons --subject failure-mode` to recall known anti-patterns

### 18.4 Supersession

Lessons evolve. When a new `decision` invalidates a prior lesson:
1. Principal appends the new lesson (new ULID, new line)
2. Principal also appends a **patch entry** updating the old lesson's `superseded_by` field
3. Since the file is append-only, the patch entry is the canonical record — readers reconcile by scanning forward and taking the latest state for each ID

Alternative for v0: a sidecar `.agent-comms/lessons-index.json` may cache the latest state. Optional.

### 18.5 v0 Implementation Surface (Tonight)

Narrow scope:
- **Codex CLI**: implements `lessons` subcommand (read + propose entries via `suggestion`). No direct write to `lessons.jsonl` — only principal commits.
- **Claude watchdog**: reads `lessons.jsonl` on each tick to surface count and most recent subject in status output.
- **File initialization**: principal creates an empty `.agent-comms/lessons.jsonl` on first watchdog start.
- **No automatic lesson generation**: lessons accrue only from real `decision` events. Tonight the file may stay empty; that's expected.

---

## 19. Access Asymmetry

Currently both agents run as the same user on `leviathan` and share filesystem, gitconfig, network egress, and secrets. That will not hold as the cluster grows. This section reserves the protocol fields needed to route correctly under asymmetric access without forcing v0 enforcement.

### 19.1 Access Scopes (Capability Profile Extension)

Each agent's capability profile (§15) gains an `access_scopes` field:

```json
"access_scopes": {
  "filesystem_roots": [
    {"path": "/home/psimmons/projects/factvault", "mode": "rw"},
    {"path": "/home/psimmons/.claude", "mode": "ro"}
  ],
  "network_egress": "all",
  "secrets_namespace": ["factvault-prod", "factvault-dev"],
  "git_credentials": [
    {"repo": "github.com/petersimmons1972/factvault", "identity": "peter.simmons.ga@gmail.com", "push": true}
  ]
}
```

| Field | Type | Notes |
|-------|------|-------|
| `filesystem_roots` | array | Paths this agent can access, with `mode: ro\|rw` |
| `network_egress` | enum or array | `"all"` or array of allowed destinations (host patterns, CIDRs) |
| `secrets_namespace` | array[string] | Infisical project slugs this agent can read |
| `git_credentials` | array | Per-repo: identity used, whether push is permitted |

### 19.2 Routing With Access Scopes

The principal MUST consider `access_scopes` when routing:

- An `order` requiring filesystem write to a path outside the agent's `filesystem_roots` → principal does not route there
- An `order` requiring a secret outside the agent's `secrets_namespace` → principal does not route there
- If the principal mis-routes (race condition, stale profile), the agent emits `nack` with `body: "insufficient_access: <reason>"` and the principal re-routes or escalates

### 19.3 Cross-Agent Collaboration Under Asymmetry

When no single agent has all required scopes, the principal mediates a multi-agent handoff:

- **Pattern**: agent A produces an artifact using its scoped secrets; agent B executes compute using its scoped GPU; A signs the result with its credentials
- **Mechanism**: principal issues sequenced `order` messages with explicit handoff refs (`refs: ["msg:<prior-order-id>", "artifact:<path-or-url>"]`)
- **Failure mode**: if B cannot consume A's artifact (path not accessible to B), B emits `error` with code `permission_denied` and the principal re-plans

### 19.4 v0 Implementation Surface (Tonight)

Narrow scope:
- Both agents share scopes on `leviathan`; no enforcement
- `access_scopes` field is populated in capability profiles "for show" — visible in `registry.json`, not yet honored as a routing gate
- `nack` reason `insufficient_access` is reserved but not yet emitted
- Real enforcement (Infisical scoping, filesystem ACLs, separate git identities) is v1.1+ work

---

**End of specification**
