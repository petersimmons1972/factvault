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
