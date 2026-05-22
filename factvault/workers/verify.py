# factvault/workers/verify.py
from __future__ import annotations

import logging
import time
from typing import Any

import httpx
from sqlalchemy import text

from factvault.archiving.hash import compute_hash
from factvault.db.engine import get_engine
from factvault.workers.base import Worker, register_worker

logger = logging.getLogger(__name__)

BATCH_SIZE = 50
FETCH_TIMEOUT = 15
EXCERPT_TOLERANCE = 20  # ±chars for offset drift


@register_worker
class VerifyWorker(Worker):
    """Stage 5: Re-verify source liveness and excerpt continuity.

    Writes append-only rows to source_verifications. Never overwrites raw_text
    or raw_html — the captured body is durable evidence.
    """

    name = "verify"

    def run(self, args: dict[str, Any]) -> int:
        tenant_id: str | None = args.get("tenant_id")
        once: bool = args.get("once", False)
        interval: int = args.get("interval", 60)
        age_days: int = args.get("age_threshold_days", 30)
        fetch_days: int = args.get("fetch_age_threshold_days", 7)

        # Tests pass ``engine`` directly to avoid requiring FACTVAULT_DATABASE_URL.
        engine = args.get("engine") or get_engine()
        while True:
            processed = self._process_batch(engine, tenant_id, age_days, fetch_days)
            if once or processed == 0:
                break
            if processed < BATCH_SIZE:
                time.sleep(interval)

        return 0

    def _process_batch(
        self, engine, tenant_id: str | None, age_days: int, fetch_days: int
    ) -> int:
        with engine.connect() as conn:
            if tenant_id:
                conn.execute(text("BEGIN"))
                conn.execute(text(f"SET LOCAL app.tenant_id = '{tenant_id}'"))
            rows = conn.execute(
                text("""
                    SELECT id, url, content_hash, raw_text
                    FROM sources
                    WHERE status IN ('archived', 'verified', 'content-changed')
                      AND (last_verified_at IS NULL
                           OR last_verified_at < now() - make_interval(days => :age_days))
                      AND (fetched_at IS NULL
                           OR fetched_at < now() - make_interval(days => :fetch_days))
                    ORDER BY last_verified_at NULLS FIRST
                    LIMIT :batch
                """),
                {
                    "age_days": age_days,
                    "fetch_days": fetch_days,
                    "batch": BATCH_SIZE,
                },
            ).fetchall()
            conn.commit()

        if not rows:
            return 0

        for row in rows:
            self._verify_source(engine, row, tenant_id)

        return len(rows)

    def _verify_source(self, engine, row, tenant_id: str | None) -> None:
        source_id = row.id
        url = row.url
        stored_hash: str | None = row.content_hash

        # Attempt re-fetch (no retries beyond 1 beyond httpx defaults)
        try:
            resp = httpx.get(url, timeout=FETCH_TIMEOUT, follow_redirects=True)
            resp.raise_for_status()
            new_bytes = resp.content
        except Exception as exc:
            logger.info("Link-rot detected for %s: %s", url, exc)
            self._write_verification(engine, source_id, tenant_id, "link-rot")
            return

        new_hash = compute_hash(new_bytes)
        new_text = new_bytes.decode("utf-8", errors="replace")

        if new_hash == stored_hash:
            self._write_verification(engine, source_id, tenant_id, "live",
                                     new_hash=new_hash)
            return

        # Hash changed — check excerpts linked to this source
        status = self._check_excerpts(engine, source_id, tenant_id, new_text)
        self._write_verification(engine, source_id, tenant_id, status,
                                 new_hash=new_hash)

    def _check_excerpts(
        self, engine, source_id, tenant_id: str | None, new_text: str
    ) -> str:
        """Return 'content-changed' if all excerpts found, else 'excerpt-missing'."""
        with engine.connect() as conn:
            if tenant_id:
                conn.execute(text("BEGIN"))
                conn.execute(text(f"SET LOCAL app.tenant_id = '{tenant_id}'"))
            rows = conn.execute(
                text("""
                    SELECT excerpt, excerpt_offset_start, excerpt_offset_end
                    FROM statement_sources
                    WHERE source_id = :src
                """),
                {"src": source_id},
            ).fetchall()
            conn.commit()

        if not rows:
            return "content-changed"

        for r in rows:
            excerpt: str = r.excerpt or ""
            start: int = r.excerpt_offset_start or 0
            end: int = r.excerpt_offset_end or 0

            # Offset window check with tolerance
            window_start = max(0, start - EXCERPT_TOLERANCE)
            window_end = min(len(new_text), end + EXCERPT_TOLERANCE)
            window = new_text[window_start:window_end]

            if excerpt not in window and excerpt not in new_text:
                return "excerpt-missing"

        return "content-changed"

    def _write_verification(
        self,
        engine,
        source_id,
        tenant_id: str | None,
        status: str,
        new_hash: str | None = None,
    ) -> None:
        """Insert one row into source_verifications and advance last_verified_at.

        source_verifications is append-only — UPDATE/DELETE raise exceptions.
        The source's raw_text and raw_html are NEVER touched here.
        """
        with engine.connect() as conn:
            if tenant_id:
                conn.execute(text("BEGIN"))
                conn.execute(text(f"SET LOCAL app.tenant_id = '{tenant_id}'"))
            conn.execute(
                text("""
                    INSERT INTO source_verifications
                        (id, tenant_id, source_id, status, new_content_hash, verified_at)
                    VALUES
                        (gen_random_uuid(), :t, :src, :status, :hash, now())
                """),
                {
                    "t": str(tenant_id) if tenant_id else "00000000-0000-0000-0000-000000000000",
                    "src": source_id,
                    "status": status,
                    "hash": new_hash,
                },
            )
            # Advance last_verified_at — only field we write on the source row.
            conn.execute(
                text("""
                    UPDATE sources
                    SET last_verified_at = now()
                    WHERE id = :src
                """),
                {"src": source_id},
            )
            conn.commit()
