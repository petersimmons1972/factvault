# tests/integration/test_cli_smoke.py
"""
CLI smoke tests — catch packaging errors (wrong entry point, missing imports)
that the more focused unit tests miss.

Uses CliRunner only; no DB writes (--dry-run flag for collect).
"""
from __future__ import annotations

from pathlib import Path

import pytest
from click.testing import CliRunner

from factvault.workers.cli import main

FIXTURES = Path(__file__).parent.parent / "fixtures"
SMOKE_CONFIG = str(FIXTURES / "sample_rss_config.yaml")
SMOKE_TENANT = "22222222-2222-2222-2222-222222222222"


@pytest.fixture
def runner():
    return CliRunner()


def test_list_exits_zero(runner):
    """factvault-worker list exits 0 and prints at least one worker name."""
    result = runner.invoke(main, ["list"])
    assert result.exit_code == 0, result.output
    # 'archive' and 'verify' must be registered by import
    assert "archive" in result.output
    assert "verify" in result.output


def test_help_exits_zero(runner):
    """factvault-worker --help exits 0."""
    result = runner.invoke(main, ["--help"])
    assert result.exit_code == 0


def test_run_help_exits_zero(runner):
    """factvault-worker run --help exits 0."""
    result = runner.invoke(main, ["run", "--help"])
    assert result.exit_code == 0


def test_collect_help_exits_zero(runner):
    """factvault-worker collect --help exits 0."""
    result = runner.invoke(main, ["collect", "--help"])
    assert result.exit_code == 0


def test_collect_dry_run_exits_zero(runner):
    """
    factvault-worker collect rss --config <yaml> --dry-run
    validates config + instantiates collector without any DB writes.
    """
    result = runner.invoke(main, [
        "collect", "rss",
        "--config", SMOKE_CONFIG,
        "--tenant", SMOKE_TENANT,
        "--dry-run",
    ])
    assert result.exit_code == 0, (
        f"Dry-run exited {result.exit_code}:\n{result.output}"
    )
    assert "instantiated successfully" in result.output.lower() or \
           "dry-run" in result.output.lower()


def test_run_unknown_worker_exits_nonzero(runner):
    """factvault-worker run <nonexistent> exits non-zero with helpful message."""
    result = runner.invoke(main, ["run", "does_not_exist_xyz"])
    assert result.exit_code != 0
    assert "does_not_exist_xyz" in result.output or "does_not_exist_xyz" in (result.stderr or "")


def test_collect_missing_config_exits_nonzero(runner):
    """factvault-worker collect rss --config /nonexistent exits non-zero."""
    result = runner.invoke(main, [
        "collect", "rss",
        "--config", "/nonexistent/path/config.yaml",
        "--tenant", SMOKE_TENANT,
    ])
    assert result.exit_code != 0
