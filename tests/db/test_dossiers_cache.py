import pytest
from uuid import uuid4
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError
import json

TENANT = uuid4()


def _entity_id(conn):
    eid = uuid4()
    conn.execute(
        text(
            "INSERT INTO entities (id, tenant_id, label) "
            "VALUES (:id, :tid, 'TestCorp')"
        ),
        {"id": str(eid), "tid": str(TENANT)},
    )
    return eid


def test_insert_and_retrieve_bundle(conn):
    payload = {"facts": [{"id": str(uuid4()), "rank": "preferred"}], "assembled_at": "2026-05-22T00:00:00Z"}
    eid = _entity_id(conn)
    conn.execute(
        text(
            "INSERT INTO dossiers (id, tenant_id, entity_id, bundle) "
            "VALUES (:id, :tid, :eid, CAST(:bundle AS jsonb))"
        ),
        {
            "id": str(uuid4()),
            "tid": str(TENANT),
            "eid": str(eid),
            "bundle": json.dumps(payload),
        },
    )
    row = conn.execute(
        text("SELECT bundle FROM dossiers WHERE entity_id = :eid"),
        {"eid": str(eid)},
    ).fetchone()
    assert row is not None
    assert row.bundle["assembled_at"] == "2026-05-22T00:00:00Z"
    assert len(row.bundle["facts"]) == 1


def test_unique_tenant_entity(conn):
    eid = _entity_id(conn)
    conn.execute(
        text(
            "INSERT INTO dossiers (id, tenant_id, entity_id, bundle) "
            "VALUES (:id, :tid, :eid, '{}'::jsonb)"
        ),
        {"id": str(uuid4()), "tid": str(TENANT), "eid": str(eid)},
    )
    conn.execute(text("SAVEPOINT before_dup_dossier"))
    with pytest.raises(IntegrityError):
        conn.execute(
            text(
                "INSERT INTO dossiers (id, tenant_id, entity_id, bundle) "
                "VALUES (:id, :tid, :eid, '{}'::jsonb)"
            ),
            {"id": str(uuid4()), "tid": str(TENANT), "eid": str(eid)},
        )
    conn.execute(text("ROLLBACK TO SAVEPOINT before_dup_dossier"))
