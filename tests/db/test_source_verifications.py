import pytest
from uuid import uuid4
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError

TENANT = uuid4()


def _src_id(conn):
    sid = uuid4()
    conn.execute(
        text(
            "INSERT INTO sources (id, tenant_id, url, content_hash) "
            "VALUES (:id, :tid, :url, 'abc')"
        ),
        {"id": str(sid), "tid": str(TENANT), "url": f"https://example.com/{sid}"},
    )
    return sid


def test_insert_ok(conn):
    src = _src_id(conn)
    conn.execute(
        text(
            "INSERT INTO source_verifications (source_id, tenant_id, status) "
            "VALUES (:sid, :tid, 'live')"
        ),
        {"sid": str(src), "tid": str(TENANT)},
    )
    rows = conn.execute(
        text("SELECT status FROM source_verifications WHERE source_id = :sid"),
        {"sid": str(src)},
    ).fetchall()
    assert len(rows) == 1
    assert rows[0].status == "live"


def test_update_raises(conn):
    src = _src_id(conn)
    conn.execute(
        text(
            "INSERT INTO source_verifications (id, source_id, tenant_id, status) "
            "VALUES (:id, :sid, :tid, 'live')"
        ),
        {"id": str(uuid4()), "sid": str(src), "tid": str(TENANT)},
    )
    conn.execute(text("SAVEPOINT before_update"))
    with pytest.raises(Exception, match="append-only"):
        conn.execute(
            text(
                "UPDATE source_verifications SET status = 'link-rot' "
                "WHERE source_id = :sid"
            ),
            {"sid": str(src)},
        )
    conn.execute(text("ROLLBACK TO SAVEPOINT before_update"))


def test_delete_raises(conn):
    src = _src_id(conn)
    conn.execute(
        text(
            "INSERT INTO source_verifications (id, source_id, tenant_id, status) "
            "VALUES (:id, :sid, :tid, 'live')"
        ),
        {"id": str(uuid4()), "sid": str(src), "tid": str(TENANT)},
    )
    conn.execute(text("SAVEPOINT before_delete"))
    with pytest.raises(Exception, match="append-only"):
        conn.execute(
            text("DELETE FROM source_verifications WHERE source_id = :sid"),
            {"sid": str(src)},
        )
    conn.execute(text("ROLLBACK TO SAVEPOINT before_delete"))


def test_status_check_rejects_invalid(conn):
    src = _src_id(conn)
    conn.execute(text("SAVEPOINT before_bad_status"))
    with pytest.raises(IntegrityError):
        conn.execute(
            text(
                "INSERT INTO source_verifications (source_id, tenant_id, status) "
                "VALUES (:sid, :tid, 'bad-status')"
            ),
            {"sid": str(src), "tid": str(TENANT)},
        )
    conn.execute(text("ROLLBACK TO SAVEPOINT before_bad_status"))
