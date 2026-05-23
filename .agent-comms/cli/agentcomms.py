#!/usr/bin/env python3
from __future__ import annotations

import argparse
import datetime as dt
import fcntl
import json
import os
import random
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterable

PROTO_VERSION = "v1.0"
KIND_VALUES = {
    "ack",
    "archive",
    "block",
    "capability_publish",
    "claim",
    "decision",
    "error",
    "handoff",
    "heartbeat",
    "lessons",
    "nack",
    "negotiate",
    "nudge",
    "order",
    "progress",
    "question",
    "registry_snapshot",
    "suggestion",
}
LEGACY_KIND_VALUES = {"question", "answer", "nudge", "block", "handoff", "ack"}
WHO_VALUES = {"claude", "codex"}


def utc_now() -> dt.datetime:
    return dt.datetime.now(dt.timezone.utc)


def iso_ts(now: dt.datetime | None = None) -> str:
    ts = (now or utc_now()).astimezone(dt.timezone.utc).replace(microsecond=0)
    return ts.isoformat().replace("+00:00", "Z")


def parse_ts(value: str | None) -> dt.datetime | None:
    if not value or not isinstance(value, str):
        return None
    try:
        return dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None


def ulid() -> str:
    crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
    ms = int(utc_now().timestamp() * 1000)
    ts_chars = []
    for _ in range(10):
        ts_chars.append(crockford[ms & 31])
        ms >>= 5
    ts_part = "".join(reversed(ts_chars))
    rand_part = "".join(crockford[random.getrandbits(5)] for _ in range(16))
    return ts_part + rand_part


def json_dumps(obj: Any) -> str:
    return json.dumps(obj, indent=2, sort_keys=True) + "\n"


def bus_root(raw: str | None) -> Path:
    value = raw or os.environ.get("AGENTCOMMS_BUS") or os.environ.get("AGENT_COMMS_BASE") or "fs://.agent-comms"
    if value.startswith("fs://"):
        value = value[5:]
    elif value.startswith("file://"):
        value = value[7:]
    return Path(value).expanduser().resolve()


def current_agent_id(raw: str | None) -> str:
    return raw or os.environ.get("AGENTCOMMS_AGENT_ID") or "codex"


def peer_agent_id(agent_id: str) -> str:
    base, sep, host = agent_id.partition("@")
    if base == "codex":
        other = "claude"
    elif base == "claude":
        other = "codex"
    else:
        other = "claude"
    if sep:
        return f"{other}@{host}"
    return other


def short_agent_id(agent_id: str) -> str:
    return agent_id.split("@", 1)[0]


def ensure_dirs(root: Path) -> None:
    for rel in ("inbox/claude", "inbox/codex", "processed", "registry"):
        (root / rel).mkdir(parents=True, exist_ok=True)


def message_filename(ts: str, msg_id: str) -> str:
    safe_ts = ts.replace(":", "-")
    return f"{safe_ts}-{msg_id}.json"


def audit_path(root: Path) -> Path:
    return root / "audit.jsonl"


def lessons_path(root: Path) -> Path:
    return root / "lessons.jsonl"


def registry_path(root: Path, agent_id: str) -> Path:
    return root / "registry" / f"{agent_id}.json"


def inbox_dir(root: Path, agent_id: str) -> Path:
    return root / "inbox" / short_agent_id(agent_id)


def processed_dir(root: Path) -> Path:
    return root / "processed"


def write_atomic(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = Path(tempfile.mkstemp(prefix=f".{path.name}.", dir=str(path.parent))[1])
    try:
        tmp.write_text(content, encoding="utf-8")
        os.replace(tmp, path)
    finally:
        if tmp.exists():
            try:
                tmp.unlink()
            except FileNotFoundError:
                pass


def append_locked(path: Path, line: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as fh:
        fcntl.flock(fh, fcntl.LOCK_EX)
        fh.write(line)
        if not line.endswith("\n"):
            fh.write("\n")
        fh.flush()
        os.fsync(fh.fileno())
        fcntl.flock(fh, fcntl.LOCK_UN)


def load_json(path: Path) -> dict[str, Any] | None:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        return None
    return data if isinstance(data, dict) else None


def load_jsonl(path: Path) -> list[dict[str, Any]]:
    if not path.exists():
        return []
    out: list[dict[str, Any]] = []
    with path.open("r", encoding="utf-8") as fh:
        for raw in fh:
            raw = raw.strip()
            if not raw:
                continue
            try:
                obj = json.loads(raw)
            except json.JSONDecodeError:
                continue
            if isinstance(obj, dict):
                out.append(obj)
    return out


def iter_message_files(root: Path) -> Iterable[Path]:
    for rel in ("inbox/claude", "inbox/codex", "processed"):
        directory = root / rel
        if not directory.exists():
            continue
        for path in sorted(directory.glob("*.json")):
            yield path


def load_message(path: Path) -> dict[str, Any] | None:
    msg = load_json(path)
    if msg is None:
        return None
    msg["_file"] = str(path)
    return msg


def validate_message(msg: dict[str, Any], allow_legacy: bool = True) -> list[str]:
    errors: list[str] = []

    for key in ("id", "from", "to", "ts", "kind", "refs", "body"):
        if key not in msg:
            errors.append(f"missing required field: {key}")

    if "id" in msg:
        msg_id = msg.get("id")
        if not (isinstance(msg_id, str) and len(msg_id) == 26 and msg_id.isascii()):
            errors.append("id must be a 26-character ULID")

    from_val = msg.get("from")
    if from_val is not None and not isinstance(from_val, str):
        errors.append("from must be a string")
    elif isinstance(from_val, str):
        base = short_agent_id(from_val)
        if base not in WHO_VALUES:
            errors.append(f"invalid from: {from_val}")

    to_val = msg.get("to")
    if to_val is not None and not (isinstance(to_val, str) or isinstance(to_val, list)):
        errors.append("to must be a string or array")

    ts_val = parse_ts(msg.get("ts"))
    if msg.get("ts") is not None and ts_val is None:
        errors.append("ts must be ISO-8601 UTC")

    refs_val = msg.get("refs")
    if refs_val is not None and not isinstance(refs_val, list):
        errors.append("refs must be an array")

    body_val = msg.get("body")
    if body_val is not None and not isinstance(body_val, str):
        errors.append("body must be a string")

    kind_val = msg.get("kind")
    if kind_val is not None:
        if not isinstance(kind_val, str):
            errors.append("kind must be a string")
        else:
            allowed = KIND_VALUES
            if allow_legacy and "proto_version" not in msg:
                allowed = allowed | LEGACY_KIND_VALUES
            if kind_val not in allowed:
                errors.append(f"invalid kind: {kind_val}")

    proto = msg.get("proto_version")
    if proto is not None:
        if proto != PROTO_VERSION:
            errors.append(f"proto_version_mismatch: expected {PROTO_VERSION}, got {proto}")
        if not isinstance(msg.get("seq"), int):
            errors.append("missing required field: seq")
        elif msg["seq"] < 1:
            errors.append("seq must be >= 1")
        if msg.get("in_reply_to") is not None and not isinstance(msg.get("in_reply_to"), str):
            errors.append("in_reply_to must be a string or null")
        if msg.get("hmac") is not None and not isinstance(msg.get("hmac"), str):
            errors.append("hmac must be a string or null")

    if msg.get("body") == "" and kind_val in {"ack", "heartbeat"}:
        pass

    return errors


def load_registry(root: Path, agent_id: str) -> dict[str, Any]:
    path = registry_path(root, agent_id)
    data = load_json(path)
    if data is None:
        data = {
            "agent_id": agent_id,
            "last_sent_seq": 0,
            "last_seen_seq": {},
            "profile": None,
        }
    data.setdefault("agent_id", agent_id)
    data.setdefault("last_sent_seq", 0)
    data.setdefault("last_seen_seq", {})
    data.setdefault("profile", None)
    return data


def save_registry(root: Path, agent_id: str, registry: dict[str, Any]) -> None:
    path = registry_path(root, agent_id)
    registry["agent_id"] = agent_id
    write_atomic(path, json_dumps(registry))


def next_seq(root: Path, agent_id: str) -> int:
    registry = load_registry(root, agent_id)
    seq = int(registry.get("last_sent_seq") or 0) + 1
    registry["last_sent_seq"] = seq
    save_registry(root, agent_id, registry)
    return seq


def record_seen_seq(root: Path, sender: str, seq: int) -> None:
    registry = load_registry(root, sender)
    seen = registry.setdefault("last_seen_seq", {})
    if not isinstance(seen, dict):
        seen = {}
        registry["last_seen_seq"] = seen
    current = seen.get("seq")
    if isinstance(current, int):
        seen["seq"] = max(current, seq)
    else:
        seen["seq"] = seq
    save_registry(root, sender, registry)


def append_audit(root: Path, action: str, msg: dict[str, Any]) -> None:
    row = {
        "ts": iso_ts(),
        "msg_id": msg.get("id"),
        "kind": msg.get("kind"),
        "from": msg.get("from"),
        "to": msg.get("to"),
        "action": action,
        "notes": "",
    }
    append_locked(audit_path(root), json.dumps(row, sort_keys=True))


def send_message(
    root: Path,
    sender: str,
    recipient: str | list[str],
    kind: str,
    body: str,
    *,
    reply_to: str | None = None,
    refs: list[str] | None = None,
    seq: int | None = None,
    extra: dict[str, Any] | None = None,
) -> dict[str, Any]:
    ensure_dirs(root)
    msg = {
        "id": ulid(),
        "proto_version": PROTO_VERSION,
        "from": sender,
        "to": recipient,
        "ts": iso_ts(),
        "seq": seq if seq is not None else next_seq(root, sender),
        "kind": kind,
        "in_reply_to": reply_to,
        "refs": refs or [],
        "body": body,
        "hmac": None,
    }
    if extra:
        msg.update(extra)

    errors = validate_message(msg, allow_legacy=False)
    if errors:
        raise ValueError("; ".join(errors))

    recipients = recipient if isinstance(recipient, list) else [recipient]
    out_path: Path | None = None
    for target in recipients:
        target_dir = inbox_dir(root, target)
        target_dir.mkdir(parents=True, exist_ok=True)
        out_path = target_dir / message_filename(msg["ts"], msg["id"])
        write_atomic(out_path, json_dumps(msg))

    append_audit(root, "written", msg)
    return msg


def send_ack_for(root: Path, sender: str, target_msg: dict[str, Any], body: str = "Acknowledged.") -> dict[str, Any]:
    target = target_msg.get("from")
    if not isinstance(target, str):
        raise ValueError("target message missing sender")
    return send_message(
        root,
        sender=sender,
        recipient=target,
        kind="ack",
        body=body,
        reply_to=target_msg.get("id"),
        refs=[f"msg:{target_msg.get('id')}"],
    )


def find_message(root: Path, msg_id: str) -> tuple[Path, dict[str, Any]] | None:
    for path in iter_message_files(root):
        msg = load_message(path)
        if msg and msg.get("id") == msg_id:
            return path, msg
    return None


def resolve_repo(explicit: str | None) -> str:
    if explicit:
        return explicit
    env = os.environ.get("AGENTCOMMS_REPO")
    if env:
        return env
    result = subprocess.run(
        ["gh", "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner"],
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        raise RuntimeError("unable to resolve repo; set AGENTCOMMS_REPO or pass --repo")
    return result.stdout.strip()


def gh_issue_edit(repo: str, issue: int, *, add: list[str] | None = None, remove: list[str] | None = None) -> None:
    cmd = ["gh", "issue", "edit", str(issue), "-R", repo]
    for label in add or []:
        cmd += ["--add-label", label]
    for label in remove or []:
        cmd += ["--remove-label", label]
    result = subprocess.run(cmd, capture_output=True, text=True, check=False)
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or result.stdout.strip() or "gh issue edit failed")


def cmd_send(args: argparse.Namespace) -> int:
    root = bus_root(args.bus)
    sender = args.from_sender or current_agent_id(args.agent_id)
    recipient = args.to

    # Invert routing: if --from codex, message lands in recipient's inbox
    # and the "from" field is codex, but it actually goes to their inbox
    if sender != "claude":
        recipient = peer_agent_id(sender)

    refs = args.refs or []
    try:
        msg = send_message(
            root,
            sender=sender,
            recipient=recipient,
            kind=args.kind,
            body=args.body,
            reply_to=args.reply,
            refs=refs,
        )
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    print(msg["id"])
    return 0


def cmd_read(args: argparse.Namespace) -> int:
    root = bus_root(args.bus)
    agent = current_agent_id(args.agent_id)
    ensure_dirs(root)
    messages: list[dict[str, Any]] = []
    for path in sorted(inbox_dir(root, agent).glob("*.json")):
        msg = load_message(path)
        if not msg:
            continue
        if args.kind and msg.get("kind") != args.kind:
            continue
        if args.from_ and short_agent_id(str(msg.get("from", ""))) != args.from_:
            continue
        messages.append(msg)
    if args.raw:
        for message in messages:
            print(json.dumps(message, sort_keys=True))
    else:
        print(json_dumps(messages), end="")
    return 0


def cmd_ack(args: argparse.Namespace) -> int:
    root = bus_root(args.bus)
    sender = args.from_sender or current_agent_id(args.agent_id)
    found = find_message(root, args.msg_id)
    if not found:
        print("message not found", file=sys.stderr)
        return 1
    _, target = found
    try:
        # When --from codex, reply goes to the peer (claude)
        recipient = target.get("from")
        if sender != "claude":
            recipient = peer_agent_id(sender)

        msg = send_message(
            root,
            sender=sender,
            recipient=recipient,
            kind="ack",
            body=args.body,
            reply_to=args.msg_id,
            refs=[f"msg:{args.msg_id}"],
        )
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    print(msg["id"])
    return 0


def cmd_heartbeat(args: argparse.Namespace) -> int:
    root = bus_root(args.bus)
    sender = args.from_sender or current_agent_id(args.agent_id)
    recipient = peer_agent_id(sender)
    body = args.body or "idle"
    try:
        msg = send_message(root, sender=sender, recipient=recipient, kind="heartbeat", body=body)
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    print(msg["id"])
    return 0


def cmd_archive(args: argparse.Namespace) -> int:
    root = bus_root(args.bus)
    found = find_message(root, args.msg_id)
    if not found:
        print("message not found", file=sys.stderr)
        return 1
    src_path, msg = found
    dst = processed_dir(root) / src_path.name
    dst.parent.mkdir(parents=True, exist_ok=True)
    shutil.move(str(src_path), str(dst))
    append_audit(root, "archived", msg)
    if args.reason:
        append_locked(audit_path(root), json.dumps({
            "ts": iso_ts(),
            "msg_id": msg.get("id"),
            "kind": msg.get("kind"),
            "from": msg.get("from"),
            "to": msg.get("to"),
            "action": "archived_reason",
            "notes": args.reason,
        }, sort_keys=True))
    print(args.msg_id)
    return 0


def message_violations(root: Path) -> list[str]:
    problems: list[str] = []
    for path in iter_message_files(root):
        msg = load_message(path)
        if not msg:
            problems.append(f"{path}: malformed json")
            continue
        errs = validate_message(msg, allow_legacy=True)
        if errs:
            problems.append(f"{path}: " + "; ".join(errs))
    return problems


def detect_seq_gaps(messages: list[dict[str, Any]]) -> list[str]:
    by_sender: dict[str, list[dict[str, Any]]] = {}
    for msg in messages:
        sender = msg.get("from")
        if not isinstance(sender, str) or not isinstance(msg.get("seq"), int):
            continue
        if "proto_version" not in msg:
            continue
        by_sender.setdefault(sender, []).append(msg)

    gaps: list[str] = []
    for sender, msgs in by_sender.items():
        msgs.sort(key=lambda m: int(m["seq"]))
        expected = 1
        for msg in msgs:
            seq = int(msg["seq"])
            if seq < expected:
                expected = seq
            if seq > expected:
                gaps.append(f"{sender}: expected seq {expected}, saw {seq}")
            expected = seq + 1
    return gaps


def detect_unacked_orders(messages: list[dict[str, Any]]) -> list[str]:
    unacked: list[str] = []
    by_id = {m.get("id"): m for m in messages if isinstance(m.get("id"), str)}
    acked = {m.get("in_reply_to") for m in messages if m.get("kind") == "ack"}
    now = utc_now()
    for msg in messages:
        if msg.get("from") != "claude":
            continue
        if msg.get("kind") not in {"order", "nudge", "answer"}:
            continue
        if msg.get("id") in acked:
            continue
        ts = parse_ts(msg.get("ts"))
        if ts and (now - ts) > dt.timedelta(minutes=15):
            unacked.append(str(msg.get("id")))
    return unacked


def latest_heartbeat(messages: list[dict[str, Any]], who: str) -> dict[str, Any] | None:
    latest: dict[str, Any] | None = None
    latest_ts: dt.datetime | None = None
    for msg in messages:
        if short_agent_id(str(msg.get("from", ""))) != who:
            continue
        if msg.get("kind") != "heartbeat":
            continue
        ts = parse_ts(msg.get("ts"))
        if ts and (latest_ts is None or ts > latest_ts):
            latest_ts = ts
            latest = msg
    return latest


def cmd_health(args: argparse.Namespace) -> int:
    root = bus_root(args.bus)
    ensure_dirs(root)
    messages = [m for m in (load_message(p) for p in iter_message_files(root)) if m]
    violations = message_violations(root)
    gaps = detect_seq_gaps(messages)
    unacked = detect_unacked_orders(messages)
    status = {
        "ok": not (violations or gaps or unacked),
        "violations": violations,
        "seq_gaps": gaps,
        "unacked_orders": unacked,
        "last_heartbeat": {
            "codex": latest_heartbeat(messages, "codex"),
            "claude": latest_heartbeat(messages, "claude"),
        },
        "message_count": len(messages),
    }
    print(json_dumps(status), end="")
    return 0 if status["ok"] else 1


def cmd_claim(args: argparse.Namespace) -> int:
    root = bus_root(args.bus)
    sender = current_agent_id(args.agent_id)
    issue = args.issue
    repo = resolve_repo(args.repo)
    try:
        gh_issue_edit(repo, issue, add=["agent/codex/working"], remove=["agent/codex"])
    except Exception as exc:
        print(str(exc), file=sys.stderr)
        return 2
    try:
        msg = send_message(
            root,
            sender=sender,
            recipient=peer_agent_id(sender),
            kind="claim",
            body=f"claiming #{issue}",
            refs=[f"#{issue}"],
        )
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    print(msg["id"])
    return 0


def cmd_handoff(args: argparse.Namespace) -> int:
    root = bus_root(args.bus)
    sender = current_agent_id(args.agent_id)
    issue = args.issue
    repo = resolve_repo(args.repo)
    try:
        gh_issue_edit(repo, issue, remove=["agent/codex/working"])
    except Exception as exc:
        print(str(exc), file=sys.stderr)
        return 2
    try:
        msg = send_message(
            root,
            sender=sender,
            recipient=peer_agent_id(sender),
            kind="handoff",
            body=f"completed #{issue} at {args.commit}",
            refs=[f"#{issue}", f"commit:{args.commit}"],
        )
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    print(msg["id"])
    return 0


def cmd_block(args: argparse.Namespace) -> int:
    root = bus_root(args.bus)
    sender = current_agent_id(args.agent_id)
    try:
        msg = send_message(
            root,
            sender=sender,
            recipient=peer_agent_id(sender),
            kind="block",
            body=args.reason,
            refs=args.refs or [],
        )
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    print(msg["id"])
    return 0


def cmd_question(args: argparse.Namespace) -> int:
    root = bus_root(args.bus)
    sender = current_agent_id(args.agent_id)
    try:
        msg = send_message(
            root,
            sender=sender,
            recipient=peer_agent_id(sender),
            kind="question",
            body=args.body,
            refs=args.refs or [],
        )
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    print(msg["id"])
    return 0


def gather_capability_profile(agent_id: str) -> dict[str, Any]:
    host = os.uname().nodename
    go_version = subprocess.run(["go", "version"], capture_output=True, text=True, check=False)
    py_version = subprocess.run(["python3", "--version"], capture_output=True, text=True, check=False)
    nvidia = subprocess.run(["nvidia-smi", "--query-gpu=name", "--format=csv,noheader"], capture_output=True, text=True, check=False)

    gpus = []
    if nvidia.returncode == 0 and nvidia.stdout.strip():
        for line in nvidia.stdout.splitlines():
            line = line.strip()
            if line:
                gpus.append({"model": line, "vram_gb": None, "count": 1})

    return {
        "agent_id": agent_id,
        "host": host,
        "hardware": {
            "cpu_model": os.uname().machine,
            "ram_gb": None,
            "gpus": gpus,
        },
        "languages": [
            {"name": "go", "version": go_version.stdout.strip() or go_version.stderr.strip() or None},
            {"name": "python", "version": py_version.stdout.strip() or py_version.stderr.strip() or None},
        ],
        "models": [],
        "skills": ["factvault-migration", "go-implementation", "python-implementation"],
        "endpoints": [{"name": "agentcomms-cli", "url": "local", "protocol": "fs"}],
        "load": {"cpu": 0, "ram": 0, "gpu": 0, "queue_depth": 0},
        "last_published_ts": iso_ts(),
        "health": "ok",
        "trusted": True,
    }


def cmd_capability_publish(args: argparse.Namespace) -> int:
    root = bus_root(args.bus)
    sender = current_agent_id(args.agent_id)
    profile = gather_capability_profile(sender)
    registry = load_registry(root, sender)
    registry["profile"] = profile
    registry["last_sent_seq"] = int(registry.get("last_sent_seq") or 0)
    save_registry(root, sender, registry)
    try:
        msg = send_message(
            root,
            sender=sender,
            recipient=peer_agent_id(sender),
            kind="capability_publish",
            body=json.dumps(profile, sort_keys=True),
            refs=[],
            extra={"profile_hash": args.profile_hash} if args.profile_hash else None,
        )
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    print(msg["id"])
    return 0


def cmd_lessons(args: argparse.Namespace) -> int:
    root = bus_root(args.bus)
    entries = load_jsonl(lessons_path(root))
    since = parse_ts(args.since) if args.since else None
    out: list[dict[str, Any]] = []
    for entry in entries:
        if args.subject and entry.get("subject") != args.subject:
            continue
        if since:
            ts = parse_ts(entry.get("ts"))
            if not ts or ts < since:
                continue
        if not args.include_superseded and entry.get("superseded_by"):
            continue
        out.append(entry)
    print(json_dumps(out), end="")
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="agentcomms")
    parser.add_argument("--bus", help="Filesystem bus location, e.g. fs:///home/.../.agent-comms")
    parser.add_argument("--agent-id", help="Agent identity for this CLI instance")
    parser.add_argument("--repo", help="GitHub repository owner/name, defaults to gh repo view")
    sub = parser.add_subparsers(dest="cmd", required=True)

    send = sub.add_parser("send")
    send.add_argument("--kind", required=True, choices=sorted(KIND_VALUES))
    send.add_argument("--to", required=True)
    send.add_argument("--from", dest="from_sender", default=os.environ.get("AGENTCOMMS_FROM", "claude"), choices=["claude", "codex"], help="Sender agent (default: claude or AGENTCOMMS_FROM env)")
    send.add_argument("--reply")
    send.add_argument("--refs", nargs="*")
    send.add_argument("--body", required=True)
    send.set_defaults(func=cmd_send)

    read = sub.add_parser("read")
    read.add_argument("--unread", action="store_true")
    read.add_argument("--kind", choices=sorted(KIND_VALUES))
    read.add_argument("--from", dest="from_", help="Filter by sender")
    read.add_argument("--raw", action="store_true")
    read.set_defaults(func=cmd_read)

    read_inbox = sub.add_parser("read-inbox")
    read_inbox.add_argument("--unread", action="store_true")
    read_inbox.add_argument("--kind", choices=sorted(KIND_VALUES))
    read_inbox.add_argument("--from", dest="from_", help="Filter by sender")
    read_inbox.add_argument("--raw", action="store_true")
    read_inbox.set_defaults(func=cmd_read)

    ack = sub.add_parser("ack")
    ack.add_argument("msg_id")
    ack.add_argument("--from", dest="from_sender", default=os.environ.get("AGENTCOMMS_FROM", "claude"), choices=["claude", "codex"], help="Sender agent (default: claude or AGENTCOMMS_FROM env)")
    ack.add_argument("--body", default="Acknowledged.")
    ack.set_defaults(func=cmd_ack)

    heartbeat = sub.add_parser("heartbeat")
    heartbeat.add_argument("--from", dest="from_sender", default=os.environ.get("AGENTCOMMS_FROM", "claude"), choices=["claude", "codex"], help="Sender agent (default: claude or AGENTCOMMS_FROM env)")
    heartbeat.add_argument("--body", default="idle")
    heartbeat.set_defaults(func=cmd_heartbeat)

    archive = sub.add_parser("archive")
    archive.add_argument("msg_id")
    archive.add_argument("--reason")
    archive.set_defaults(func=cmd_archive)

    health = sub.add_parser("health")
    health.set_defaults(func=cmd_health)

    claim = sub.add_parser("claim")
    claim.add_argument("issue", type=int)
    claim.set_defaults(func=cmd_claim)

    handoff = sub.add_parser("handoff")
    handoff.add_argument("issue", type=int)
    handoff.add_argument("commit")
    handoff.set_defaults(func=cmd_handoff)

    block = sub.add_parser("block")
    block.add_argument("reason")
    block.add_argument("--refs", nargs="*")
    block.set_defaults(func=cmd_block)

    question = sub.add_parser("question")
    question.add_argument("body")
    question.add_argument("--refs", nargs="*")
    question.set_defaults(func=cmd_question)

    capability = sub.add_parser("capability_publish")
    capability.add_argument("--profile-hash")
    capability.set_defaults(func=cmd_capability_publish)

    lessons = sub.add_parser("lessons")
    lessons.add_argument("--subject")
    lessons.add_argument("--since")
    lessons.add_argument("--include-superseded", action="store_true")
    lessons.set_defaults(func=cmd_lessons)

    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        return int(args.func(args))
    except BrokenPipeError:
        return 0


if __name__ == "__main__":
    raise SystemExit(main())
