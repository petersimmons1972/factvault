"""Tests for the statements table."""
import uuid
import pytest
from decimal import Decimal
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError


TENANT = uuid.uuid4()


def _insert_entity(conn, tenant_id, label):
    return conn.execute(
        text(
            "INSERT INTO entities (tenant_id, label) VALUES (:tid, :label) RETURNING id"
        ),
        {"tid": str(tenant_id), "label": label},
    ).scalar()


def _insert_property(conn, tenant_id, slug, value_type):
    return conn.execute(
        text(
            "INSERT INTO properties (tenant_id, slug, label, value_type) "
            "VALUES (:tid, :slug, :slug, :vt) RETURNING id"
        ),
        {"tid": str(tenant_id), "slug": slug, "vt": value_type},
    ).scalar()


def test_statement_insert_val_text(conn):
    eid = _insert_entity(conn, TENANT, "Acme")
    pid = _insert_property(conn, TENANT, "name", "string")
    conn.execute(
        text(
            "INSERT INTO statements "
            "(tenant_id, subject_id, property_id, val_text, confidence) "
            "VALUES (:tid, :sid, :pid, :vt, :conf)"
        ),
        {"tid": str(TENANT), "sid": str(eid), "pid": str(pid), "vt": "Acme Corp", "conf": "0.5"},
    )
    result = conn.execute(
        text("SELECT val_text FROM statements WHERE subject_id = :sid"),
        {"sid": str(eid)},
    ).scalar()
    assert result == "Acme Corp"


def test_statement_rank_check(conn):
    eid = _insert_entity(conn, TENANT, "E2")
    pid = _insert_property(conn, TENANT, "status_bad", "string")
    with pytest.raises(IntegrityError, match="chk_statements_rank"):
        conn.execute(
            text(
                "INSERT INTO statements "
                "(tenant_id, subject_id, property_id, val_text, rank, confidence) "
                "VALUES (:tid, :sid, :pid, 'x', 'invalid', 0.5)"
            ),
            {"tid": str(TENANT), "sid": str(eid), "pid": str(pid)},
        )
        conn.commit()


def test_statement_confidence_range(conn):
    eid = _insert_entity(conn, TENANT, "E3")
    pid = _insert_property(conn, TENANT, "conf_test", "string")
    with pytest.raises(IntegrityError, match="chk_statements_confidence"):
        conn.execute(
            text(
                "INSERT INTO statements "
                "(tenant_id, subject_id, property_id, val_text, confidence) "
                "VALUES (:tid, :sid, :pid, 'x', 1.5)"
            ),
            {"tid": str(TENANT), "sid": str(eid), "pid": str(pid)},
        )
        conn.commit()


def test_statement_value_populated_check(conn):
    """All value columns NULL must fail."""
    eid = _insert_entity(conn, TENANT, "E4")
    pid = _insert_property(conn, TENANT, "empty_val", "string")
    with pytest.raises(IntegrityError, match="chk_statement_value_populated"):
        conn.execute(
            text(
                "INSERT INTO statements "
                "(tenant_id, subject_id, property_id, confidence) "
                "VALUES (:tid, :sid, :pid, 0.5)"
            ),
            {"tid": str(TENANT), "sid": str(eid), "pid": str(pid)},
        )
        conn.commit()


def test_statement_rank_default_normal(conn):
    eid = _insert_entity(conn, TENANT, "E5")
    pid = _insert_property(conn, TENANT, "rank_default", "string")
    conn.execute(
        text(
            "INSERT INTO statements "
            "(tenant_id, subject_id, property_id, val_text, confidence) "
            "VALUES (:tid, :sid, :pid, 'x', 0.5)"
        ),
        {"tid": str(TENANT), "sid": str(eid), "pid": str(pid)},
    )
    rank = conn.execute(
        text("SELECT rank FROM statements WHERE subject_id = :sid"),
        {"sid": str(eid)},
    ).scalar()
    assert rank == "normal"
