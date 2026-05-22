from datetime import datetime, timezone

import pytest

from factvault.collectors.base import (
    Collector,
    CollectorRegistry,
    RawDocument,
    get_collector,
    register_collector,
)


def test_rawdocument_is_frozen():
    doc = RawDocument(
        url="https://example.com",
        raw_html=b"<html/>",
        fetched_at=datetime.now(tz=timezone.utc),
    )
    with pytest.raises((AttributeError, TypeError)):
        doc.url = "https://other.com"  # type: ignore[misc]


def test_rawdocument_defaults():
    doc = RawDocument(
        url="https://example.com",
        raw_html=b"<html/>",
        fetched_at=datetime.now(tz=timezone.utc),
    )
    assert doc.title is None
    assert doc.raw_text is None
    assert doc.published_at is None
    assert doc.publisher is None
    assert doc.collector_name is None
    assert doc.metadata == {}


def test_rawdocument_is_hashable():
    doc = RawDocument(
        url="https://example.com",
        raw_html=b"<html/>",
        fetched_at=datetime.now(tz=timezone.utc),
    )
    assert len({doc}) == 1


def test_rawdocument_equality():
    timestamp = datetime(2026, 1, 1, tzinfo=timezone.utc)
    a = RawDocument(url="https://x.com", raw_html=b"a", fetched_at=timestamp)
    b = RawDocument(url="https://x.com", raw_html=b"a", fetched_at=timestamp)
    assert a == b


def test_collector_abc_cannot_instantiate():
    with pytest.raises(TypeError):
        Collector()  # type: ignore[abstract]


def test_collector_subclass_without_fetch_rejected():
    class BadCollector(Collector):
        name = "bad"

    with pytest.raises(TypeError):
        BadCollector()  # type: ignore[abstract]


def test_collector_subclass_with_fetch_ok():
    class GoodCollector(Collector):
        name = "good"

        def fetch(self):
            return iter([])

    assert list(GoodCollector().fetch()) == []


def test_register_and_get_collector():
    class MyCollector(Collector):
        name = "my_test_collector"

        def fetch(self):
            return iter([])

    register_collector(MyCollector)
    assert get_collector("my_test_collector") is MyCollector
    assert "my_test_collector" in CollectorRegistry.all()


def test_get_unknown_collector_raises():
    with pytest.raises(KeyError):
        get_collector("nonexistent_xyz_collector")


def test_register_duplicate_raises():
    class DupCollector(Collector):
        name = "dup_test_collector"

        def fetch(self):
            return iter([])

    register_collector(DupCollector)

    class DupCollector2(Collector):
        name = "dup_test_collector"

        def fetch(self):
            return iter([])

    with pytest.raises(ValueError, match="already registered"):
        register_collector(DupCollector2)
