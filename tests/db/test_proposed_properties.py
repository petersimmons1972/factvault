import pytest
from uuid import uuid4
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError

TENANT = uuid4()


def _insert(conn, slug, status="pending", value_type="string"):
    conn.execute(
        text(
            "INSERT INTO proposed_properties "
            "(id, tenant_id, proposed_slug, proposed_value_type, proposed_by, status) "
            "VALUES (:id, :tid, :slug, :vt, 'llm:gpt-5:v1', :status)"
        ),
        {
            "id": str(uuid4()),
            "tid": str(TENANT),
            "slug": slug,
            "vt": value_type,
            "status": status,
        },
    )


def test_insert_ok(conn):
    _insert(conn, "test_slug_ok")
    rows = conn.execute(
        text(
            "SELECT status FROM proposed_properties "
            "WHERE tenant_id = :tid AND proposed_slug = 'test_slug_ok'"
        ),
        {"tid": str(TENANT)},
    ).fetchall()
    assert len(rows) == 1
    assert rows[0].status == "pending"


def test_status_check_rejects_invalid(conn):
    conn.execute(text("SAVEPOINT before_bad_status"))
    with pytest.raises(IntegrityError):
        _insert(conn, "bad_status_slug", status="nonsense")
    conn.execute(text("ROLLBACK TO SAVEPOINT before_bad_status"))


def test_value_type_check_rejects_invalid(conn):
    conn.execute(text("SAVEPOINT before_bad_vt"))
    with pytest.raises(IntegrityError):
        _insert(conn, "bad_vt_slug", value_type="blob")
    conn.execute(text("ROLLBACK TO SAVEPOINT before_bad_vt"))


def test_unique_constraint_same_slug_same_status(conn):
    """Two pending rows for same (tenant, slug) are not allowed."""
    conn.execute(text("SAVEPOINT before_dup"))
    with pytest.raises(IntegrityError):
        _insert(conn, "dup_slug", status="pending")
        _insert(conn, "dup_slug", status="pending")
    conn.execute(text("ROLLBACK TO SAVEPOINT before_dup"))


def test_unique_constraint_allows_different_status(conn):
    """Rejected then re-proposed (pending) is allowed — different status."""
    _insert(conn, "reprop_slug", status="rejected")
    _insert(conn, "reprop_slug", status="pending")
    rows = conn.execute(
        text(
            "SELECT status FROM proposed_properties "
            "WHERE tenant_id = :tid AND proposed_slug = 'reprop_slug' "
            "ORDER BY created_at"
        ),
        {"tid": str(TENANT)},
    ).fetchall()
    assert {r.status for r in rows} == {"rejected", "pending"}
