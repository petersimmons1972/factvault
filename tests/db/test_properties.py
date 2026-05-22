"""Tests for the properties table."""
import uuid
import pytest
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError


TENANT_A = uuid.uuid4()


def test_property_insert(conn):
    conn.execute(
        text(
            "INSERT INTO properties (tenant_id, slug, label, value_type) "
            "VALUES (:tid, :slug, :label, :vt)"
        ),
        {"tid": str(TENANT_A), "slug": "founded_in", "label": "Founded in", "vt": "date"},
    )
    result = conn.execute(
        text("SELECT slug FROM properties WHERE tenant_id = :tid"),
        {"tid": str(TENANT_A)},
    ).fetchone()
    assert result[0] == "founded_in"


def test_property_value_type_check(conn):
    with pytest.raises(IntegrityError, match="chk_properties_value_type"):
        conn.execute(
            text(
                "INSERT INTO properties (tenant_id, slug, label, value_type) "
                "VALUES (:tid, :slug, :label, :vt)"
            ),
            {"tid": str(TENANT_A), "slug": "bad_prop", "label": "Bad", "vt": "foo"},
        )
        conn.commit()


def test_property_unique_slug_per_tenant(conn):
    conn.execute(
        text(
            "INSERT INTO properties (tenant_id, slug, label, value_type) "
            "VALUES (:tid, 'ceo', 'CEO', 'entity_ref')"
        ),
        {"tid": str(TENANT_A)},
    )
    with pytest.raises(IntegrityError, match="uq_properties_tenant_slug"):
        conn.execute(
            text(
                "INSERT INTO properties (tenant_id, slug, label, value_type) "
                "VALUES (:tid, 'ceo', 'Chief Executive', 'entity_ref')"
            ),
            {"tid": str(TENANT_A)},
        )
        conn.commit()


def test_property_system_wide_null_tenant(conn):
    """tenant_id=NULL means system-wide property; allowed."""
    conn.execute(
        text(
            "INSERT INTO properties (tenant_id, slug, label, value_type) "
            "VALUES (NULL, 'instance_of', 'Instance of', 'entity_ref')"
        )
    )
    result = conn.execute(
        text("SELECT slug FROM properties WHERE tenant_id IS NULL")
    ).fetchone()
    assert result[0] == "instance_of"


def test_all_value_types_accepted(conn):
    for i, vt in enumerate(["entity_ref", "string", "number", "date", "url"]):
        conn.execute(
            text(
                "INSERT INTO properties (tenant_id, slug, label, value_type) "
                "VALUES (:tid, :slug, :label, :vt)"
            ),
            {"tid": str(TENANT_A), "slug": f"prop_{vt}_{i}", "label": vt, "vt": vt},
        )
    count = conn.execute(
        text(
            "SELECT COUNT(*) FROM properties WHERE tenant_id = :tid AND slug LIKE 'prop_%'"
        ),
        {"tid": str(TENANT_A)},
    ).scalar()
    assert count == 5
