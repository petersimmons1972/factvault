# tests/collectors/test_upload.py
"""
Tests for the upload collector (in-process ingest functions).

PLAN-BUG NOTE 5: Use app_engine (not conn) for RLS-sensitive inserts.
PLAN-BUG NOTE 6: tenant_context sets app.tenant_id; RLS guard in DB handles empty-string.
"""
import uuid
import pytest
from sqlalchemy import text

from factvault.collectors.upload import ingest_url, ingest_file
from factvault.db.rls import tenant_context

pytestmark = pytest.mark.usefixtures("migrated_engine")


@pytest.fixture
def tenant_id():
    return uuid.uuid4()


def test_ingest_url_returns_uuid(app_engine, tenant_id):
    with app_engine.connect() as conn:
        with tenant_context(conn, tenant_id):
            source_id = ingest_url(
                conn=conn,
                url="https://example.com/page",
                tenant_id=tenant_id,
            )
    assert isinstance(source_id, uuid.UUID)


def test_ingest_url_creates_sources_row(app_engine, tenant_id):
    with app_engine.connect() as conn:
        with tenant_context(conn, tenant_id):
            source_id = ingest_url(
                conn=conn,
                url="https://example.com/article",
                tenant_id=tenant_id,
            )
            row = conn.execute(
                text("SELECT url, status, tenant_id FROM sources WHERE id = :id"),
                {"id": str(source_id)},
            ).fetchone()

    assert row is not None
    assert row.url == "https://example.com/article"
    assert row.status == "collected"
    assert uuid.UUID(str(row.tenant_id)) == tenant_id


def test_ingest_url_status_is_collected(app_engine, tenant_id):
    with app_engine.connect() as conn:
        with tenant_context(conn, tenant_id):
            source_id = ingest_url(
                conn=conn,
                url="https://example.com/status-test",
                tenant_id=tenant_id,
            )
            row = conn.execute(
                text("SELECT status FROM sources WHERE id = :id"),
                {"id": str(source_id)},
            ).fetchone()

    assert row.status == "collected"


def test_ingest_url_deduplicates(app_engine, tenant_id):
    """Ingesting the same URL twice for the same tenant returns the existing id."""
    url = "https://example.com/dedup-test"
    with app_engine.connect() as conn:
        with tenant_context(conn, tenant_id):
            id1 = ingest_url(conn=conn, url=url, tenant_id=tenant_id)
            id2 = ingest_url(conn=conn, url=url, tenant_id=tenant_id)

    assert id1 == id2


def test_ingest_file_creates_sources_row_with_raw_html(app_engine, tenant_id):
    raw_html = b"<html><head><title>Local File</title></head><body>content</body></html>"
    with app_engine.connect() as conn:
        with tenant_context(conn, tenant_id):
            source_id = ingest_file(
                conn=conn,
                url="https://example.com/uploaded",
                raw_html=raw_html,
                tenant_id=tenant_id,
            )
            row = conn.execute(
                text("SELECT url, status, raw_html FROM sources WHERE id = :id"),
                {"id": str(source_id)},
            ).fetchone()

    assert row is not None
    assert row.url == "https://example.com/uploaded"
    assert row.status == "collected"
    # raw_html stored compressed; it must be non-null and non-empty.
    assert row.raw_html is not None


def test_ingest_file_with_title(app_engine, tenant_id):
    raw_html = b"<html><head><title>My Title</title></head><body>text</body></html>"
    with app_engine.connect() as conn:
        with tenant_context(conn, tenant_id):
            source_id = ingest_file(
                conn=conn,
                url="https://example.com/titled",
                raw_html=raw_html,
                tenant_id=tenant_id,
                title="My Title",
            )
            row = conn.execute(
                text("SELECT title FROM sources WHERE id = :id"),
                {"id": str(source_id)},
            ).fetchone()

    assert row.title == "My Title"


def test_ingest_different_tenants_same_url_both_created(app_engine):
    """Same URL for two different tenants creates two separate rows."""
    tenant_a = uuid.uuid4()
    tenant_b = uuid.uuid4()
    url = "https://example.com/shared-url"

    with app_engine.connect() as conn:
        with tenant_context(conn, tenant_a):
            id_a = ingest_url(conn=conn, url=url, tenant_id=tenant_a)
        with tenant_context(conn, tenant_b):
            id_b = ingest_url(conn=conn, url=url, tenant_id=tenant_b)

    assert id_a != id_b
