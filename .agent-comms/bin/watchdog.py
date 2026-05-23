#!/usr/bin/env python3
"""
Claude-side heartbeat watchdog.

Monitors .agent-comms/ for:
  1. Codex heartbeat liveness (DORMANT if > 7 min absent)
  2. Unacked orders from Claude to Codex (UNACKED if > 15 min old)
  3. Stalled tickets with active working claims but no progress (STALLED if > 20 min)

Exit 0 if ACTIVE and clean; exit 1 if any DORMANT/UNACKED/STALLED.
"""

import json
import os
import sys
from datetime import datetime, timezone, timedelta
from pathlib import Path


def parse_timestamp(ts_str):
    """Parse ISO 8601 or prefixed timestamp to datetime (UTC)."""
    if not ts_str:
        return None
    # Handle ISO format with optional fractional seconds
    try:
        if '.' in ts_str:
            return datetime.fromisoformat(ts_str.replace('Z', '+00:00'))
        else:
            return datetime.fromisoformat(ts_str.replace('Z', '+00:00'))
    except (ValueError, AttributeError):
        return None


def parse_message_file(fpath):
    """Load and parse JSON message file. Return dict or None if malformed."""
    try:
        with open(fpath, 'r') as f:
            return json.load(f)
    except (json.JSONDecodeError, FileNotFoundError, IOError) as e:
        print(f"WARN: Skipping malformed message {fpath}: {e}", file=sys.stderr)
        return None


def get_comms_base():
    """Return base path for .agent-comms (respects env override for testing)."""
    return os.environ.get('AGENT_COMMS_BASE', '.agent-comms')


def scan_heartbeats(base):
    """
    Find most recent heartbeat from Codex.
    Return (ts_datetime, message_dict) or (None, None).
    """
    inbox_claude = Path(base) / 'inbox' / 'claude'
    processed = Path(base) / 'processed'

    latest_heartbeat = None
    latest_ts = None

    for inbox_dir in [inbox_claude, processed]:
        if not inbox_dir.exists():
            continue
        for fpath in inbox_dir.glob('*.json'):
            msg = parse_message_file(fpath)
            if not msg:
                continue
            if msg.get('from') == 'codex' and msg.get('kind') == 'heartbeat':
                ts = parse_timestamp(msg.get('ts'))
                if ts and (latest_ts is None or ts > latest_ts):
                    latest_ts = ts
                    latest_heartbeat = msg

    return (latest_ts, latest_heartbeat)


def scan_unacked_orders(base):
    """
    Find Claude-outbound orders (nudge/answer/block) in inbox/codex/ lacking ack.
    Return list of message IDs > 15 min old.
    """
    inbox_codex = Path(base) / 'inbox' / 'codex'
    if not inbox_codex.exists():
        return []

    now = datetime.now(timezone.utc)
    unacked = []

    # Load all messages
    all_msgs = {}
    for fpath in inbox_codex.glob('*.json'):
        msg = parse_message_file(fpath)
        if msg:
            all_msgs[msg.get('id')] = msg

    # Find Claude-originated orders that lack an ack
    for msg_id, msg in all_msgs.items():
        if msg.get('from') == 'claude' and msg.get('kind') in ['nudge', 'answer', 'block']:
            ts = parse_timestamp(msg.get('ts'))
            if ts:
                age = (now - ts).total_seconds() / 60
                # Check if there's a matching ack from Codex
                has_ack = False
                for other_id, other_msg in all_msgs.items():
                    if (other_msg.get('from') == 'codex' and
                        other_msg.get('kind') == 'ack' and
                        other_msg.get('in_reply_to') == msg_id):
                        has_ack = True
                        break
                if not has_ack and age > 15:
                    unacked.append(msg_id)

    return unacked


def scan_stalled_tickets(base):
    """
    Query GitHub for open agent/codex/working tickets.
    Check for > 20 min staleness: ticket still marked working but no recent heartbeat progress.
    Return list of ticket numbers.
    """
    stalled = []

    # Check if gh is available
    try:
        import subprocess
        result = subprocess.run(
            ['gh', 'issue', 'list', '-R', 'petersimmons1972/factvault',
             '--label', 'agent/codex/working', '--state', 'open',
             '--json', 'number,updatedAt', '--limit', '100'],
            capture_output=True, text=True, timeout=10
        )
        if result.returncode != 0:
            return stalled

        tickets = json.loads(result.stdout) if result.stdout else []
        now = datetime.now(timezone.utc)

        for ticket in tickets:
            updated_at_str = ticket.get('updatedAt')
            if updated_at_str:
                updated = parse_timestamp(updated_at_str)
                if updated:
                    age_min = (now - updated).total_seconds() / 60
                    if age_min > 20:
                        stalled.append(ticket.get('number'))
    except (ImportError, FileNotFoundError, subprocess.TimeoutExpired, json.JSONDecodeError):
        # gh not available or failed; skip stall detection
        pass

    return stalled


def main():
    base = get_comms_base()
    now = datetime.now(timezone.utc)

    last_hb_ts, last_hb_msg = scan_heartbeats(base)
    unacked_orders = scan_unacked_orders(base)
    stalled_tickets = scan_stalled_tickets(base)

    # Determine session state
    session_state = 'UNKNOWN'
    if last_hb_ts:
        age_min = (now - last_hb_ts).total_seconds() / 60
        session_state = 'DORMANT' if age_min > 7 else 'ACTIVE'
    else:
        session_state = 'DORMANT'

    # Build output
    output = {
        'timestamp': now.isoformat() + 'Z' if now.tzinfo else now.isoformat(),
        'session_state': session_state,
        'last_heartbeat': last_hb_ts.isoformat() + 'Z' if last_hb_ts and last_hb_ts.tzinfo else (last_hb_ts.isoformat() if last_hb_ts else None),
        'unacked_orders': unacked_orders,
        'stalled_tickets': stalled_tickets,
        'summary': f"{session_state}"
    }

    if unacked_orders:
        output['summary'] += f"; {len(unacked_orders)} unacked orders"
    if stalled_tickets:
        output['summary'] += f"; {len(stalled_tickets)} stalled tickets"

    print(json.dumps(output))

    # Exit code
    exit_code = 0
    if session_state != 'ACTIVE' or unacked_orders or stalled_tickets:
        exit_code = 1

    sys.exit(exit_code)


if __name__ == '__main__':
    main()
