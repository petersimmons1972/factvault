# tests/workers/test_cli_collect.py
"""Tests for `factvault-worker collect` subcommand."""
from __future__ import annotations

import os
import uuid
from datetime import datetime, timezone
from pathlib import Path
from unittest.mock import patch, MagicMock

import pytest
from click.testing import CliRunner
from sqlalchemy import text

from factvault.collectors.base import (
    Collector,
    RawDocument,
    _REGISTRY as _COLLECTOR_REGISTRY,
    register_collector,
)
from factvault.workers.cli import main

TENANT_ID = uuid.UUID("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")

# Path to the sample config shipped with the test fixtures
SAMPLE_CONFIG = str(Path(__file__).parent.parent / "fixtures" / "sample_config.yaml")


@pytest.fixture(autouse=True)
def isolate_collector_registry():
    """Restore collector registry state after each test."""
    original = dict(_COLLECTOR_REGISTRY)
    yield
    _COLLECTOR_REGISTRY.clear()
    _COLLECTOR_REGISTRY.update(original)


@pytest.fixture()
def runner():
    return CliRunner()


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _make_raw_doc(url: str) -> RawDocument:
    return RawDocument(
        url=url,
        raw_html=b"<html>test</html>",
        fetched_at=datetime.now(tz=timezone.utc),
        title="Test Title",
        publisher="Test Publisher",
        collector_name="fake_rss",
    )


# ---------------------------------------------------------------------------
# Test 1: happy path — rows land in sources with status='collected'
# ---------------------------------------------------------------------------

def test_collect_inserts_sources(runner, app_engine, tmp_path):
    """collect command inserts RawDocuments into sources with status='collected'."""

    # Register a fake collector that yields two documents
    @register_collector
    class _FakeCollect(Collector):
        name = "fake_collect_t16"

        def __init__(self, feeds=None, **kwargs):
            self.feeds = feeds or []

        def fetch(self):
            yield _make_raw_doc("https://example.com/article-collect-1")
            yield _make_raw_doc("https://example.com/article-collect-2")

    cfg_yaml = tmp_path / "cfg.yaml"
    cfg_yaml.write_text(
        f"""
tenants:
  - id: {TENANT_ID}
    name: test
collectors:
  - name: fake_collect_t16
    config:
      feeds:
        - https://example.com/feed.xml
"""
    )

    # Patch get_engine to return the test engine
    with patch("factvault.workers.cli.get_engine", return_value=app_engine):
        result = runner.invoke(
            main,
            ["collect", "fake_collect_t16", "--config", str(cfg_yaml), "--tenant", str(TENANT_ID)],
        )

    assert result.exit_code == 0, result.output
    assert "Collected 2 sources" in result.output

    from factvault.db.rls import tenant_context

    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                rows = conn.execute(
                    text(
                        "SELECT url, status FROM sources"
                        " WHERE tenant_id = :t AND url LIKE '%article-collect%'"
                        " ORDER BY url"
                    ),
                    {"t": str(TENANT_ID)},
                ).fetchall()

    urls = [r[0] for r in rows]
    statuses = [r[1] for r in rows]
    assert "https://example.com/article-collect-1" in urls
    assert "https://example.com/article-collect-2" in urls
    assert all(s == "collected" for s in statuses)

    # Cleanup
    from factvault.db.rls import tenant_context as tc
    with app_engine.connect() as conn:
        with conn.begin():
            with tc(conn, TENANT_ID):
                conn.execute(
                    text("DELETE FROM sources WHERE tenant_id = :t AND url LIKE '%article-collect%'"),
                    {"t": str(TENANT_ID)},
                )


# ---------------------------------------------------------------------------
# Test 2: unknown collector → exit code != 0, error message names it
# ---------------------------------------------------------------------------

def test_collect_unknown_collector_exits_nonzero(runner, tmp_path):
    """Unknown collector name produces a non-zero exit and names it in the error."""
    cfg_yaml = tmp_path / "cfg.yaml"
    cfg_yaml.write_text(
        f"""
tenants:
  - id: {TENANT_ID}
    name: test
collectors:
  - name: no_such_collector_xyz
    config: {{}}
"""
    )

    result = runner.invoke(
        main,
        [
            "collect",
            "no_such_collector_xyz",
            "--config",
            str(cfg_yaml),
            "--tenant",
            str(TENANT_ID),
        ],
    )

    assert result.exit_code != 0
    assert "no_such_collector_xyz" in (result.output + (result.stderr or ""))


# ---------------------------------------------------------------------------
# Test 3: --dry-run prints URLs but does NOT write to DB
# ---------------------------------------------------------------------------

def test_collect_dry_run_no_db_writes(runner, app_engine, tmp_path):
    """--dry-run prints URLs to stdout but does not insert anything into the DB."""

    @register_collector
    class _FakeDry(Collector):
        name = "fake_dry_t16"

        def __init__(self, feeds=None, **kwargs):
            pass

        def fetch(self):
            yield _make_raw_doc("https://example.com/dry-run-url-1")

    cfg_yaml = tmp_path / "cfg.yaml"
    cfg_yaml.write_text(
        f"""
tenants:
  - id: {TENANT_ID}
    name: test
collectors:
  - name: fake_dry_t16
    config:
      feeds: []
"""
    )

    with patch("factvault.workers.cli.get_engine", return_value=app_engine):
        result = runner.invoke(
            main,
            [
                "collect",
                "fake_dry_t16",
                "--config",
                str(cfg_yaml),
                "--tenant",
                str(TENANT_ID),
                "--dry-run",
            ],
        )

    assert result.exit_code == 0, result.output
    assert "https://example.com/dry-run-url-1" in result.output

    # Confirm no row was inserted
    from factvault.db.rls import tenant_context

    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, TENANT_ID):
                count = conn.execute(
                    text(
                        "SELECT COUNT(*) FROM sources"
                        " WHERE tenant_id = :t AND url = 'https://example.com/dry-run-url-1'"
                    ),
                    {"t": str(TENANT_ID)},
                ).scalar()

    assert count == 0


# ---------------------------------------------------------------------------
# Test 4: missing --tenant → exit code != 0
# ---------------------------------------------------------------------------

def test_collect_missing_tenant_exits_nonzero(runner, tmp_path):
    """--tenant is required; omitting it must produce a non-zero exit."""
    cfg_yaml = tmp_path / "cfg.yaml"
    cfg_yaml.write_text(
        """
tenants: []
collectors: []
"""
    )

    result = runner.invoke(
        main,
        ["collect", "rss", "--config", str(cfg_yaml)],
    )

    assert result.exit_code != 0
