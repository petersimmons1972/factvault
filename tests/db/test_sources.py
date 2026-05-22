"""Tests for the sources table.

Invariant: raw_text is NULL at insert time (Stage 1 / Collect).
It is populated by Stage 2 (Archive). Tests verify this and the status lifecycle.
"""
import uuid
import pytest
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError


TENANT = uuid.uuid4()


def _insert_source(conn, url, status="collected", raw_text=None, content_hash="abc123"):
    conn.execute(
        text(
            "INSERT INTO sources (tenant_id, url, content_hash, status, raw_text) "
            "VALUES (:tid, :url, :hash, :status, :rt)"
        ),
        {
            "tid": str(TENANT),
            "url": url,
            "hash": content_hash,
            "status": status,
            "rt": raw_text,
        },
    )


def test_source_insert_raw_text_nullable(conn):
    """raw_text is NULL at Stage 1 (Collect) — this must not raise."""
    _insert_source(conn, "https://example.com/article-1")
    result = conn.execute(
        text("SELECT raw_text FROM sources WHERE url = 'https://example.com/article-1'")
    ).scalar()
    assert result is None


def test_source_status_default_collected(conn):
    conn.execute(
        text(
            "INSERT INTO sources (tenant_id, url, content_hash) "
            "VALUES (:tid, 'https://example.com/article-2', 'hash2')"
        ),
        {"tid": str(TENANT)},
    )
    status = conn.execute(
        text("SELECT status FROM sources WHERE url = 'https://example.com/article-2'")
    ).scalar()
    assert status == "collected"


def test_source_status_check_rejects_invalid(conn):
    with pytest.raises(IntegrityError, match="chk_sources_status"):
        _insert_source(conn, "https://example.com/article-3", status="pending")
        conn.commit()


def test_source_all_valid_statuses(conn):
    statuses = ["collected", "archived", "extracted", "verified", "link-rot", "content-changed"]
    for i, s in enumerate(statuses):
        conn.execute(
            text(
                "INSERT INTO sources (tenant_id, url, content_hash, status) "
                "VALUES (:tid, :url, 'h', :status)"
            ),
            {"tid": str(TENANT), "url": f"https://example.com/s{i}", "status": s},
        )
    count = conn.execute(
        text(
            "SELECT COUNT(*) FROM sources WHERE tenant_id = :tid AND url LIKE 'https://example.com/s%'"
        ),
        {"tid": str(TENANT)},
    ).scalar()
    assert count == 6


def test_source_raw_text_populated_at_archived(conn):
    """Simulate Stage 2: update status to 'archived' and set raw_text."""
    _insert_source(conn, "https://example.com/article-4")
    conn.execute(
        text(
            "UPDATE sources SET status = 'archived', raw_text = 'Article body text...' "
            "WHERE url = 'https://example.com/article-4'"
        )
    )
    result = conn.execute(
        text(
            "SELECT raw_text, status FROM sources "
            "WHERE url = 'https://example.com/article-4'"
        )
    ).fetchone()
    assert result[0] == "Article body text..."
    assert result[1] == "archived"


def test_source_unique_url_per_tenant(conn):
    _insert_source(conn, "https://example.com/unique")
    with pytest.raises(IntegrityError, match="uq_sources_tenant_url"):
        _insert_source(conn, "https://example.com/unique")
        conn.commit()
