import pytest
from uuid import uuid4
from sqlalchemy import text

TENANT = uuid4()


def _setup(conn):
    """Insert one entity and one property; return (entity_id, property_id)."""
    eid = uuid4()
    pid = uuid4()
    conn.execute(
        text("INSERT INTO entities (id, tenant_id, label) VALUES (:id, :tid, 'ConflictCorp')"),
        {"id": str(eid), "tid": str(TENANT)},
    )
    conn.execute(
        text(
            "INSERT INTO properties (id, slug, label, value_type) "
            "VALUES (:id, :slug, 'Conflict Prop', 'string')"
        ),
        {"id": str(pid), "slug": f"conflict_prop_{pid.hex[:8]}"},
    )
    return eid, pid


def _stmt(conn, eid, pid, val, rank="normal"):
    sid = uuid4()
    conn.execute(
        text(
            "INSERT INTO statements "
            "(id, tenant_id, subject_id, property_id, val_text, rank, confidence) "
            "VALUES (:id, :tid, :eid, :pid, :val, :rank, 0.5)"
        ),
        {
            "id": str(sid),
            "tid": str(TENANT),
            "eid": str(eid),
            "pid": str(pid),
            "val": val,
            "rank": rank,
        },
    )
    return sid


def test_conflict_appears_with_three_statements(conn):
    """
    Two statements with same value (preferred + normal) + one with different value
    → one conflict row with competing_count = 3.
    """
    eid, pid = _setup(conn)
    _stmt(conn, eid, pid, "ValueA", rank="preferred")
    _stmt(conn, eid, pid, "ValueA", rank="normal")
    _stmt(conn, eid, pid, "ValueB", rank="normal")

    # Superuser bypasses RLS; filter by tenant_id via WHERE clause
    rows = conn.execute(
        text(
            "SELECT competing_count, statement_ids "
            "FROM v_conflicts "
            "WHERE subject_id = :eid AND property_id = :pid AND tenant_id = :tid"
        ),
        {"eid": str(eid), "pid": str(pid), "tid": str(TENANT)},
    ).fetchall()

    assert len(rows) == 1, f"Expected 1 conflict row, got {len(rows)}"
    assert rows[0].competing_count == 3
    assert len(rows[0].statement_ids) == 3


def test_deprecated_rows_excluded_from_conflicts(conn):
    """
    Adding a deprecated statement with a new value must not change v_conflicts output.
    """
    eid, pid = _setup(conn)
    # Create an initial conflict (2 non-deprecated rows with different values)
    _stmt(conn, eid, pid, "Alpha", rank="preferred")
    _stmt(conn, eid, pid, "Beta", rank="normal")

    before = conn.execute(
        text(
            "SELECT competing_count FROM v_conflicts "
            "WHERE subject_id = :eid AND property_id = :pid AND tenant_id = :tid"
        ),
        {"eid": str(eid), "pid": str(pid), "tid": str(TENANT)},
    ).fetchone()

    assert before is not None
    before_count = before.competing_count

    # Add a deprecated row with a brand-new value — should NOT affect v_conflicts
    _stmt(conn, eid, pid, "GammaDeprecated", rank="deprecated")

    after = conn.execute(
        text(
            "SELECT competing_count FROM v_conflicts "
            "WHERE subject_id = :eid AND property_id = :pid AND tenant_id = :tid"
        ),
        {"eid": str(eid), "pid": str(pid), "tid": str(TENANT)},
    ).fetchone()

    assert after.competing_count == before_count, (
        f"Deprecated row changed competing_count from {before_count} to {after.competing_count}"
    )
