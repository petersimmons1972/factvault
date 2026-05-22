"""Tests for the entities table."""
import uuid
import pytest
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError


TENANT_A = uuid.uuid4()
TENANT_B = uuid.uuid4()


def test_entity_insert(conn):
    conn.execute(
        text(
            "INSERT INTO entities (tenant_id, label) VALUES (:tid, :label)"
        ),
        {"tid": str(TENANT_A), "label": "Acme Corp"},
    )
    result = conn.execute(
        text("SELECT label FROM entities WHERE tenant_id = :tid"),
        {"tid": str(TENANT_A)},
    ).fetchone()
    assert result[0] == "Acme Corp"


def test_entity_label_not_null(conn):
    with pytest.raises(IntegrityError, match="null value"):
        conn.execute(
            text("INSERT INTO entities (tenant_id) VALUES (:tid)"),
            {"tid": str(TENANT_A)},
        )
        conn.commit()


def test_entity_unique_ext_id_per_tenant(conn):
    conn.execute(
        text(
            "INSERT INTO entities (tenant_id, label, ext_id) VALUES (:tid, :label, :ext)"
        ),
        {"tid": str(TENANT_A), "label": "Corp A", "ext": "Q123"},
    )
    with pytest.raises(IntegrityError, match="uq_entities_tenant_ext_id"):
        conn.execute(
            text(
                "INSERT INTO entities (tenant_id, label, ext_id) VALUES (:tid, :label, :ext)"
            ),
            {"tid": str(TENANT_A), "label": "Corp B", "ext": "Q123"},
        )
        conn.commit()


def test_entity_same_ext_id_different_tenants(conn):
    """Same ext_id is allowed for different tenants."""
    conn.execute(
        text(
            "INSERT INTO entities (tenant_id, label, ext_id) VALUES (:tid, :label, :ext)"
        ),
        {"tid": str(TENANT_A), "label": "Corp A", "ext": "Q999"},
    )
    conn.execute(
        text(
            "INSERT INTO entities (tenant_id, label, ext_id) VALUES (:tid, :label, :ext)"
        ),
        {"tid": str(TENANT_B), "label": "Corp B", "ext": "Q999"},
    )
    # Both rows exist
    count = conn.execute(
        text("SELECT COUNT(*) FROM entities WHERE ext_id = 'Q999'")
    ).scalar()
    assert count == 2


def test_entity_meta_jsonb_roundtrip(conn):
    conn.execute(
        text(
            "INSERT INTO entities (tenant_id, label, meta) "
            "VALUES (:tid, :label, CAST(:meta AS jsonb))"
        ),
        {"tid": str(TENANT_A), "label": "Corp C", "meta": '{"sector": "tech", "employees": 500}'},
    )
    result = conn.execute(
        text("SELECT meta->>'sector' FROM entities WHERE label = 'Corp C'")
    ).scalar()
    assert result == "tech"


def test_entity_null_ext_id_not_unique_violation(conn):
    """NULL ext_id rows with different tenants are allowed (unique key is (tenant_id, ext_id))."""
    conn.execute(
        text("INSERT INTO entities (tenant_id, label) VALUES (:tid, 'Entity X')"),
        {"tid": str(TENANT_A)},
    )
    conn.execute(
        text("INSERT INTO entities (tenant_id, label) VALUES (:tid, 'Entity Y')"),
        {"tid": str(TENANT_B)},
    )
    count = conn.execute(
        text(
            "SELECT COUNT(*) FROM entities WHERE ext_id IS NULL"
        ),
    ).scalar()
    assert count >= 2
