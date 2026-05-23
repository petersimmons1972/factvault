#!/usr/bin/env bash
# Smoke test for the agentcomms Go v2 CLI.
# Exercises: send → read → ack → archive, atomic-write tmp ignore,
# dead-letter routing, health output.
#
# Exit 0 on success, nonzero on any failure.
set -euo pipefail

BIN="${AGENTCOMMS_BIN:-./bin/agentcomms}"
ROOT="$(mktemp -d -t agentcomms-smoke.XXXXXX)"
trap 'rm -rf "$ROOT"' EXIT

say() { printf '[smoke] %s\n' "$*"; }
fail() { printf '[smoke] FAIL: %s\n' "$*" >&2; exit 1; }

if [[ ! -x "$BIN" ]]; then
  say "building $BIN"
  mkdir -p "$(dirname "$BIN")"
  go build -o "$BIN" ./cmd/agentcomms
fi

# 1. send: claude → codex
say "send claude→codex"
SEND_ID=$("$BIN" --root "$ROOT" send --from claude --to codex --kind nudge --body "smoke test 1")
[[ ${#SEND_ID} -eq 26 ]] || fail "send returned non-ULID: $SEND_ID"

# 2. read: codex inbox should have 1 message
say "read codex inbox"
COUNT=$("$BIN" --root "$ROOT" read --inbox codex | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')
[[ "$COUNT" == "1" ]] || fail "expected 1 message in codex inbox, got $COUNT"

# 3. send: codex → claude (verify direction routing)
say "send codex→claude"
CODEX_ID=$("$BIN" --root "$ROOT" send --from codex --to claude --kind ack --body "smoke test 2" --refs "msg:$SEND_ID")
FROM_FIELD=$("$BIN" --root "$ROOT" read --inbox claude | python3 -c 'import json,sys;print(json.load(sys.stdin)[0]["from"])')
[[ "$FROM_FIELD" == "codex" ]] || fail "expected from=codex, got $FROM_FIELD"

# 4. ack: codex acks the original claude→codex message
say "ack original message"
ACK_ID=$("$BIN" --root "$ROOT" ack "$SEND_ID" --from codex --body "got it")
[[ ${#ACK_ID} -eq 26 ]] || fail "ack returned non-ULID: $ACK_ID"
# Original should now be in processed/
ls "$ROOT/processed" | grep -q "$SEND_ID" || fail "ack did not archive original to processed/"
# Ack message should land in claude inbox
ls "$ROOT/inbox/claude" | grep -q "$ACK_ID" || fail "ack message did not land in claude inbox"

# 5. archive: directly archive the ack
say "archive ack message"
"$BIN" --root "$ROOT" archive "$ACK_ID" --reason "smoke cleanup"
ls "$ROOT/processed" | grep -q "$ACK_ID" || fail "archive did not move file to processed/"

# 6. atomic-write: place a .tmp file directly; read must ignore it
say "atomic write — readers ignore .tmp"
TMP_FILE="$ROOT/inbox/codex/2026-05-23T01:00:00Z-01HXYZABCDEFGHJKMNPQRSTVWX.json.tmp"
echo '{"id":"01HXYZABCDEFGHJKMNPQRSTVWX","from":"claude","to":"codex","ts":"2026-05-23T01:00:00Z","kind":"nudge","refs":[],"body":"partial"}' > "$TMP_FILE"
COUNT=$("$BIN" --root "$ROOT" read --inbox codex | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')
[[ "$COUNT" == "0" ]] || fail "read should ignore .tmp file but got $COUNT messages (expected 0)"

# 7. dead-letter: write malformed JSON; read routes it to dead-letter/
say "dead-letter — malformed message"
BAD_FILE="$ROOT/inbox/codex/2026-05-23T01:00:00Z-01HXYZABCDEFGHJKMNPQRSTVWY.json"
echo '{not valid json' > "$BAD_FILE"
"$BIN" --root "$ROOT" read --inbox codex > /dev/null
ls "$ROOT/dead-letter" | grep -q "01HXYZABCDEFGHJKMNPQRSTVWY" || fail "malformed file not in dead-letter/"
[[ -f "$ROOT/audit/events.jsonl" ]] || fail "audit log missing"
grep -q "dead_letter" "$ROOT/audit/events.jsonl" || fail "audit log missing dead_letter event"
grep -q '"kind":"block"' "$ROOT/audit/events.jsonl" || fail "audit log missing block kind"

# 8. health: schema_valid:true after we drain the dead-letter dir
say "health — JSON output"
rm -rf "$ROOT/dead-letter"/*
HEALTH_JSON=$("$BIN" --root "$ROOT" health)
echo "$HEALTH_JSON"
echo "$HEALTH_JSON" | python3 -c 'import json,sys;d=json.load(sys.stdin);assert d["schema_valid"] is True,"schema_valid not true";assert "queue_depth" in d,"missing queue_depth";print("health JSON OK")'

say "ALL SMOKE TESTS PASSED"
