"""
factvault.db.rls — Tenant context manager for Postgres RLS.

Usage::

    from factvault.db.rls import tenant_context

    with engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, tenant_id):
                rows = conn.execute(text("SELECT * FROM entities")).fetchall()
"""
from __future__ import annotations

import contextlib
from uuid import UUID
from sqlalchemy import text
from sqlalchemy.engine import Connection


@contextlib.contextmanager
def tenant_context(connection: Connection, tenant_id: UUID):
    """
    Set ``app.tenant_id`` for the current transaction so that Postgres RLS
    policies can enforce tenant isolation.

    Uses ``SET LOCAL`` so the setting is automatically rolled back at
    transaction end — no explicit cleanup required.

    The caller is responsible for wrapping the context manager inside an
    active transaction (``connection.begin()``).

    Example::

        with engine.connect() as conn:
            with conn.begin():
                with tenant_context(conn, my_tenant_uuid):
                    conn.execute(text("SELECT * FROM entities")).fetchall()
    """
    # Normalise to UUID so callers who pass a string still get proper validation.
    # This also ensures the value embedded in SET LOCAL is always a valid
    # hyphenated UUID string — safe for f-string interpolation below.
    if not isinstance(tenant_id, UUID):
        tenant_id = UUID(str(tenant_id))

    # Postgres SET LOCAL does not accept parameterised values — the value must
    # be embedded directly in the SQL string.  Using a UUID value (validated
    # above) is safe: it can only contain hex digits and hyphens.
    connection.execute(text(f"SET LOCAL app.tenant_id = '{tenant_id}'"))
    try:
        yield connection
    finally:
        # SET LOCAL reverts automatically at transaction commit/rollback;
        # nothing explicit is needed here.
        pass
