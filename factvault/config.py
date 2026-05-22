import os


def get_db_url() -> str:
    """Load database URL from FACTVAULT_DATABASE_URL env var."""
    url = os.environ.get("FACTVAULT_DATABASE_URL")
    if not url:
        raise RuntimeError(
            "FACTVAULT_DATABASE_URL environment variable is not set. "
            "Example: postgresql+psycopg://user:pass@localhost:5432/factvault"
        )
    return url


def get_tenant_id() -> str:
    """Load active tenant ID from FACTVAULT_TENANT_ID env var."""
    tenant_id = os.environ.get("FACTVAULT_TENANT_ID")
    if not tenant_id:
        raise RuntimeError(
            "FACTVAULT_TENANT_ID environment variable is not set."
        )
    return tenant_id
