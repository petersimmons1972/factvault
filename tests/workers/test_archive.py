# tests/workers/test_archive.py
import hashlib
import uuid
import zlib
import pytest
from pytest_httpx import HTTPXMock
from unittest.mock import patch
from factvault.workers.archive import ArchiveWorker
from factvault.workers.base import get_worker
from factvault.db.rls import tenant_context
from sqlalchemy import text


TENANT_ID = uuid.uuid4()
OTHER_TENANT_ID = uuid.uuid4()
FAKE_HTML = b"<html><body><p>Hello world article text.</p></body></html>"
FAKE_URL_1 = "https://example-arch.com/article-1"
FAKE_URL_2 = "https://example-arch.com/article-2"

# Placeholder hash for 'collected' rows (archive worker overwrites with real hash)
_PLACEHOLDER_HASH = "sha256:" + hashlib.sha256(b"placeholder").hexdigest()


@pytest.fixture
def two_collected_sources(app_engine):
    """Insert two sources with status='collected' and no raw_html (needs HTTP fetch)."""
    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                conn.execute(text("""
                    INSERT INTO sources (id, tenant_id, url, status, content_hash, fetched_at)
                    VALUES
                      (gen_random_uuid(), :t, :url1, 'collected', :hash, now()),
                      (gen_random_uuid(), :t, :url2, 'collected', :hash, now())
                """), {"t": str(TENANT_ID), "url1": FAKE_URL_1, "url2": FAKE_URL_2,
                       "hash": _PLACEHOLDER_HASH})
    yield
    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                conn.execute(text("DELETE FROM sources WHERE tenant_id = :t"),
                             {"t": str(TENANT_ID)})


def test_archive_worker_processes_collected_sources(
    two_collected_sources, app_engine, httpx_mock: HTTPXMock
):
    """Two 'collected' sources are fetched, extracted, and reach 'archived' status."""
    httpx_mock.add_response(url=FAKE_URL_1, content=FAKE_HTML)
    httpx_mock.add_response(url=FAKE_URL_2, content=FAKE_HTML)

    with patch("factvault.workers.archive.submit_to_wayback",
               return_value="https://web.archive.org/web/20240101/example.com"):
        worker = ArchiveWorker()
        code = worker.run({"tenant_id": str(TENANT_ID), "once": True, "interval": 60,
                           "engine": app_engine})

    assert code == 0

    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                rows = conn.execute(text("""
                    SELECT status, content_hash, raw_text, archive_url
                    FROM sources
                    WHERE tenant_id = :t
                """), {"t": str(TENANT_ID)}).fetchall()

    assert len(rows) == 2
    for row in rows:
        assert row.status == "archived"
        assert row.content_hash is not None
        assert row.content_hash.startswith("sha256:")
        assert row.archive_url is not None


def test_archive_worker_registered():
    """ArchiveWorker is importable via the registry."""
    cls = get_worker("archive")
    assert cls is ArchiveWorker


def test_archive_worker_skips_sources_with_raw_html(app_engine, httpx_mock: HTTPXMock):
    """Sources that already have raw_html skip the HTTP fetch step."""
    source_id = uuid.uuid4()
    pre_html = zlib.compress(FAKE_HTML, level=6)

    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                conn.execute(text("""
                    INSERT INTO sources
                        (id, tenant_id, url, status, content_hash, raw_html, fetched_at)
                    VALUES (:id, :t, :url, 'collected', :hash, :html, now())
                """), {"id": str(source_id), "t": str(TENANT_ID),
                       "url": "https://example-arch.com/pre-loaded",
                       "html": pre_html, "hash": _PLACEHOLDER_HASH})

    with patch("factvault.workers.archive.submit_to_wayback", return_value=None):
        worker = ArchiveWorker()
        code = worker.run({"tenant_id": str(TENANT_ID), "once": True, "interval": 60,
                           "engine": app_engine})

    assert code == 0

    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                row = conn.execute(text(
                    "SELECT status FROM sources WHERE id = :id"
                ), {"id": str(source_id)}).fetchone()

    assert row.status == "archived"

    # Cleanup
    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                conn.execute(text("DELETE FROM sources WHERE id = :id"),
                             {"id": str(source_id)})


def test_archive_worker_wayback_failure_does_not_block(app_engine, httpx_mock: HTTPXMock):
    """Wayback returning None for one source does not prevent archiving."""
    source_id = uuid.uuid4()
    url = "https://example-arch.com/wayback-fails"

    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                conn.execute(text("""
                    INSERT INTO sources (id, tenant_id, url, status, content_hash, fetched_at)
                    VALUES (:id, :t, :url, 'collected', :hash, now())
                """), {"id": str(source_id), "t": str(TENANT_ID), "url": url,
                       "hash": _PLACEHOLDER_HASH})

    httpx_mock.add_response(url=url, content=FAKE_HTML)

    with patch("factvault.workers.archive.submit_to_wayback", return_value=None):
        worker = ArchiveWorker()
        code = worker.run({"tenant_id": str(TENANT_ID), "once": True, "interval": 60,
                           "engine": app_engine})

    assert code == 0

    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                row = conn.execute(text(
                    "SELECT status, archive_url FROM sources WHERE id = :id"
                ), {"id": str(source_id)}).fetchone()

    assert row.status == "archived"
    assert row.archive_url is None  # Wayback failed → NULL, but still archived

    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                conn.execute(text("DELETE FROM sources WHERE id = :id"),
                             {"id": str(source_id)})


def test_archive_worker_does_not_touch_other_tenant(app_engine, httpx_mock: HTTPXMock):
    """RLS isolation: a source from another tenant is NOT archived by tenant A's run."""
    other_source_id = uuid.uuid4()

    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, OTHER_TENANT_ID):
                conn.execute(text("""
                    INSERT INTO sources (id, tenant_id, url, status, content_hash, fetched_at)
                    VALUES (:id, :t, :url, 'collected', :hash, now())
                """), {"id": str(other_source_id), "t": str(OTHER_TENANT_ID),
                       "url": "https://example-arch.com/other-tenant",
                       "hash": _PLACEHOLDER_HASH})

    with patch("factvault.workers.archive.submit_to_wayback", return_value=None):
        worker = ArchiveWorker()
        # Run as TENANT_ID — should NOT see OTHER_TENANT_ID's source via RLS
        worker.run({"tenant_id": str(TENANT_ID), "once": True, "interval": 60,
                    "engine": app_engine})

    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, OTHER_TENANT_ID):
                row = conn.execute(text(
                    "SELECT status FROM sources WHERE id = :id"
                ), {"id": str(other_source_id)}).fetchone()

    assert row.status == "collected"  # Untouched by TENANT_ID's worker run

    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, OTHER_TENANT_ID):
                conn.execute(text("DELETE FROM sources WHERE id = :id"),
                             {"id": str(other_source_id)})
