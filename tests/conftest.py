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


@pytest.fixture(scope="session")
def app_engine(migrated_engine: Engine) -> Engine:
    """
    Session-scoped engine that connects as a non-superuser role (app_user).

    Why this fixture exists:
    PostgreSQL superusers bypass Row-Level Security unconditionally — even
    FORCE ROW LEVEL SECURITY does not apply to superusers (see PG docs §5.8).
    The ``migrated_engine`` connects as the 'test' superuser, so RLS
    enforcement tests would silently pass or fail incorrectly when using it.

    This fixture creates a dedicated non-superuser ``app_user`` role after
    migrations have run (so all tables exist for GRANT), then returns an
    engine connecting as that role.  Only the RLS isolation tests use this
    fixture; all other tests continue to use ``conn`` / ``migrated_engine``.
    """
    # Create the non-superuser role using the superuser migrated_engine
    with migrated_engine.connect() as su_conn:
        su_conn.execute(
            text(
                "DO $$ BEGIN "
                "  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') "
                "  THEN CREATE ROLE app_user LOGIN PASSWORD 'apppass'; "
                "  END IF; "
                "END $$"
            )
        )
        su_conn.execute(
            text("GRANT ALL ON ALL TABLES IN SCHEMA public TO app_user")
        )
        su_conn.commit()

    # Build an engine URL for app_user by replacing the credentials
    su_url = migrated_engine.url
    app_url = su_url.set(username="app_user", password="apppass")
    engine = create_engine(app_url, echo=False)
    yield engine
    engine.dispose()
