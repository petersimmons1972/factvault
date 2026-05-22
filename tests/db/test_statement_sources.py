"""Tests for the statement_sources junction table."""
import uuid
import pytest
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError


TENANT = uuid.uuid4()


def _setup(conn):
    eid = conn.execute(
        text("INSERT INTO entities (tenant_id, label) VALUES (:tid, 'E') RETURNING id"),
        {"tid": str(TENANT)},
    ).scalar()
    pid = conn.execute(
        text(
            "INSERT INTO properties (tenant_id, slug, label, value_type) "
            "VALUES (:tid, 'p_ss', 'P', 'string') RETURNING id"
        ),
        {"tid": str(TENANT)},
    ).scalar()
    stmt_id = conn.execute(
        text(
            "INSERT INTO statements "
            "(tenant_id, subject_id, property_id, val_text, confidence) "
            "VALUES (:tid, :sid, :pid, 'v', 0.5) RETURNING id"
        ),
        {"tid": str(TENANT), "sid": str(eid), "pid": str(pid)},
    ).scalar()
    src_id = conn.execute(
        text(
            "INSERT INTO sources (tenant_id, url, content_hash) "
            "VALUES (:tid, 'https://example.com/ss-test', 'hash') RETURNING id"
        ),
        {"tid": str(TENANT)},
    ).scalar()
    return stmt_id, src_id


def _insert_ss(conn, stmt_id, src_id, start=10, end=50, method="human"):
    conn.execute(
        text(
            "INSERT INTO statement_sources "
            "(statement_id, source_id, tenant_id, excerpt, "
            "excerpt_offset_start, excerpt_offset_end, extraction_method) "
            "VALUES (:sid, :src, :tid, 'excerpt text', :start, :end, :method)"
        ),
        {
            "sid": str(stmt_id),
            "src": str(src_id),
            "tid": str(TENANT),
            "start": start,
            "end": end,
            "method": method,
        },
    )


def test_statement_source_insert(conn):
    stmt_id, src_id = _setup(conn)
    _insert_ss(conn, stmt_id, src_id)
    count = conn.execute(
        text("SELECT COUNT(*) FROM statement_sources WHERE statement_id = :sid"),
        {"sid": str(stmt_id)},
    ).scalar()
    assert count == 1


def test_statement_source_unique_stmt_src(conn):
    stmt_id, src_id = _setup(conn)
    _insert_ss(conn, stmt_id, src_id)
    with pytest.raises(IntegrityError, match="uq_statement_sources_stmt_src"):
        _insert_ss(conn, stmt_id, src_id, start=20, end=60)
        conn.commit()


def test_statement_source_offset_end_gt_start(conn):
    stmt_id, src_id = _setup(conn)
    with pytest.raises(IntegrityError, match="chk_ss_offset_end"):
        _insert_ss(conn, stmt_id, src_id, start=50, end=10)
        conn.commit()


def test_statement_source_offset_start_non_negative(conn):
    stmt_id, src_id = _setup(conn)
    with pytest.raises(IntegrityError, match="chk_ss_offset_start"):
        _insert_ss(conn, stmt_id, src_id, start=-1, end=10)
        conn.commit()


def test_statement_source_cascade_on_statement_delete(conn):
    stmt_id, src_id = _setup(conn)
    _insert_ss(conn, stmt_id, src_id)
    conn.execute(
        text("DELETE FROM statements WHERE id = :sid"),
        {"sid": str(stmt_id)},
    )
    count = conn.execute(
        text(
            "SELECT COUNT(*) FROM statement_sources WHERE statement_id = :sid"
        ),
        {"sid": str(stmt_id)},
    ).scalar()
    assert count == 0


def test_statement_source_excerpt_not_null(conn):
    stmt_id, src_id = _setup(conn)
    with pytest.raises(IntegrityError, match="null value"):
        conn.execute(
            text(
                "INSERT INTO statement_sources "
                "(statement_id, source_id, tenant_id, "
                "excerpt_offset_start, excerpt_offset_end, extraction_method) "
                "VALUES (:sid, :src, :tid, 0, 10, 'human')"
            ),
            {"sid": str(stmt_id), "src": str(src_id), "tid": str(TENANT)},
        )
        conn.commit()
