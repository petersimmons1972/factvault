from __future__ import annotations

import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch


MODULE_PATH = Path(__file__).resolve().parents[1] / "agentcomms.py"
WRAPPER_PATH = Path(__file__).resolve().parents[1] / "agentcomms"


def load_module():
    spec = importlib.util.spec_from_file_location("agentcomms", MODULE_PATH)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def make_args(**kwargs):
    defaults = {
        "bus": None,
        "agent_id": None,
        "repo": None,
    }
    defaults.update(kwargs)
    return SimpleNamespace(**defaults)


def read_json(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def test_wrapper_is_executable():
    assert WRAPPER_PATH.exists()
    assert WRAPPER_PATH.read_text(encoding="utf-8").startswith("#!/usr/bin/env bash")
    assert WRAPPER_PATH.stat().st_mode & 0o111


def test_send_read_and_ack_round_trip(tmp_path):
    mod = load_module()
    bus = f"fs://{tmp_path}"
    sender = "claude"
    recipient = "codex"

    msg = mod.send_message(
        mod.bus_root(bus),
        sender=sender,
        recipient=recipient,
        kind="order",
        body="do the thing",
        refs=["#84"],
    )

    inbox_file = next((tmp_path / "inbox" / recipient).glob("*.json"))
    stored = read_json(inbox_file)
    assert stored["id"] == msg["id"]
    assert stored["proto_version"] == "v1.0"
    assert stored["from"] == sender
    assert stored["to"] == recipient
    assert stored["kind"] == "order"
    assert stored["seq"] == 1
    assert stored["refs"] == ["#84"]

    out = mod.cmd_read(make_args(bus=bus, agent_id=recipient, kind=None, from_=None, raw=False))
    assert out == 0

    readback = mod.cmd_read(make_args(bus=bus, agent_id=recipient, kind="order", from_=sender, raw=True))
    assert readback == 0

    ack_rc = mod.cmd_ack(make_args(bus=bus, agent_id=recipient, msg_id=msg["id"], body="Ack"))
    assert ack_rc == 0

    ack_file = next((tmp_path / "inbox" / sender).glob("*.json"))
    ack_msg = read_json(ack_file)
    assert ack_msg["kind"] == "ack"
    assert ack_msg["in_reply_to"] == msg["id"]
    assert ack_msg["to"] == sender

    nack_rc = mod.cmd_nack(make_args(bus=bus, agent_id=recipient, msg_id=msg["id"], body="No"))
    assert nack_rc == 0
    replies = sorted((tmp_path / "inbox" / sender).glob("*.json"))
    nack_msg = read_json(replies[-1])
    assert nack_msg["kind"] == "nack"
    assert nack_msg["in_reply_to"] == msg["id"]


def test_heartbeat_claim_and_handoff_flow(tmp_path):
    mod = load_module()
    bus = f"fs://{tmp_path}"

    rc = mod.cmd_heartbeat(make_args(bus=bus, agent_id="codex", body="alive"))
    assert rc == 0
    heartbeat = next((tmp_path / "inbox" / "claude").glob("*.json"))
    heartbeat_msg = read_json(heartbeat)
    assert heartbeat_msg["kind"] == "heartbeat"
    assert heartbeat_msg["from"] == "codex"
    assert heartbeat_msg["to"] == "claude"

    with patch.object(mod, "gh_issue_edit") as gh_edit, patch.object(mod, "send_message") as send_msg:
        send_msg.return_value = {"id": "01TESTMESSAGE00000000000000"}
        rc = mod.cmd_claim(make_args(bus=bus, agent_id="codex", issue=84, repo="owner/repo"))
        assert rc == 0
        gh_edit.assert_called_once_with("owner/repo", 84, add=["agent/codex/working"], remove=["agent/codex"])
        send_msg.assert_called_once()

    with patch.object(mod, "gh_issue_edit") as gh_edit, patch.object(mod, "send_message") as send_msg:
        send_msg.return_value = {"id": "01TESTMESSAGE00000000000001"}
        rc = mod.cmd_handoff(make_args(bus=bus, agent_id="codex", issue=84, commit="abc123", repo="owner/repo"))
        assert rc == 0
        gh_edit.assert_called_once_with("owner/repo", 84, remove=["agent/codex/working"])
        send_msg.assert_called_once()

    rc = mod.cmd_progress(make_args(bus=bus, agent_id="codex", body="half done", refs="#84"))
    assert rc == 0
    progress_files = list((tmp_path / "inbox" / "claude").glob("*.json"))
    assert any(read_json(path)["kind"] == "progress" for path in progress_files)


def test_health_reports_unacked_orders_and_latest_heartbeat(tmp_path):
    mod = load_module()
    bus = f"fs://{tmp_path}"
    root = mod.bus_root(bus)
    mod.ensure_dirs(root)

    order = {
        "id": "01J00000000000000000000000",
        "proto_version": "v1.0",
        "from": "claude",
        "to": "codex",
        "ts": "2026-05-23T00:00:00Z",
        "seq": 1,
        "kind": "order",
        "in_reply_to": None,
        "refs": [],
        "body": "work",
        "hmac": None,
    }
    heartbeat = {
        "id": "01J00000000000000000000001",
        "proto_version": "v1.0",
        "from": "codex",
        "to": "claude",
        "ts": "2026-05-23T01:00:00Z",
        "seq": 1,
        "kind": "heartbeat",
        "in_reply_to": None,
        "refs": [],
        "body": "alive",
        "hmac": None,
    }
    (tmp_path / "inbox" / "codex" / "order.json").write_text(json.dumps(order), encoding="utf-8")
    (tmp_path / "inbox" / "claude" / "heartbeat.json").write_text(json.dumps(heartbeat), encoding="utf-8")

    with patch.object(mod, "utc_now", return_value=mod.dt.datetime(2026, 5, 23, 0, 20, tzinfo=mod.dt.timezone.utc)):
        rc = mod.cmd_health(make_args(bus=bus))
    assert rc == 1


def test_capability_publish_persists_registry(tmp_path):
    mod = load_module()
    bus = f"fs://{tmp_path}"
    profile = {
        "agent_id": "codex",
        "host": "testhost",
        "hardware": {},
        "languages": [],
        "models": [],
        "skills": [],
        "endpoints": [],
        "load": {},
        "last_published_ts": "2026-05-23T00:00:00Z",
        "health": "ok",
        "trusted": True,
    }

    with patch.object(mod, "gather_capability_profile", return_value=profile):
        rc = mod.cmd_capability_publish(make_args(bus=bus, agent_id="codex", profile_hash="abc"))

    assert rc == 0
    registry = read_json(tmp_path / "registry" / "codex.json")
    assert registry["profile"] == profile
    assert registry["agent_id"] == "codex"
    capability_msg = next((tmp_path / "inbox" / "claude").glob("*.json"))
    msg = read_json(capability_msg)
    assert msg["kind"] == "capability_publish"
    assert msg["profile_hash"] == "abc"


def test_lessons_propose_get_and_list(tmp_path, capsys):
    mod = load_module()
    bus = f"fs://{tmp_path}"

    rc = mod.cmd_lessons(make_args(bus=bus, action="propose", subject="protocol", body="keep it stable", since="2026-05-23T00:00:00Z"))
    assert rc == 0
    out = capsys.readouterr().out
    assert "keep it stable" in out

    rc = mod.cmd_lessons(make_args(bus=bus, action="get", subject="protocol"))
    assert rc == 0
    out = capsys.readouterr().out
    entries = json.loads(out)
    assert len(entries) == 1
    assert entries[0]["subject"] == "protocol"

    rc = mod.cmd_lessons(make_args(bus=bus, action="list", include_superseded=False))
    assert rc == 0
    out = capsys.readouterr().out
    entries = json.loads(out)
    assert len(entries) == 1
