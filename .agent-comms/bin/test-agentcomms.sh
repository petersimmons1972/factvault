#!/bin/bash
set -euo pipefail

# Test suite for agentcomms CLI
# Tests basic send/read/ack/archive workflow with --from flag inversion

AGENTCOMMS_CLI="$(cd "$(dirname "${BASH_SOURCE[0]}")/../cli" && pwd)/agentcomms"
BUS_ROOT="$(mktemp -d)"

export AGENTCOMMS_BUS="fs://$BUS_ROOT"

cleanup() {
    rm -rf "$BUS_ROOT"
}
trap cleanup EXIT

echo "Test 1: send from claude to codex"
msg1=$("$AGENTCOMMS_CLI" send --kind nudge --to codex --body "test message from claude")
echo "  Message ID: $msg1"

echo "Test 2: read codex's inbox (should have msg1)"
codex_inbox=$("$AGENTCOMMS_CLI" --agent-id codex read --raw)
if echo "$codex_inbox" | grep -q "$msg1"; then
    echo "  PASS: Message found in codex inbox"
else
    echo "  FAIL: Message not found in codex inbox"
    exit 1
fi

echo "Test 3: send from codex to claude (using --from codex)"
msg2=$("$AGENTCOMMS_CLI" send --from codex --kind nudge --to codex --body "test message from codex")
echo "  Message ID: $msg2"

echo "Test 4: read claude's inbox (should have msg2 from codex)"
claude_inbox=$("$AGENTCOMMS_CLI" --agent-id claude read --raw)
if echo "$claude_inbox" | grep -q "$msg2"; then
    echo "  PASS: Message from codex found in claude inbox"
else
    echo "  FAIL: Message from codex not found in claude inbox"
    exit 1
fi

echo "Test 5: ack msg2 as claude"
ack_id=$("$AGENTCOMMS_CLI" --agent-id claude ack "$msg2" --body "Got it")
echo "  ACK ID: $ack_id"

echo "Test 6: read codex's inbox (should have ack)"
codex_inbox_ack=$("$AGENTCOMMS_CLI" --agent-id codex read --raw)
if echo "$codex_inbox_ack" | grep -q "$ack_id"; then
    echo "  PASS: ACK found in codex inbox"
else
    echo "  FAIL: ACK not found in codex inbox"
    exit 1
fi

echo "Test 7: archive msg2"
archive_result=$("$AGENTCOMMS_CLI" archive "$msg2" --reason "tested")
echo "  Archived: $archive_result"

echo "Test 8: send from codex with ack (codex-as-sender assertion)"
msg3=$("$AGENTCOMMS_CLI" send --from codex --kind nudge --to codex --body "another test from codex")
echo "  Message ID: $msg3"
ack3=$("$AGENTCOMMS_CLI" send --from codex --kind ack --to codex --body "acknowledged by codex")
echo "  ACK ID: $ack3"

echo "Test 9: read claude's inbox (should have both msg3 and ack3)"
claude_inbox_final=$("$AGENTCOMMS_CLI" --agent-id claude read --raw)
if echo "$claude_inbox_final" | grep -q "$msg3" && echo "$claude_inbox_final" | grep -q "$ack3"; then
    echo "  PASS: Both codex messages found in claude inbox"
else
    echo "  FAIL: Not all codex messages found in claude inbox"
    exit 1
fi

echo ""
echo "All smoke tests passed!"
exit 0
