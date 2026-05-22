"""Tests for the relations table."""
import uuid
import pytest
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError


def _insert_entity(conn, label, tenant_id=None):
    tid = tenant_id or uuid.uuid4()
    return conn.execute(
        text("INSERT INTO entities (tenant_id, label) VALUES (:tid, :label) RETURNING id"),
        {"tid": str(tid), "label": label},
    ).scalar(), tid


def test_relation_insert(conn):
    src, t1 = _insert_entity(conn, "Source Corp")
    tgt, t2 = _insert_entity(conn, "Target Corp")
    TENANT = t1
    conn.execute(
        text(
            "INSERT INTO relations (tenant_id, source_id, target_id, type) "
            "VALUES (:tid, :src, :tgt, 'acquired')"
        ),
        {"tid": str(TENANT), "src": str(src), "tgt": str(tgt)},
    )
    result = conn.execute(
        text("SELECT type FROM relations WHERE source_id = :src"),
        {"src": str(src)},
    ).scalar()
    assert result == "acquired"


def test_relation_source_fk(conn):
    tgt, TENANT = _insert_entity(conn, "T2")
    with pytest.raises(IntegrityError):
        conn.execute(
            text(
                "INSERT INTO relations (tenant_id, source_id, target_id, type) "
                "VALUES (:tid, :src, :tgt, 'x')"
            ),
            {"tid": str(TENANT), "src": str(uuid.uuid4()), "tgt": str(tgt)},
        )
        conn.commit()


def test_relation_confidence_check(conn):
    src, t1 = _insert_entity(conn, "S3")
    tgt, t2 = _insert_entity(conn, "T3")
    TENANT = t1
    with pytest.raises(IntegrityError, match="chk_relations_confidence"):
        conn.execute(
            text(
                "INSERT INTO relations (tenant_id, source_id, target_id, type, confidence) "
                "VALUES (:tid, :src, :tgt, 'y', 1.5)"
            ),
            {"tid": str(TENANT), "src": str(src), "tgt": str(tgt)},
        )
        conn.commit()


def test_relation_meta_jsonb(conn):
    src, t1 = _insert_entity(conn, "S4")
    tgt, t2 = _insert_entity(conn, "T4")
    TENANT = t1
    conn.execute(
        text(
            "INSERT INTO relations (tenant_id, source_id, target_id, type, meta) "
            "VALUES (:tid, :src, :tgt, 'invested_in', '{\"deal_size\": 5000000}'::jsonb)"
        ),
        {"tid": str(TENANT), "src": str(src), "tgt": str(tgt)},
    )
    result = conn.execute(
        text(
            "SELECT meta->>'deal_size' FROM relations WHERE source_id = :src"
        ),
        {"src": str(src)},
    ).scalar()
    assert result == "5000000"


def test_relation_unique_tenant_source_target_type(conn):
    src, t1 = _insert_entity(conn, "S5")
    tgt, t2 = _insert_entity(conn, "T5")
    TENANT = t1
    conn.execute(
        text(
            "INSERT INTO relations (tenant_id, source_id, target_id, type) "
            "VALUES (:tid, :src, :tgt, 'partnered_with')"
        ),
        {"tid": str(TENANT), "src": str(src), "tgt": str(tgt)},
    )
    with pytest.raises(IntegrityError, match="uq_relations_tenant_source_target_type"):
        conn.execute(
            text(
                "INSERT INTO relations (tenant_id, source_id, target_id, type) "
                "VALUES (:tid, :src, :tgt, 'partnered_with')"
            ),
            {"tid": str(TENANT), "src": str(src), "tgt": str(tgt)},
        )
        conn.commit()
