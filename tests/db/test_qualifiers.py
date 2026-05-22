"""Tests for the qualifiers table."""
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
            "VALUES (:tid, 'p_qual', 'P', 'string') RETURNING id"
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
    return eid, pid, stmt_id


def test_qualifier_insert(conn):
    _, pid, stmt_id = _setup(conn)
    conn.execute(
        text(
            "INSERT INTO qualifiers (statement_id, property_id, val_text) "
            "VALUES (:sid, :pid, 'qualifier value')"
        ),
        {"sid": str(stmt_id), "pid": str(pid)},
    )
    result = conn.execute(
        text("SELECT val_text FROM qualifiers WHERE statement_id = :sid"),
        {"sid": str(stmt_id)},
    ).scalar()
    assert result == "qualifier value"


def test_qualifier_value_populated_check(conn):
    _, pid, stmt_id = _setup(conn)
    with pytest.raises(IntegrityError, match="chk_qualifier_value_populated"):
        conn.execute(
            text(
                "INSERT INTO qualifiers (statement_id, property_id) "
                "VALUES (:sid, :pid)"
            ),
            {"sid": str(stmt_id), "pid": str(pid)},
        )
        conn.commit()


def test_qualifier_cascade_on_statement_delete(conn):
    _, pid, stmt_id = _setup(conn)
    conn.execute(
        text(
            "INSERT INTO qualifiers (statement_id, property_id, val_text) "
            "VALUES (:sid, :pid, 'q')"
        ),
        {"sid": str(stmt_id), "pid": str(pid)},
    )
    conn.execute(
        text("DELETE FROM statements WHERE id = :sid"),
        {"sid": str(stmt_id)},
    )
    count = conn.execute(
        text("SELECT COUNT(*) FROM qualifiers WHERE statement_id = :sid"),
        {"sid": str(stmt_id)},
    ).scalar()
    assert count == 0
