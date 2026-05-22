"""
Load-bearing multi-tenancy test.
If this test fails, the project does not ship.

RLS isolation requires distinct connections per tenant; the function-scoped
``conn`` fixture is single-tenant by design (one SAVEPOINT-wrapped connection).
We open additional connections from the session-scoped ``app_engine`` here so
that each tenant gets a truly separate TCP/server session — the only correct
way to test that RLS boundaries hold across connection contexts.

Why ``app_engine`` and not ``migrated_engine``:
PostgreSQL superusers bypass RLS unconditionally, even with FORCE ROW LEVEL
SECURITY in effect (PG docs §5.8). The ``migrated_engine`` connects as the
'test' superuser, which would cause RLS assertions to silently pass with false
results. The ``app_engine`` fixture in conftest.py connects as a non-superuser
role so that RLS policies are actually enforced.
"""
import pytest
from uuid import uuid4
from sqlalchemy import text
from sqlalchemy.engine import Engine

from factvault.db.rls import tenant_context

TENANT_A = uuid4()
TENANT_B = uuid4()


# ---------------------------------------------------------------------------
# Behavior 1 — Read isolation (entities table)
# ---------------------------------------------------------------------------

def test_read_isolation_tenant_b_sees_zero_rows(app_engine: Engine):
    """
    Insert an entity as Tenant A.
    Switch to Tenant B context on a SEPARATE connection.
    Tenant B must see zero rows.
    Switch back to Tenant A — must see the row.
    """
    entity_id = str(uuid4())
    label = f"ReadIsolation-{entity_id[:8]}"

    # --- Tenant A inserts ---
    with app_engine.connect() as conn_a:
        with tenant_context(conn_a, TENANT_A):
            conn_a.execute(
                text(
                    "INSERT INTO entities (id, tenant_id, label) "
                    "VALUES (:id, :tid, :lbl)"
                ),
                {"id": entity_id, "tid": str(TENANT_A), "lbl": label},
            )
        conn_a.commit()

    # --- Tenant B reads on a SEPARATE connection ---
    with app_engine.connect() as conn_b:
        with tenant_context(conn_b, TENANT_B):
            rows = conn_b.execute(
                text("SELECT id FROM entities WHERE id = :id"),
                {"id": entity_id},
            ).fetchall()
        conn_b.rollback()

    assert rows == [], (
        f"Tenant B saw Tenant A's entity — RLS not enforced. rows={rows}"
    )

    # --- Tenant A reads back on a fresh connection ---
    with app_engine.connect() as conn_a2:
        with tenant_context(conn_a2, TENANT_A):
            rows_a = conn_a2.execute(
                text("SELECT label FROM entities WHERE id = :id"),
                {"id": entity_id},
            ).fetchall()
        conn_a2.rollback()

    assert len(rows_a) == 1, (
        f"Tenant A could not see its own row after Tenant B sweep. rows={rows_a}"
    )
    assert rows_a[0].label == label

    # Cleanup — superuser bypass RLS for deletion
    with app_engine.connect() as conn_clean:
        # Disable RLS enforcement for cleanup by using tenant A context
        with tenant_context(conn_clean, TENANT_A):
            conn_clean.execute(
                text("DELETE FROM entities WHERE id = :id"),
                {"id": entity_id},
            )
        conn_clean.commit()


# ---------------------------------------------------------------------------
# Behavior 2 — Write isolation: UPDATE from wrong tenant affects 0 rows
# ---------------------------------------------------------------------------

def test_write_isolation_update_from_wrong_tenant_affects_zero_rows(
    app_engine: Engine,
):
    """
    UPDATE from Tenant B on Tenant A's entity must silently affect 0 rows
    (RLS hides A's rows from B's view; no error, 0 rowcount).
    After the attempted hijack, Tenant A's label must be unchanged.
    """
    entity_id = str(uuid4())
    original_label = f"OriginalLabel-{entity_id[:8]}"

    # --- Tenant A inserts ---
    with app_engine.connect() as conn_a:
        with tenant_context(conn_a, TENANT_A):
            conn_a.execute(
                text(
                    "INSERT INTO entities (id, tenant_id, label) "
                    "VALUES (:id, :tid, :lbl)"
                ),
                {"id": entity_id, "tid": str(TENANT_A), "lbl": original_label},
            )
        conn_a.commit()

    # --- Tenant B tries to UPDATE on a SEPARATE connection ---
    with app_engine.connect() as conn_b:
        with tenant_context(conn_b, TENANT_B):
            result = conn_b.execute(
                text(
                    "UPDATE entities SET label = 'HijackedLabel' WHERE id = :id"
                ),
                {"id": entity_id},
            )
            assert result.rowcount == 0, (
                f"Expected 0 rows updated from wrong-tenant context, "
                f"got {result.rowcount}"
            )
        conn_b.rollback()

    # --- Verify Tenant A's label is unchanged ---
    with app_engine.connect() as conn_a2:
        with tenant_context(conn_a2, TENANT_A):
            row = conn_a2.execute(
                text("SELECT label FROM entities WHERE id = :id"),
                {"id": entity_id},
            ).fetchone()
        conn_a2.rollback()

    assert row is not None, "Tenant A's entity disappeared after Tenant B UPDATE attempt"
    assert row.label == original_label, (
        f"Label was mutated despite RLS: expected '{original_label}', got '{row.label}'"
    )

    # Cleanup
    with app_engine.connect() as conn_clean:
        with tenant_context(conn_clean, TENANT_A):
            conn_clean.execute(
                text("DELETE FROM entities WHERE id = :id"),
                {"id": entity_id},
            )
        conn_clean.commit()


# ---------------------------------------------------------------------------
# Behavior 3 — Cross-table isolation (sources table)
# ---------------------------------------------------------------------------

def test_cross_table_isolation_sources(app_engine: Engine):
    """
    Repeat read + write isolation checks on the ``sources`` table to confirm
    the RLS policies are applied broadly — not just on ``entities``.
    """
    source_id = str(uuid4())
    # url must be unique per (tenant_id, url) — embed the id to guarantee that
    url = f"https://example.com/source-{source_id}"

    # --- Tenant A inserts a source ---
    with app_engine.connect() as conn_a:
        with tenant_context(conn_a, TENANT_A):
            conn_a.execute(
                text(
                    "INSERT INTO sources (id, tenant_id, url, content_hash) "
                    "VALUES (:id, :tid, :url, :hash)"
                ),
                {
                    "id": source_id,
                    "tid": str(TENANT_A),
                    "url": url,
                    "hash": "abc123",
                },
            )
        conn_a.commit()

    # --- Tenant B read — must see zero rows ---
    with app_engine.connect() as conn_b:
        with tenant_context(conn_b, TENANT_B):
            rows_b = conn_b.execute(
                text("SELECT id FROM sources WHERE id = :id"),
                {"id": source_id},
            ).fetchall()
        conn_b.rollback()

    assert rows_b == [], (
        f"Tenant B saw Tenant A's source — cross-table RLS not enforced. rows={rows_b}"
    )

    # --- Tenant B UPDATE — must affect 0 rows ---
    with app_engine.connect() as conn_b2:
        with tenant_context(conn_b2, TENANT_B):
            result = conn_b2.execute(
                text(
                    "UPDATE sources SET title = 'hijacked' WHERE id = :id"
                ),
                {"id": source_id},
            )
            assert result.rowcount == 0, (
                f"Tenant B updated Tenant A's source — RLS not enforced on "
                f"sources. rowcount={result.rowcount}"
            )
        conn_b2.rollback()

    # --- Tenant A can still read its row ---
    with app_engine.connect() as conn_a2:
        with tenant_context(conn_a2, TENANT_A):
            row_a = conn_a2.execute(
                text("SELECT url FROM sources WHERE id = :id"),
                {"id": source_id},
            ).fetchone()
        conn_a2.rollback()

    assert row_a is not None, "Tenant A's source disappeared"
    assert row_a.url == url

    # Cleanup
    with app_engine.connect() as conn_clean:
        with tenant_context(conn_clean, TENANT_A):
            conn_clean.execute(
                text("DELETE FROM sources WHERE id = :id"),
                {"id": source_id},
            )
        conn_clean.commit()


# ---------------------------------------------------------------------------
# Behavior 4 — No-context behavior: SELECT without app.tenant_id returns 0 rows
# ---------------------------------------------------------------------------

def test_no_tenant_context_returns_zero_rows(app_engine: Engine):
    """
    A SELECT without setting app.tenant_id must return zero rows.

    The RLS policy evaluates:
        tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid

    When app.tenant_id is unset, current_setting('app.tenant_id', true) returns
    '' (empty string, not NULL — the missing_ok=true flag suppresses the error
    but still returns ''). NULLIF('', '') converts that to NULL; NULL::uuid is
    NULL; and NULL != tenant_id evaluates to NULL (not true), so all rows are
    filtered.  Without NULLIF the cast ''::uuid would throw an error.
    """
    entity_id = str(uuid4())
    label = f"NoContext-{entity_id[:8]}"

    # Insert as Tenant A so there is at least one row in the table
    with app_engine.connect() as conn_a:
        with tenant_context(conn_a, TENANT_A):
            conn_a.execute(
                text(
                    "INSERT INTO entities (id, tenant_id, label) "
                    "VALUES (:id, :tid, :lbl)"
                ),
                {"id": entity_id, "tid": str(TENANT_A), "lbl": label},
            )
        conn_a.commit()

    # Open a fresh connection without setting app.tenant_id at all
    with app_engine.connect() as conn_no_ctx:
        # Verify app.tenant_id is truly unset on this connection
        setting_val = conn_no_ctx.execute(
            text("SELECT current_setting('app.tenant_id', true)")
        ).scalar()
        assert setting_val in (None, ""), (
            f"Expected app.tenant_id to be unset, got: {setting_val!r}"
        )

        rows = conn_no_ctx.execute(
            text("SELECT id FROM entities WHERE id = :id"),
            {"id": entity_id},
        ).fetchall()
        conn_no_ctx.rollback()

    assert rows == [], (
        f"Expected zero rows without tenant context, got {rows}. "
        "RLS policy is not blocking unauthenticated access."
    )

    # Cleanup
    with app_engine.connect() as conn_clean:
        with tenant_context(conn_clean, TENANT_A):
            conn_clean.execute(
                text("DELETE FROM entities WHERE id = :id"),
                {"id": entity_id},
            )
        conn_clean.commit()
