# tests/workers/test_verify.py
import uuid
import hashlib
import pytest
import httpx
from pytest_httpx import HTTPXMock
from factvault.workers.verify import VerifyWorker
from factvault.workers.base import get_worker
from factvault.db.rls import tenant_context
from sqlalchemy import text

TENANT_ID = uuid.uuid4()
EXCERPT = "The quick brown fox"
BODY = f"<html><body><p>{EXCERPT} jumped over the lazy dog.</p></body></html>"
BODY_BYTES = BODY.encode()


def _hash(b: bytes) -> str:
    return "sha256:" + hashlib.sha256(b).hexdigest()


def _make_source(app_engine, url: str, with_recent_verification: bool = False):
    """Insert a single archived source; returns source_id (UUID).

    If with_recent_verification=True, sets last_verified_at=now() so the
    source is NOT due for re-verification in tests that use age_threshold_days=0.
    """
    source_id = uuid.uuid4()
    params = {
        "id": str(source_id), "t": str(TENANT_ID), "url": url,
        "raw_text": BODY, "hash": _hash(BODY_BYTES),
    }

    if with_recent_verification:
        sql = text("""
            INSERT INTO sources
                (id, tenant_id, url, status, raw_text, content_hash, fetched_at, last_verified_at)
            VALUES
                (:id, :t, :url, 'archived', :raw_text, :hash,
                 now() - interval '8 days', now())
        """)
    else:
        sql = text("""
            INSERT INTO sources
                (id, tenant_id, url, status, raw_text, content_hash, fetched_at)
            VALUES
                (:id, :t, :url, 'archived', :raw_text, :hash, now() - interval '8 days')
        """)

    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                conn.execute(sql, params)
    return source_id


def _cleanup_source(app_engine, source_id: uuid.UUID, migrated_engine=None):
    """Delete a source and its child rows. Uses superuser for source_verifications
    (append-only trigger blocks regular user DELETE — bypass via superuser ALTER TABLE DISABLE TRIGGER)."""
    if migrated_engine:
        # Superuser can temporarily disable the append-only trigger for cleanup
        with migrated_engine.connect() as conn:
            conn.execute(text(
                "ALTER TABLE source_verifications DISABLE TRIGGER trg_source_verifications_no_delete"
            ))
            conn.execute(text(
                "DELETE FROM source_verifications WHERE source_id = :id"
            ), {"id": str(source_id)})
            conn.execute(text(
                "ALTER TABLE source_verifications ENABLE TRIGGER trg_source_verifications_no_delete"
            ))
            conn.execute(text(
                "DELETE FROM statement_sources WHERE source_id = :id"
            ), {"id": str(source_id)})
            conn.execute(text(
                "DELETE FROM statements WHERE tenant_id = :t"
            ), {"t": str(TENANT_ID)})
            conn.execute(text(
                "DELETE FROM properties WHERE tenant_id = :t"
            ), {"t": str(TENANT_ID)})
            conn.execute(text(
                "DELETE FROM entities WHERE tenant_id = :t"
            ), {"t": str(TENANT_ID)})
            conn.execute(text("DELETE FROM sources WHERE id = :id"), {"id": str(source_id)})
            conn.commit()


def _run_verify(app_engine):
    """Helper: run VerifyWorker --once with zero age thresholds."""
    worker = VerifyWorker()
    return worker.run({
        "tenant_id": str(TENANT_ID),
        "once": True,
        "interval": 60,
        "age_threshold_days": 0,
        "fetch_age_threshold_days": 0,
        "engine": app_engine,
    })


def _get_verification(app_engine, source_id):
    """Fetch the most recent source_verifications row for source_id."""
    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                return conn.execute(text("""
                    SELECT status, new_content_hash FROM source_verifications
                    WHERE source_id = :id AND tenant_id = :t
                    ORDER BY verified_at DESC LIMIT 1
                """), {"id": str(source_id), "t": str(TENANT_ID)}).fetchone()


def _get_source(app_engine, source_id):
    """Fetch key fields from sources for source_id."""
    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                return conn.execute(text("""
                    SELECT status, raw_text, last_verified_at
                    FROM sources WHERE id = :id
                """), {"id": str(source_id)}).fetchone()


def _insert_statement_source(app_engine, source_id):
    """Insert entity + property + statement + statement_sources row with EXCERPT."""
    entity_id = uuid.uuid4()
    prop_id = uuid.uuid4()
    stmt_id = uuid.uuid4()
    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                conn.execute(text("""
                    INSERT INTO entities (id, tenant_id, label)
                    VALUES (:id, :t, 'Test Entity')
                """), {"id": str(entity_id), "t": str(TENANT_ID)})
                conn.execute(text("""
                    INSERT INTO properties (id, tenant_id, slug, label, value_type)
                    VALUES (:id, :t, :slug, 'Test', 'string')
                """), {"id": str(prop_id), "t": str(TENANT_ID),
                       "slug": f"test_prop_{prop_id.hex[:8]}"})
                conn.execute(text("""
                    INSERT INTO statements
                        (id, tenant_id, subject_id, property_id, val_text, rank, confidence)
                    VALUES (:id, :t, :sub, :prop, 'val', 'normal', 1.0)
                """), {"id": str(stmt_id), "t": str(TENANT_ID),
                       "sub": str(entity_id), "prop": str(prop_id)})
                conn.execute(text("""
                    INSERT INTO statement_sources
                        (id, tenant_id, statement_id, source_id, excerpt,
                         excerpt_offset_start, excerpt_offset_end,
                         extraction_method)
                    VALUES
                        (gen_random_uuid(), :t, :stmt, :src, :excerpt,
                         :start, :end, 'manual')
                """), {
                    "t": str(TENANT_ID), "stmt": str(stmt_id), "src": str(source_id),
                    "excerpt": EXCERPT,
                    "start": BODY.find(EXCERPT),
                    "end": BODY.find(EXCERPT) + len(EXCERPT),
                })


def test_verify_live_source(app_engine, migrated_engine, httpx_mock: HTTPXMock):
    """Re-fetch returns same body → source_verifications row with status='live'."""
    url = f"https://example-verify.com/live-{uuid.uuid4().hex[:8]}"
    source_id = _make_source(app_engine, url)
    httpx_mock.add_response(url=url, content=BODY_BYTES)

    code = _run_verify(app_engine)
    assert code == 0

    row = _get_verification(app_engine, source_id)
    assert row is not None
    assert row.status == "live"

    _cleanup_source(app_engine, source_id, migrated_engine)


def test_verify_link_rot(app_engine, migrated_engine, httpx_mock: HTTPXMock):
    """Connection error on re-fetch → source_verifications row with status='link-rot'."""
    url = f"https://example-verify.com/rot-{uuid.uuid4().hex[:8]}"
    source_id = _make_source(app_engine, url)
    httpx_mock.add_exception(url=url, exception=httpx.ConnectError("DNS failure"))

    code = _run_verify(app_engine)
    assert code == 0

    row = _get_verification(app_engine, source_id)
    assert row is not None
    assert row.status == "link-rot"

    # Source status must NOT change (local body is still authoritative evidence)
    src = _get_source(app_engine, source_id)
    assert src.status == "archived"

    _cleanup_source(app_engine, source_id, migrated_engine)


def test_verify_content_changed_excerpt_found(app_engine, migrated_engine, httpx_mock: HTTPXMock):
    """Hash changes but excerpt still in body → status='content-changed'."""
    url = f"https://example-verify.com/changed-{uuid.uuid4().hex[:8]}"
    source_id = _make_source(app_engine, url)
    new_body = f"<html><body><p>{EXCERPT} and more stuff added.</p></body></html>".encode()
    httpx_mock.add_response(url=url, content=new_body)

    _insert_statement_source(app_engine, source_id)

    code = _run_verify(app_engine)
    assert code == 0

    row = _get_verification(app_engine, source_id)
    assert row is not None
    assert row.status == "content-changed"

    _cleanup_source(app_engine, source_id, migrated_engine)


def test_verify_excerpt_missing(app_engine, migrated_engine, httpx_mock: HTTPXMock):
    """Hash changes and excerpt gone → status='excerpt-missing' with new_content_hash."""
    url = f"https://example-verify.com/missing-{uuid.uuid4().hex[:8]}"
    source_id = _make_source(app_engine, url)
    completely_different = b"<html><body><p>Totally different content now.</p></body></html>"
    httpx_mock.add_response(url=url, content=completely_different)

    _insert_statement_source(app_engine, source_id)

    code = _run_verify(app_engine)
    assert code == 0

    row = _get_verification(app_engine, source_id)
    assert row is not None
    assert row.status == "excerpt-missing"
    assert row.new_content_hash is not None
    assert row.new_content_hash.startswith("sha256:")

    _cleanup_source(app_engine, source_id, migrated_engine)


def test_verify_raw_text_not_overwritten(app_engine, migrated_engine, httpx_mock: HTTPXMock):
    """The source's raw_text must retain its original value in all scenarios."""
    url = f"https://example-verify.com/rawtext-{uuid.uuid4().hex[:8]}"
    source_id = _make_source(app_engine, url)
    new_body = b"<html><body><p>Completely changed.</p></body></html>"
    httpx_mock.add_response(url=url, content=new_body)

    _insert_statement_source(app_engine, source_id)
    code = _run_verify(app_engine)
    assert code == 0

    src = _get_source(app_engine, source_id)
    assert src.raw_text == BODY  # Original body preserved — architectural invariant

    _cleanup_source(app_engine, source_id, migrated_engine)


def test_verify_due_filter(app_engine, migrated_engine):
    """A source with last_verified_at = now() is NOT due and is skipped."""
    url = f"https://example-verify.com/notdue-{uuid.uuid4().hex[:8]}"
    source_id = _make_source(app_engine, url, with_recent_verification=True)

    # age_threshold_days=1 means "due if last_verified_at < now() - 1 day"
    # The source was just verified (last_verified_at = now()), so it is NOT due.
    worker = VerifyWorker()
    worker.run({
        "tenant_id": str(TENANT_ID),
        "once": True,
        "interval": 60,
        "age_threshold_days": 1,
        "fetch_age_threshold_days": 0,
        "engine": app_engine,
    })

    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                row = conn.execute(text("""
                    SELECT COUNT(*) as cnt FROM source_verifications
                    WHERE source_id = :id AND tenant_id = :t
                """), {"id": str(source_id), "t": str(TENANT_ID)}).fetchone()

    assert row.cnt == 0  # No verification row created — source was not due

    _cleanup_source(app_engine, source_id, migrated_engine)


def test_verify_worker_registered():
    cls = get_worker("verify")
    assert cls is VerifyWorker
