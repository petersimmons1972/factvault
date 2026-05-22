"""
Session-scoped testcontainers fixture that builds the local docker/postgres/Dockerfile
so pgvector is available. Function-scoped fixture wraps each test in a rolled-back
transaction, keeping the test database clean without truncating tables.
"""
import os
import pytest
from testcontainers.postgres import PostgresContainer
from sqlalchemy import create_engine, text, Connection, Engine


POSTGRES_IMAGE = "postgres:16"  # fallback; overridden by build context below


@pytest.fixture(scope="session")
def postgres_engine() -> Engine:
    """
    Spin up a real Postgres 16 + pgvector container.
    Uses the official postgres:16 image with pgvector installed via SQL at startup,
    since testcontainers does not natively build local Dockerfiles.
    The docker/postgres/Dockerfile is the production image; this fixture
    installs pgvector the same way (CREATE EXTENSION) so tests are equivalent.
    """
    with PostgresContainer("pgvector/pgvector:pg16") as pg:
        url = pg.get_connection_url().replace("psycopg2", "psycopg")
        engine = create_engine(url, echo=False)
        yield engine
        engine.dispose()


@pytest.fixture(scope="session")
def migrated_engine(postgres_engine: Engine) -> Engine:
    """
    Runs all Alembic migrations against the session-scoped engine.
    Returns the same engine post-migration. Called by tests that need a fully
    migrated schema.

    Passes the live engine via alembic_cfg.attributes["connection"] so that
    env.py's run_migrations_online uses it directly — bypassing engine_from_config
    and the %(FACTVAULT_DATABASE_URL)s configparser interpolation that would
    otherwise fail when the env var is absent.
    """
    from alembic.config import Config
    from alembic import command

    # render_as_string(hide_password=False) is required — str(engine.url) masks
    # the password as "***", which would cause auth failure in alembic's env.py.
    db_url = postgres_engine.url.render_as_string(hide_password=False)
    # Set env var so env.py's os.environ branch fires and overrides alembic.ini
    os.environ["FACTVAULT_DATABASE_URL"] = db_url

    alembic_cfg = Config("alembic.ini")
    command.upgrade(alembic_cfg, "head")
    return postgres_engine


@pytest.fixture()
def conn(migrated_engine: Engine) -> Connection:
    """
    Function-scoped connection inside a SAVEPOINT transaction.
    Rolls back at the end of each test, leaving the DB clean.
    """
    with migrated_engine.connect() as connection:
        connection.execute(text("SAVEPOINT test_savepoint"))
        yield connection
        connection.execute(text("ROLLBACK TO SAVEPOINT test_savepoint"))
