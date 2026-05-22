"""
factvault.db.engine — SQLAlchemy engine factory.

The ``get_engine`` function reads FACTVAULT_DATABASE_URL from the environment
and returns a connected Engine.  Workers call ``get_engine()`` at run-time.
Tests inject an Engine directly via the ``engine`` key in the args dict so
that this module is never invoked in the test environment.
"""
from __future__ import annotations

from sqlalchemy import create_engine
from sqlalchemy.engine import Engine

from factvault.config import get_db_url

_engine: Engine | None = None


def get_engine() -> Engine:
    """Return a module-level singleton Engine built from FACTVAULT_DATABASE_URL.

    The engine is created once and reused for the lifetime of the process.
    Tests should NOT call this function; they pass an ``engine`` in the
    worker's ``args`` dict instead.
    """
    global _engine
    if _engine is None:
        _engine = create_engine(get_db_url(), pool_pre_ping=True)
    return _engine
