from __future__ import annotations

import argparse
import datetime as dt
import fcntl
import json
import os
import platform
import re
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any, Iterable

PROTO_VERSION = "v1.0"
KIND_VALUES = {
    "order",
    "ack",
    "nack",
    "heartbeat",
    "claim",
    "progress",
    "handoff",
    "block",
    "question",
    "nudge",
    "error",
    "suggestion",
    "negotiate",
    "decision",
    "capability_publish",
    "capability_query",
    "capability_response",
    "registry_snapshot",
    "lessons",
}
LEGACY_KIND_VALUES = {"answer"}
WHO_VALUES = {"claude", "codex"}
ULID_RE = re.compile(r"^[0-9A-HJKMNP-TV-Z]{26}$")
ISO_RE = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$")


class Message:
    def __init__(
        self,
        *,
        id: str,
        proto_version: str,
        sender: str,
        recipient: str,
        ts: str,
        seq: int,
        kind: str,
        refs: list[str],
        body: str,
        in_reply_to: str | None = None,
        hmac: str | None = None,
        profile_hash: str | None = None,
        payload: dict[str, Any] | None = None,
    ) -> None:
        self.id = id
        self.proto_version = proto_version
        self.sender = sender
        self.recipient = recipient
        self.ts = ts
        self.seq = seq
        self.kind = kind
        self.refs = refs
        self.body = body
        self.in_reply_to = in_reply_to
        self.hmac = hmac
        self.profile_hash = profile_hash
        self.payload = payload

    def to_dict(self) -> dict[str, Any]:
        msg = {
            "id": self.id,
            "proto_version": self.proto_version,
            "from": self.sender,
            "to": self.recipient,
            "ts": self.ts,
            "seq": self.seq,
            "kind": self.kind,
            "refs": self.refs,
            "body": self.body,
            "in_reply_to": self.in_reply_to,
            "hmac": self.hmac,
        }
        if self.profile_hash is not None:
            msg["profile_hash"] = self.profile_hash
        if self.payload is not None:
            msg["payload"] = self.payload
        return msg


def utc_now() -> dt.datetime:
    return dt.datetime.now(dt.timezone.utc)


def iso_ts_z() -> str:
    return utc_now().isoformat(timespec="milliseconds").replace("+00:00", "Z")


def iso_ts_filename() -> str:
    return utc_now().strftime("%Y-%m-%dT%H-%M-%SZ")


def gen_ulid() -> str:
    alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
    ts_ms = int(utc_now().timestamp() * 1000)
    rand = os.urandom(10)
    value = (ts_ms << 80) | int.from_bytes(rand, "big")
    chars: list[str] = []
    for _ in range(26):
        chars.append(alphabet[value & 31])
        value >>= 5
    return "".join(reversed(chars))


def bus_root(bus: str | None = None) -> Path:
    if bus:
        if bus.startswith("fs://"):
            return Path(bus.removeprefix("fs://")).expanduser().resolve()
        if bus.startswith("file://"):
            return Path(bus.removeprefix("file://")).expanduser().resolve()
        return Path(bus).expanduser().resolve()
    return Path(__file__).resolve().parents[1]


def ensure_dirs(root: Path) -> None:
    for rel in [
        "inbox/claude",
        "inbox/codex",
        "processed",
        "registry",
        "audit",
    ]:
        (root / rel).mkdir(parents=True, exist_ok=True)


def current_agent_id(args: Any) -> str:
    return getattr(args, "agent_id", None) or os.environ.get("AGENTCOMMS_AGENT_ID", "claude")


def peer_agent_id(agent_id: str) -> str:
    return "codex" if agent_id == "claude" else "claude"


def short_agent_id(agent_id: str) -> str:
    return agent_id.split("/", 1)[-1]


def inbox_dir(root: Path, agent_id: str) -> Path:
    return root / "inbox" / short_agent_id(agent_id)


def processed_dir(root: Path) -> Path:
    return root / "processed"


def audit_path(root: Path) -> Path:
    return root / "audit.jsonl"


def lessons_path(root: Path) -> Path:
    return root / "lessons.json"


def registry_path(root: Path, agent_id: str) -> Path:
    return root / "registry" / f"{short_agent_id(agent_id)}.json"


def write_atomic(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = None, None
    try:
        fd, tmp = tempfile.mkstemp(prefix=path.name + ".", dir=str(path.parent))
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            fh.write(content)
            fh.flush()
            os.fsync(fh.fileno())
        os.replace(tmp, path)
    finally:
        if tmp and os.path.exists(tmp):
            try:
                os.unlink(tmp)
            except FileNotFoundError:
                pass


def append_locked(path: Path, line: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "a", encoding="utf-8") as fh:
        fcntl.flock(fh.fileno(), fcntl.LOCK_EX)
        try:
            fh.write(line)
            if not line.endswith("\n"):
                fh.write("\n")
            fh.flush()
            os.fsync(fh.fileno())
        finally:
            fcntl.flock(fh.fileno(), fcntl.LOCK_UN)


def load_json(path: Path) -> dict[str, Any] | list[Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def iter_message_files(directory: Path) -> Iterable[Path]:
    if not directory.exists():
        return []
    return sorted(p for p in directory.iterdir() if p.suffix == ".json" and p.is_file())


def validate_kind(kind: str) -> bool:
    return kind in KIND_VALUES or kind in LEGACY_KIND_VALUES


def validate_message(msg: dict[str, Any]) -> tuple[bool, str]:
    required = ["id", "proto_version", "from", "to", "ts", "seq", "kind", "refs", "body"]
    for field in required:
        if field not in msg:
            return False, f"missing required field: {field}"
    if not ULID_RE.match(str(msg.get("id", ""))):
        return False, f"invalid id format: {msg.get('id')}"
    if msg.get("proto_version") != PROTO_VERSION:
        return False, f"proto_version mismatch: expected {PROTO_VERSION}, got {msg.get('proto_version')}"
    if msg.get("from") not in WHO_VALUES:
        return False, f"invalid from: {msg.get('from')}"
    if msg.get("to") not in WHO_VALUES:
        return False, f"invalid to: {msg.get('to')}"
    if not ISO_RE.match(str(msg.get("ts", ""))):
        return False, f"invalid ts format: {msg.get('ts')}"
    if not isinstance(msg.get("seq"), int) or msg["seq"] < 1:
        return False, "seq must be >= 1"
    if not validate_kind(str(msg.get("kind"))):
        return False, f"invalid kind: {msg.get('kind')}"
    if not isinstance(msg.get("refs"), list):
        return False, "refs must be an array"
    if not isinstance(msg.get("body"), str):
        return False, "body must be a string"
    if msg.get("in_reply_to") is not None and not ULID_RE.match(str(msg.get("in_reply_to"))):
        return False, f"invalid in_reply_to format: {msg.get('in_reply_to')}"
    if msg.get("hmac") is not None and not isinstance(msg.get("hmac"), str):
        return False, "hmac must be a string or null"
    return True, ""


def find_message(bus: str, msg_id: str) -> Path | None:
    root = bus_root(bus)
    for sub in [root / "inbox" / "claude", root / "inbox" / "codex", root / "processed"]:
        if not sub.exists():
            continue
        for path in sub.iterdir():
            if path.is_file() and msg_id in path.name:
                return path
    return None


def gh_issue_edit(repo: str, issue: int, add: list[str] | None = None, remove: list[str] | None = None) -> None:
    cmd = ["gh", "issue", "edit", str(issue), "-R", repo]
    for label in add or []:
        cmd.extend(["--add-label", label])
    for label in remove or []:
        cmd.extend(["--remove-label", label])
    subprocess.run(cmd, check=True, capture_output=True, text=True)


def send_message(
    root: Path,
    *,
    sender: str,
    recipient: str,
    kind: str,
    body: str,
    refs: list[str] | None = None,
    reply: str | None = None,
    seq: int = 1,
    extra: dict[str, Any] | None = None,
) -> dict[str, Any]:
    msg = Message(
        id=gen_ulid(),
        proto_version=PROTO_VERSION,
        sender=sender,
        recipient=recipient,
        ts=iso_ts_z(),
        seq=seq,
        kind=kind,
        refs=refs or [],
        body=body,
        in_reply_to=reply,
    )
    payload = msg.to_dict()
    if extra:
        payload.update(extra)
    ok, err = validate_message(payload)
    if not ok:
        raise ValueError(err)

    ensure_dirs(root)
    filename = f"{iso_ts_filename()}-{payload['id']}.json"
    path = inbox_dir(root, recipient) / filename
    write_atomic(path, json.dumps(payload, ensure_ascii=False))
    append_locked(
        audit_path(root),
        json.dumps(
            {
                "ts": iso_ts_z(),
                "msg_id": payload["id"],
                "kind": payload["kind"],
                "from": payload["from"],
                "to": payload["to"],
                "action": "written",
                "notes": "",
            }
        ),
    )
    return payload


def read_messages(
    root: Path,
    *,
    inbox_name: str | None = None,
    kind: str | None = None,
    from_agent: str | None = None,
) -> list[dict[str, Any]]:
    inboxes = [inbox_name] if inbox_name else ["claude", "codex"]
    messages: list[dict[str, Any]] = []
    for inbox in inboxes:
        for path in iter_message_files(root / "inbox" / inbox):
            try:
                msg = load_json(path)
            except Exception:
                continue
            if kind and msg.get("kind") != kind:
                continue
            if from_agent and msg.get("from") != from_agent:
                continue
            messages.append(msg)
    return messages


def cmd_send(args) -> int:
    root = bus_root(args.bus)
    sender = getattr(args, "from_sender", None) or current_agent_id(args)
    recipient = args.to
    if sender not in WHO_VALUES:
        raise ValueError(f"invalid sender: {sender}")
    if recipient not in WHO_VALUES:
        raise ValueError(f"invalid recipient: {recipient}")
    payload = send_message(
        root,
        sender=sender,
        recipient=recipient,
        kind=args.kind,
        body=args.body,
        refs=[r for r in (args.refs.split(",") if args.refs else []) if r],
        reply=args.reply,
    )
    print(payload["id"])
    return 0


def cmd_read(args) -> int:
    root = bus_root(args.bus)
    inbox = getattr(args, "inbox", None) or current_agent_id(args)
    messages = read_messages(root, inbox_name=short_agent_id(inbox), kind=getattr(args, "kind", None), from_agent=getattr(args, "from_", None))
    if getattr(args, "raw", False):
        for msg in messages:
            print(json.dumps(msg))
    else:
        print(json.dumps(messages, indent=2))
    return 0


def cmd_ack(args) -> int:
    root = bus_root(args.bus)
    orig_file = find_message(args.bus, args.msg_id)
    if not orig_file:
        print(json.dumps({"error": f"message not found: {args.msg_id}"}), file=sys.stderr)
        return 1
    orig_msg = load_json(orig_file)
    sender = getattr(args, "from_sender", None) or current_agent_id(args)
    recipient = orig_msg.get("from")
    ack = send_message(
        root,
        sender=sender,
        recipient=recipient,
        kind="ack",
        body=args.body or "Acknowledged.",
        refs=[f"msg:{args.msg_id}"],
        reply=args.msg_id,
    )
    return 0


def cmd_nack(args) -> int:
    root = bus_root(args.bus)
    orig_file = find_message(args.bus, args.msg_id)
    if not orig_file:
        print(json.dumps({"error": f"message not found: {args.msg_id}"}), file=sys.stderr)
        return 1
    orig_msg = load_json(orig_file)
    sender = getattr(args, "from_sender", None) or current_agent_id(args)
    send_message(
        root,
        sender=sender,
        recipient=orig_msg.get("from"),
        kind="nack",
        body=args.body or "Rejected.",
        refs=[f"msg:{args.msg_id}"],
        reply=args.msg_id,
    )
    return 0


def cmd_heartbeat(args) -> int:
    root = bus_root(args.bus)
    sender = current_agent_id(args)
    recipient = peer_agent_id(sender)
    send_message(
        root,
        sender=sender,
        recipient=recipient,
        kind="heartbeat",
        body=args.body or "heartbeat",
        refs=[],
    )
    return 0


def cmd_progress(args) -> int:
    root = bus_root(args.bus)
    refs = [r for r in (args.refs.split(",") if getattr(args, "refs", None) else []) if r]
    send_message(
        root,
        sender=current_agent_id(args),
        recipient="claude",
        kind="progress",
        body=args.body,
        refs=refs,
    )
    return 0


def cmd_archive(args) -> int:
    root = bus_root(args.bus)
    msg_file = find_message(args.bus, args.msg_id)
    if not msg_file:
        print(json.dumps({"error": f"message not found: {args.msg_id}"}), file=sys.stderr)
        return 1
    dest = processed_dir(root) / msg_file.name
    dest.parent.mkdir(parents=True, exist_ok=True)
    msg_file.rename(dest)
    append_locked(
        audit_path(root),
        json.dumps(
            {
                "ts": iso_ts_z(),
                "msg_id": args.msg_id,
                "kind": "archive",
                "from": current_agent_id(args),
                "to": "system",
                "action": "archived",
                "notes": getattr(args, "reason", "") or "",
            }
        ),
    )
    return 0


def cmd_health(args) -> int:
    root = bus_root(args.bus)
    ensure_dirs(root)
    schema_valid = (root / "schema.json").exists()
    latest_hb = None
    heartbeats = read_messages(root, kind="heartbeat", from_agent="codex", inbox_name="claude")
    if heartbeats:
        latest_hb = heartbeats[-1]
    unacked_orders = []
    orders = read_messages(root, kind="order", from_agent="claude", inbox_name="codex")
    acks = read_messages(root, kind="ack")
    acked_ids = {ack.get("in_reply_to") for ack in acks}
    for order in orders:
        if order.get("id") not in acked_ids:
            unacked_orders.append(order.get("id"))
    dormant = True
    if latest_hb:
        hb_ts = dt.datetime.fromisoformat(latest_hb["ts"].replace("Z", "+00:00"))
        dormant = (utc_now() - hb_ts).total_seconds() > 7 * 60
    findings = {
        "schema_valid": schema_valid,
        "watchdog_installed": (root / "bin" / "watchdog.py").exists(),
        "last_heartbeat": latest_hb.get("ts") if latest_hb else None,
        "dormant": dormant,
        "unacked_orders": unacked_orders,
        "errors": [],
    }
    print(json.dumps(findings))
    return 1 if (not schema_valid or dormant or unacked_orders) else 0


def gh_issue_list(repo: str, label: str) -> list[dict[str, Any]]:
    result = subprocess.run(
        [
            "gh",
            "issue",
            "list",
            "-R",
            repo,
            "--label",
            label,
            "--state",
            "open",
            "--json",
            "number,title,labels,updatedAt",
            "--limit",
            "100",
        ],
        capture_output=True,
        text=True,
        check=True,
    )
    return json.loads(result.stdout or "[]")


def gather_capability_profile(args) -> dict[str, Any]:
    profile = {
        "agent_id": current_agent_id(args),
        "host": platform.node(),
        "hardware": {
            "machine": platform.machine(),
            "platform": platform.platform(),
        },
        "languages": [],
        "models": [],
        "skills": [],
        "endpoints": [],
        "load": {},
        "last_published_ts": iso_ts_z(),
        "health": "ok",
        "trusted": True,
    }
    return profile


def cmd_claim(args) -> int:
    repo = args.repo
    gh_issue_edit(repo, int(args.issue), add=["agent/codex/working"], remove=["agent/codex"])
    root = bus_root(args.bus)
    send_message(
        root,
        sender=current_agent_id(args),
        recipient="claude",
        kind="claim",
        body=f"claimed issue #{args.issue}",
        refs=[f"#{args.issue}"],
    )
    return 0


def cmd_handoff(args) -> int:
    repo = args.repo
    gh_issue_edit(repo, int(args.issue), remove=["agent/codex/working"])
    root = bus_root(args.bus)
    send_message(
        root,
        sender=current_agent_id(args),
        recipient="claude",
        kind="handoff",
        body=f"handoff issue #{args.issue} commit {args.commit}",
        refs=[f"#{args.issue}", f"commit:{args.commit}"],
    )
    return 0


def cmd_block(args) -> int:
    root = bus_root(args.bus)
    send_message(
        root,
        sender=current_agent_id(args),
        recipient="claude",
        kind="block",
        body=args.body,
        refs=[f"code:{args.code}", f"severity:{args.severity}"],
    )
    return 0


def cmd_question(args) -> int:
    root = bus_root(args.bus)
    send_message(
        root,
        sender=current_agent_id(args),
        recipient="claude",
        kind="question",
        body=args.body,
        refs=[],
    )
    return 0


def cmd_capability_publish(args) -> int:
    root = bus_root(args.bus)
    profile = gather_capability_profile(args)
    payload = {
        "agent_id": current_agent_id(args),
        "profile_hash": args.profile_hash,
        "profile": profile,
    }
    write_atomic(registry_path(root, current_agent_id(args)), json.dumps(payload, indent=2))
    send_message(
        root,
        sender=current_agent_id(args),
        recipient="claude",
        kind="capability_publish",
        body="capability registry updated",
        refs=[],
        extra={"profile_hash": args.profile_hash},
    )
    return 0


def lessons_load(root: Path) -> list[dict[str, Any]]:
    path = lessons_path(root)
    if not path.exists():
        return []
    data = load_json(path)
    return data if isinstance(data, list) else []


def lessons_save(root: Path, entries: list[dict[str, Any]]) -> None:
    write_atomic(lessons_path(root), json.dumps(entries, indent=2))


def cmd_lessons(args) -> int:
    root = bus_root(args.bus)
    ensure_dirs(root)
    action = getattr(args, "action", None)
    if action == "list":
        entries = lessons_load(root)
        if not getattr(args, "include_superseded", False):
            entries = [e for e in entries if not e.get("superseded_by")]
        print(json.dumps(entries, indent=2))
        return 0
    if action == "get":
        subject = args.subject
        entries = [e for e in lessons_load(root) if e.get("subject") == subject]
        print(json.dumps(entries, indent=2))
        return 0
    if action == "propose":
        entries = lessons_load(root)
        entry = {
            "subject": args.subject,
            "body": args.body,
            "since": args.since or iso_ts_z(),
            "superseded_by": None,
        }
        entries.append(entry)
        lessons_save(root, entries)
        print(json.dumps(entry, indent=2))
        return 0
    raise ValueError("unknown lessons action")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Agent communications CLI")
    parser.add_argument("--bus", dest="bus", default=None, help="Filesystem bus location, e.g. fs:///home/.../.agent-comms")
    parser.add_argument("--agent-id", dest="agent_id", default=os.environ.get("AGENTCOMMS_AGENT_ID", "claude"))
    parser.add_argument("--repo", dest="repo", default=None, help="GitHub repository owner/name, defaults to gh repo view")
    sub = parser.add_subparsers(dest="command", required=True)

    p = sub.add_parser("send")
    p.add_argument("--kind", required=True)
    p.add_argument("--to", required=True)
    p.add_argument("--from", dest="from_sender", default=os.environ.get("AGENTCOMMS_FROM", "claude"))
    p.add_argument("--reply")
    p.add_argument("--refs")
    p.add_argument("--body", required=True)
    p.set_defaults(func=cmd_send)

    p = sub.add_parser("read")
    p.add_argument("--unread", action="store_true")
    p.add_argument("--kind")
    p.add_argument("--from", dest="from_", help="Filter by sender")
    p.add_argument("--inbox", choices=["claude", "codex"])
    p.add_argument("--raw", action="store_true")
    p.set_defaults(func=cmd_read)

    p = sub.add_parser("ack")
    p.add_argument("msg_id")
    p.add_argument("--from", dest="from_sender", default=os.environ.get("AGENTCOMMS_FROM", "claude"))
    p.add_argument("--body", default="Acknowledged.")
    p.set_defaults(func=cmd_ack)

    p = sub.add_parser("nack")
    p.add_argument("msg_id")
    p.add_argument("--from", dest="from_sender", default=os.environ.get("AGENTCOMMS_FROM", "claude"))
    p.add_argument("--body", default="Rejected.")
    p.set_defaults(func=cmd_nack)

    p = sub.add_parser("heartbeat")
    p.add_argument("--from", dest="from_sender", default=os.environ.get("AGENTCOMMS_FROM", "claude"))
    p.add_argument("--body", default="heartbeat")
    p.set_defaults(func=cmd_heartbeat)

    p = sub.add_parser("progress")
    p.add_argument("--body", required=True)
    p.add_argument("--refs")
    p.set_defaults(func=cmd_progress)

    p = sub.add_parser("archive")
    p.add_argument("msg_id")
    p.add_argument("--reason", default="")
    p.set_defaults(func=cmd_archive)

    p = sub.add_parser("health")
    p.set_defaults(func=cmd_health)

    p = sub.add_parser("claim")
    p.add_argument("issue")
    p.set_defaults(func=cmd_claim)

    p = sub.add_parser("handoff")
    p.add_argument("issue")
    p.add_argument("--commit", required=True)
    p.set_defaults(func=cmd_handoff)

    p = sub.add_parser("block")
    p.add_argument("--code", required=True)
    p.add_argument("--severity", default="error")
    p.add_argument("--body", required=True)
    p.set_defaults(func=cmd_block)

    p = sub.add_parser("question")
    p.add_argument("--body", required=True)
    p.set_defaults(func=cmd_question)

    p = sub.add_parser("capability_publish")
    p.add_argument("--profile-hash", dest="profile_hash", required=True)
    p.set_defaults(func=cmd_capability_publish)

    lessons = sub.add_parser("lessons")
    lessons_sub = lessons.add_subparsers(dest="action", required=True)
    p = lessons_sub.add_parser("list")
    p.add_argument("--include-superseded", action="store_true")
    p.set_defaults(func=cmd_lessons)
    p = lessons_sub.add_parser("get")
    p.add_argument("subject")
    p.set_defaults(func=cmd_lessons)
    p = lessons_sub.add_parser("propose")
    p.add_argument("subject")
    p.add_argument("body")
    p.add_argument("--since", default=None)
    p.set_defaults(func=cmd_lessons)

    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    return int(args.func(args))


if __name__ == "__main__":
    raise SystemExit(main())
