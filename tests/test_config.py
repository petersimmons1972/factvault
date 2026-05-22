# tests/test_config.py
import pytest
from pathlib import Path
from pydantic import ValidationError

from factvault.config import load_yaml_config, FactvaultConfig

FIXTURES = Path(__file__).parent / "fixtures"


def test_load_valid_config():
    cfg = load_yaml_config(str(FIXTURES / "sample_config.yaml"))
    assert isinstance(cfg, FactvaultConfig)
    assert len(cfg.tenants) == 1
    assert cfg.tenants[0].name == "default"


def test_rss_collector_config_parsed():
    cfg = load_yaml_config(str(FIXTURES / "sample_config.yaml"))
    rss = next(c for c in cfg.collectors if c.name == "rss")
    assert len(rss.config["feeds"]) > 0


def test_http_collector_config_parsed():
    cfg = load_yaml_config(str(FIXTURES / "sample_config.yaml"))
    http_col = next(c for c in cfg.collectors if c.name == "http")
    assert len(http_col.config["urls"]) > 0


def test_archive_worker_config_parsed():
    cfg = load_yaml_config(str(FIXTURES / "sample_config.yaml"))
    assert cfg.archive_worker.interval_seconds == 60
    assert cfg.archive_worker.batch_size == 50


def test_verify_worker_config_parsed():
    cfg = load_yaml_config(str(FIXTURES / "sample_config.yaml"))
    assert cfg.verify_worker.age_threshold_days == 30
    assert cfg.verify_worker.fetch_age_threshold_days == 7


def test_invalid_config_raises_validation_error():
    with pytest.raises(ValidationError) as exc_info:
        load_yaml_config(str(FIXTURES / "invalid_config.yaml"))
    # Error message should identify the offending field
    assert "tenants" in str(exc_info.value) or "interval_seconds" in str(exc_info.value)
