from __future__ import annotations

import os
import uuid
from typing import Any

import yaml
from pydantic import BaseModel, ConfigDict, field_validator


class TenantConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")

    id: uuid.UUID
    name: str


class CollectorConfig(BaseModel):
    model_config = ConfigDict(extra="allow")

    name: str
    config: dict[str, Any] = {}


class ArchiveWorkerConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")

    interval_seconds: int = 60
    batch_size: int = 50


class VerifyWorkerConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")

    age_threshold_days: int = 30
    fetch_age_threshold_days: int = 7
    batch_size: int = 50


class FactvaultConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")

    tenants: list[TenantConfig] = []
    collectors: list[CollectorConfig] = []
    archive_worker: ArchiveWorkerConfig = ArchiveWorkerConfig()
    verify_worker: VerifyWorkerConfig = VerifyWorkerConfig()

    @field_validator("tenants", mode="before")
    @classmethod
    def tenants_must_be_list(cls, v: Any) -> Any:
        if not isinstance(v, list):
            raise ValueError("tenants must be a list")
        return v


def load_yaml_config(path: str) -> FactvaultConfig:
    """Load and validate a Factvault YAML config file.

    Args:
      path: Filesystem path to the YAML config file.

    Returns:
      Validated FactvaultConfig instance.

    Raises:
      pydantic.ValidationError: if the YAML does not conform to the schema.
      FileNotFoundError: if path does not exist.
    """
    with open(path) as fh:
        raw = yaml.safe_load(fh)
    return FactvaultConfig.model_validate(raw or {})


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
