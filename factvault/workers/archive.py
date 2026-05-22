# factvault/workers/archive.py
from __future__ import annotations

import logging
import time
import zlib
from typing import Any

import httpx
from sqlalchemy import text

from factvault.archiving.extract import extract_text
from factvault.archiving.hash import compute_hash
from factvault.archiving.wayback import submit_url as _submit_url
from factvault.db.engine import get_engine
from factvault.workers.base import Worker, register_worker

logger = logging.getLogger(__name__)

# Expose a consistent alias so tests can patch at a stable path.
submit_to_wayback = _submit_url

BATCH_SIZE = 50
FETCH_TIMEOUT = 30  # seconds


@register_worker
class ArchiveWorker(Worker):
    """Stage 2: Archive collected sources.

    For each source in status='collected':
    - Fetch raw_html via httpx if not already present
    - Compute content_hash
    - Extract raw_text via trafilatura
    - Submit to Wayback SPN (best-effort)
    - Compress raw_html (zlib level 6)
    - UPDATE sources SET ... status='archived'
    - Commit per source (retain progress across crashes)
    """

    name = "archive"

    def run(self, args: dict[str, Any]) -> int:
        tenant_id: str | None = args.get("tenant_id")
        once: bool = args.get("once", False)
        interval: int = args.get("interval", 60)

        # Tests pass ``engine`` directly to avoid requiring FACTVAULT_DATABASE_URL.
        engine = args.get("engine") or get_engine()
        while True:
            processed = self._process_batch(engine, tenant_id)
            if once or processed == 0:
                break
            if processed < BATCH_SIZE:
                time.sleep(interval)

        return 0

    def _process_batch(self, engine, tenant_id: str | None) -> int:
        """Fetch a batch of 'collected' sources and process each one."""
        # Phase 1: collect IDs to process (short-lived connection, FOR UPDATE SKIP LOCKED)
        with engine.connect() as conn:
            if tenant_id:
                # SET LOCAL requires an active transaction; begin() ensures one.
                conn.execute(text("BEGIN"))
                conn.execute(text(f"SET LOCAL app.tenant_id = '{tenant_id}'"))
            rows = conn.execute(
                text("""
                    SELECT id, url, raw_html
                    FROM sources
                    WHERE status = 'collected'
                    ORDER BY created_at
                    LIMIT :batch
                    FOR UPDATE SKIP LOCKED
                """),
                {"batch": BATCH_SIZE},
            ).fetchall()
            # Commit releases the row locks; we process each row individually below.
            conn.commit()

        if not rows:
            return 0

        # Phase 2: process each source in its own connection+transaction.
        processed = 0
        for row in rows:
            self._archive_source(engine, row, tenant_id)
            processed += 1

        return processed

    def _archive_source(self, engine, row, tenant_id: str | None) -> None:
        source_id = row.id
        url = row.url

        # Decompress or fetch raw_html
        if row.raw_html is not None:
            try:
                raw_bytes = zlib.decompress(row.raw_html)
            except zlib.error:
                raw_bytes = row.raw_html  # stored uncompressed
        else:
            try:
                resp = httpx.get(url, timeout=FETCH_TIMEOUT, follow_redirects=True)
                resp.raise_for_status()
                raw_bytes = resp.content
            except Exception as exc:
                logger.warning("Failed to fetch %s: %s", url, exc)
                return

        content_hash = compute_hash(raw_bytes)
        raw_text = extract_text(raw_bytes, url)

        # Best-effort Wayback submission
        archive_url: str | None = None
        try:
            archive_url = submit_to_wayback(url)
        except Exception as exc:
            logger.debug("Wayback SPN failed for %s: %s", url, exc)

        compressed_html = zlib.compress(raw_bytes, level=6)

        with engine.connect() as conn:
            if tenant_id:
                conn.execute(text("BEGIN"))
                conn.execute(text(f"SET LOCAL app.tenant_id = '{tenant_id}'"))
            conn.execute(
                text("""
                    UPDATE sources
                    SET raw_html     = :html,
                        raw_text     = :text,
                        content_hash = :hash,
                        archive_url  = :archive_url,
                        fetched_at   = now(),
                        status       = 'archived'
                    WHERE id = :id
                """),
                {
                    "html": compressed_html,
                    "text": raw_text,
                    "hash": content_hash,
                    "archive_url": archive_url,
                    "id": source_id,
                },
            )
            conn.commit()

        logger.info("Archived source %s (%s)", source_id, url)
