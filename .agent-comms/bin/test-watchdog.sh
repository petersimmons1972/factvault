#!/bin/bash
#
# Smoke test for Claude heartbeat watchdog.
# Creates temporary .agent-comms-test/ dir with fake messages.
# Runs watchdog.py against it and verifies expected classifications.
#

set -u

TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

COMMS_TEST="$TMPDIR/.agent-comms-test"
mkdir -p "$COMMS_TEST"/{inbox/{claude,codex},processed}

cd /home/psimmons/projects/factvault

# Test 1: DORMANT (no heartbeat, > 7 min old heartbeat)
echo "Test 1: DORMANT state (no recent heartbeat)..."
HB_OLD="2026-05-23T00:00:00Z"
cat > "$COMMS_TEST/inbox/claude/old-hb.json" <<JSONEOF
{
  "id": "01KS9MTJFV42NJCJ091G1PQ00R",
  "from": "codex",
  "to": "claude",
  "ts": "$HB_OLD",
  "kind": "heartbeat",
  "refs": [],
  "body": "idle"
}
JSONEOF

OUT=$(AGENT_COMMS_BASE="$COMMS_TEST" python3 .agent-comms/bin/watchdog.py 2>/dev/null)
STATE=$(echo "$OUT" | jq -r '.session_state')
if [ "$STATE" != "DORMANT" ]; then
  echo "FAIL: Expected DORMANT, got $STATE"
  exit 1
fi
echo "PASS: Detected DORMANT state correctly"

# Test 2: ACTIVE (recent heartbeat)
echo "Test 2: ACTIVE state (recent heartbeat)..."
rm "$COMMS_TEST/inbox/claude/old-hb.json"
RECENT_HB=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
cat > "$COMMS_TEST/inbox/claude/recent-hb.json" <<JSONEOF
{
  "id": "01KS9MTJFV42NJCJ091G1PQ001",
  "from": "codex",
  "to": "claude",
  "ts": "$RECENT_HB",
  "kind": "heartbeat",
  "refs": [],
  "body": "idle"
}
JSONEOF

OUT=$(AGENT_COMMS_BASE="$COMMS_TEST" python3 .agent-comms/bin/watchdog.py 2>/dev/null)
STATE=$(echo "$OUT" | jq -r '.session_state')
if [ "$STATE" != "ACTIVE" ]; then
  echo "FAIL: Expected ACTIVE, got $STATE"
  exit 1
fi
echo "PASS: Detected ACTIVE state correctly"

# Test 3: UNACKED orders (Claude order > 15 min old, no ack from Codex)
echo "Test 3: UNACKED orders..."
CLAUDE_OLD_ORDER=$(date -u -d '20 minutes ago' +"%Y-%m-%dT%H:%M:%SZ")
cat > "$COMMS_TEST/inbox/codex/claude-order.json" <<JSONEOF
{
  "id": "01KS9MTJFV42NJCJ091G1PQ002",
  "from": "claude",
  "to": "codex",
  "ts": "$CLAUDE_OLD_ORDER",
  "kind": "nudge",
  "refs": ["factvault#99"],
  "body": "check status"
}
JSONEOF

OUT=$(AGENT_COMMS_BASE="$COMMS_TEST" python3 .agent-comms/bin/watchdog.py 2>/dev/null)
UNACKED=$(echo "$OUT" | jq -r '.unacked_orders | length')
if [ "$UNACKED" != "1" ]; then
  echo "FAIL: Expected 1 unacked order, got $UNACKED"
  exit 1
fi
echo "PASS: Detected unacked order correctly"

# Test 4: Acked order (should clear UNACKED)
echo "Test 4: Acked order (clears UNACKED)..."
cat > "$COMMS_TEST/inbox/codex/claude-order-ack.json" <<JSONEOF
{
  "id": "01KS9MTJFV42NJCJ091G1PQ003",
  "from": "codex",
  "to": "claude",
  "ts": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
  "kind": "ack",
  "refs": [],
  "in_reply_to": "01KS9MTJFV42NJCJ091G1PQ002",
  "body": "received"
}
JSONEOF

OUT=$(AGENT_COMMS_BASE="$COMMS_TEST" python3 .agent-comms/bin/watchdog.py 2>/dev/null)
UNACKED=$(echo "$OUT" | jq -r '.unacked_orders | length')
if [ "$UNACKED" != "0" ]; then
  echo "FAIL: Expected 0 unacked orders, got $UNACKED"
  exit 1
fi
echo "PASS: Acked order removed from UNACKED list"

echo ""
echo "All tests passed."
exit 0
