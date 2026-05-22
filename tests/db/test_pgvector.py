"""Verify pgcrypto and vector extensions are present after migration 0001."""
import pytest
from sqlalchemy import text


def test_pgvector_extension_loaded(conn):
    result = conn.execute(
        text(
            "SELECT extname FROM pg_extension "
            "WHERE extname IN ('vector', 'pgcrypto') ORDER BY extname"
        )
    ).fetchall()
    names = {row[0] for row in result}
    assert "vector" in names, "vector extension not found"
    assert "pgcrypto" in names, "pgcrypto extension not found"


def test_gen_random_uuid_works(conn):
    """gen_random_uuid() requires pgcrypto."""
    result = conn.execute(text("SELECT gen_random_uuid()")).scalar()
    assert result is not None
    assert len(str(result)) == 36  # UUID string length


def test_vector_type_usable(conn):
    """Create a temp table with a vector column and insert a value."""
    conn.execute(text("CREATE TEMP TABLE _vec_test (v vector(3))"))
    conn.execute(text("INSERT INTO _vec_test VALUES ('[1,2,3]')"))
    result = conn.execute(text("SELECT v FROM _vec_test")).scalar()
    assert result is not None


# ---------------------------------------------------------------------------
# Task 15 — embedding vector(1024) round-trip tests
# ---------------------------------------------------------------------------

from uuid import uuid4


def _rand_vec(dim=1024):
    import random
    return [round(random.uniform(-1.0, 1.0), 6) for _ in range(dim)]


def _vec_literal(v):
    return "[" + ",".join(str(x) for x in v) + "]"


def _as_list(val):
    """Convert a pgvector result to a plain Python list.

    Without pgvector type registration, psycopg returns vectors as strings
    like '[0.1,0.2,...]'. With registration (pgvector.Vector), list() works
    directly. This helper handles both cases.
    """
    if isinstance(val, str):
        import json
        return json.loads(val)
    return list(val)


def test_entity_embedding_roundtrip(conn):
    tenant = uuid4()
    eid = uuid4()
    vec = _rand_vec()
    conn.execute(
        text(
            "INSERT INTO entities (id, tenant_id, label, embedding) "
            "VALUES (:id, :tid, 'VecCorp', CAST(:emb AS vector))"
        ),
        {"id": str(eid), "tid": str(tenant), "emb": _vec_literal(vec)},
    )
    row = conn.execute(
        text("SELECT embedding FROM entities WHERE id = :id"),
        {"id": str(eid)},
    ).fetchone()
    assert row is not None
    # pgvector returns a list-like object; cast to list for comparison
    returned = _as_list(row.embedding)
    assert len(returned) == 1024
    assert abs(returned[0] - vec[0]) < 1e-5


def test_statement_embedding_roundtrip(conn):
    tenant = uuid4()
    eid = uuid4()
    pid = uuid4()
    conn.execute(
        text("INSERT INTO entities (id, tenant_id, label) VALUES (:id, :tid, 'E')"),
        {"id": str(eid), "tid": str(tenant)},
    )
    conn.execute(
        text(
            "INSERT INTO properties (id, slug, label, value_type) "
            "VALUES (:id, 'test_prop_emb', 'Test', 'string')"
        ),
        {"id": str(pid)},
    )
    sid = uuid4()
    vec = _rand_vec()
    conn.execute(
        text(
            "INSERT INTO statements "
            "(id, tenant_id, subject_id, property_id, val_text, rank, confidence, embedding) "
            "VALUES (:id, :tid, :eid, :pid, 'hello', 'normal', 0.5, CAST(:emb AS vector))"
        ),
        {
            "id": str(sid),
            "tid": str(tenant),
            "eid": str(eid),
            "pid": str(pid),
            "emb": _vec_literal(vec),
        },
    )
    row = conn.execute(
        text("SELECT embedding FROM statements WHERE id = :id"),
        {"id": str(sid)},
    ).fetchone()
    assert len(_as_list(row.embedding)) == 1024


def test_source_embedding_roundtrip(conn):
    tenant = uuid4()
    srcid = uuid4()
    vec = _rand_vec()
    conn.execute(
        text(
            "INSERT INTO sources (id, tenant_id, url, content_hash, embedding) "
            "VALUES (:id, :tid, :url, 'hash', CAST(:emb AS vector))"
        ),
        {
            "id": str(srcid),
            "tid": str(tenant),
            "url": f"https://example.com/emb/{srcid}",
            "emb": _vec_literal(vec),
        },
    )
    row = conn.execute(
        text("SELECT embedding FROM sources WHERE id = :id"),
        {"id": str(srcid)},
    ).fetchone()
    assert len(_as_list(row.embedding)) == 1024


def test_relation_embedding_roundtrip(conn):
    tenant = uuid4()
    e1, e2 = uuid4(), uuid4()
    for eid, label in [(e1, "A"), (e2, "B")]:
        conn.execute(
            text("INSERT INTO entities (id, tenant_id, label, ext_id) VALUES (:id, :tid, :lbl, :eid_str)"),
            {"id": str(eid), "tid": str(tenant), "lbl": label, "eid_str": str(eid)},
        )
    rid = uuid4()
    vec = _rand_vec()
    conn.execute(
        text(
            "INSERT INTO relations "
            "(id, tenant_id, source_id, target_id, type, embedding) "
            "VALUES (:id, :tid, :src, :tgt, 'acquired', CAST(:emb AS vector))"
        ),
        {
            "id": str(rid),
            "tid": str(tenant),
            "src": str(e1),
            "tgt": str(e2),
            "emb": _vec_literal(vec),
        },
    )
    row = conn.execute(
        text("SELECT embedding FROM relations WHERE id = :id"),
        {"id": str(rid)},
    ).fetchone()
    assert len(_as_list(row.embedding)) == 1024


# ---------------------------------------------------------------------------
# Task 16 — HNSW index plan-verify tests
# ---------------------------------------------------------------------------


def test_hnsw_index_used_entities(conn):
    """EXPLAIN with seqscan disabled must show the HNSW index for entities."""
    TENANT2 = uuid4()
    vec = _rand_vec()

    # Insert a handful of entities with embeddings so the planner has data
    for i in range(10):
        v = _rand_vec()
        conn.execute(
            text(
                "INSERT INTO entities (id, tenant_id, label, ext_id, embedding) "
                "VALUES (:id, :tid, :lbl, :eid_str, CAST(:emb AS vector))"
            ),
            {
                "id": str(uuid4()),
                "tid": str(TENANT2),
                "lbl": f"HNSWEntity{i}",
                "eid_str": f"hnsw-ent-{i}-{TENANT2}",
                "emb": _vec_literal(v),
            },
        )

    conn.execute(text("SET enable_seqscan = OFF"))
    plan = conn.execute(
        text(
            "EXPLAIN (ANALYZE, FORMAT TEXT) "
            "SELECT id FROM entities "
            "ORDER BY embedding <=> CAST(:emb AS vector) "
            "LIMIT 5"
        ),
        {"emb": _vec_literal(vec)},
    ).fetchall()
    conn.execute(text("SET enable_seqscan = ON"))

    plan_text = "\n".join(str(row[0]) for row in plan)
    assert "idx_entities_embedding" in plan_text, (
        f"Expected HNSW index 'idx_entities_embedding' in plan, got:\n{plan_text}"
    )


def test_hnsw_index_used_statements(conn):
    """EXPLAIN with seqscan disabled must show the HNSW index for statements."""
    TENANT3 = uuid4()
    eid = uuid4()
    pid = uuid4()

    conn.execute(
        text("INSERT INTO entities (id, tenant_id, label) VALUES (:id, :tid, 'HE')"),
        {"id": str(eid), "tid": str(TENANT3)},
    )
    conn.execute(
        text(
            "INSERT INTO properties (id, slug, label, value_type) "
            "VALUES (:id, :slug, 'HNSW', 'string')"
        ),
        {"id": str(pid), "slug": f"hnsw_prop_{pid}"},
    )
    for i in range(10):
        conn.execute(
            text(
                "INSERT INTO statements "
                "(id, tenant_id, subject_id, property_id, val_text, rank, confidence, embedding) "
                "VALUES (:id, :tid, :eid, :pid, :val, 'normal', 0.5, CAST(:emb AS vector))"
            ),
            {
                "id": str(uuid4()),
                "tid": str(TENANT3),
                "eid": str(eid),
                "pid": str(pid),
                "val": f"val{i}",
                "emb": _vec_literal(_rand_vec()),
            },
        )

    vec = _rand_vec()
    conn.execute(text("SET enable_seqscan = OFF"))
    plan = conn.execute(
        text(
            "EXPLAIN (ANALYZE, FORMAT TEXT) "
            "SELECT id FROM statements "
            "ORDER BY embedding <=> CAST(:emb AS vector) "
            "LIMIT 5"
        ),
        {"emb": _vec_literal(vec)},
    ).fetchall()
    conn.execute(text("SET enable_seqscan = ON"))

    plan_text = "\n".join(str(row[0]) for row in plan)
    assert "idx_statements_embedding" in plan_text, (
        f"Expected HNSW index 'idx_statements_embedding' in plan, got:\n{plan_text}"
    )
