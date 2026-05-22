# factvault/collectors/upload.py
"""
Upload collector — in-process ingest functions.

Unlike polling collectors, this module exposes two synchronous functions
called directly by API endpoints or CLI commands:

  ingest_url(conn, url, tenant_id, ...)  -> UUID
  ingest_file(conn, url, raw_html, tenant_id, ...) -> UUID

Both insert a row into `sources` with status='collected' and return the
row's id. Duplicate (tenant_id, url) pairs return the existing id via
ON CONFLICT DO NOTHING + SELECT.

PLAN-BUG NOTES:
  - Uses CAST(:param AS ...) — NOT :param::type — for raw SQL parameters.
  - content_hash is computed here even though raw_html may be empty bytes
    for URL-only ingests; it is recomputed by the archive worker anyway.
  - raw_html is zlib-compressed at level 6 before INSERT (spec §3.1).
  - TIMESTAMP(timezone=True) used in models; but this file only uses raw SQL
    with string timestamps — no SQLAlchemy column type needed here.
"""
from __future__ import annotations

import uuid
import zlib
from datetime import datetime, timezone

from sqlalchemy import text
from sqlalchemy.engine import Connection

from factvault.archiving.hash import compute_hash


def ingest_url(
    conn: Connection,
    url: str,
    tenant_id: uuid.UUID,
    title: str | None = None,
    publisher: str | None = None,
    published_at: datetime | None = None,
) -> uuid.UUID:
    """
    Ingest a URL into sources with status='collected'.

    If (tenant_id, url) already exists, returns the existing row's id.
    """
    # Compute a placeholder hash for an empty body (no HTML fetched yet).
    content_hash = compute_hash(b"")
    fetched_at = datetime.now(tz=timezone.utc).isoformat()

    # ON CONFLICT DO NOTHING handles the (tenant_id, url) unique constraint.
    conn.execute(
        text(
            """
            INSERT INTO sources (id, tenant_id, url, content_hash, fetched_at, title, publisher, published_at, status)
            VALUES (
                gen_random_uuid(),
                CAST(:tenant_id AS uuid),
                :url,
                :content_hash,
                CAST(:fetched_at AS timestamptz),
                :title,
                :publisher,
                CAST(:published_at AS timestamptz),
                'collected'
            )
            ON CONFLICT (tenant_id, url) DO NOTHING
            """
        ),
        {
            "tenant_id": str(tenant_id),
            "url": url,
            "content_hash": content_hash,
            "fetched_at": fetched_at,
            "title": title,
            "publisher": publisher,
            "published_at": published_at.isoformat() if published_at else None,
        },
    )
    # Do NOT commit here — SET LOCAL (used by tenant_context) is transaction-
    # scoped; committing would wipe the app.tenant_id GUC before the SELECT,
    # causing RLS to filter out the row we just inserted.  The caller owns the
    # transaction boundary (app_engine.connect() auto-commits on __exit__).

    row = conn.execute(
        text("SELECT id FROM sources WHERE tenant_id = CAST(:tenant_id AS uuid) AND url = :url"),
        {"tenant_id": str(tenant_id), "url": url},
    ).fetchone()
    return uuid.UUID(str(row.id))


def ingest_file(
    conn: Connection,
    url: str,
    raw_html: bytes,
    tenant_id: uuid.UUID,
    title: str | None = None,
    publisher: str | None = None,
    published_at: datetime | None = None,
) -> uuid.UUID:
    """
    Ingest a raw HTML body into sources with status='collected'.

    raw_html is zlib-compressed before INSERT (spec §3.1).
    content_hash is computed from the uncompressed body.
    """
    content_hash = compute_hash(raw_html)
    compressed = zlib.compress(raw_html, level=6)
    fetched_at = datetime.now(tz=timezone.utc).isoformat()

    conn.execute(
        text(
            """
            INSERT INTO sources (id, tenant_id, url, content_hash, raw_html, fetched_at, title, publisher, published_at, status)
            VALUES (
                gen_random_uuid(),
                CAST(:tenant_id AS uuid),
                :url,
                :content_hash,
                :raw_html,
                CAST(:fetched_at AS timestamptz),
                :title,
                :publisher,
                CAST(:published_at AS timestamptz),
                'collected'
            )
            ON CONFLICT (tenant_id, url) DO NOTHING
            """
        ),
        {
            "tenant_id": str(tenant_id),
            "url": url,
            "content_hash": content_hash,
            "raw_html": compressed,
            "fetched_at": fetched_at,
            "title": title,
            "publisher": publisher,
            "published_at": published_at.isoformat() if published_at else None,
        },
    )
    # Do NOT commit here — same reason as ingest_url (SET LOCAL + RLS).

    row = conn.execute(
        text("SELECT id FROM sources WHERE tenant_id = CAST(:tenant_id AS uuid) AND url = :url"),
        {"tenant_id": str(tenant_id), "url": url},
    ).fetchone()
    return uuid.UUID(str(row.id))
