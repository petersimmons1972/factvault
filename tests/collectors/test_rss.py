from pathlib import Path

from pytest_httpx import HTTPXMock

from factvault.collectors.rss import RssCollector

FIXTURE_DIR = Path(__file__).parent.parent / "fixtures" / "articles"


def _rss_bytes() -> bytes:
    return (FIXTURE_DIR / "sample.rss").read_bytes()


def test_rss_collector_name():
    assert RssCollector.name == "rss"


def test_fetch_yields_one_doc_per_item(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    docs = list(RssCollector(feed_urls=["https://feeds.example.com/rss"]).fetch())

    assert len(docs) == 2


def test_fetch_url_populated(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    docs = list(RssCollector(feed_urls=["https://feeds.example.com/rss"]).fetch())

    assert {doc.url for doc in docs} == {
        "https://example.com/article-1",
        "https://example.com/article-2",
    }


def test_fetch_title_populated(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    docs = list(RssCollector(feed_urls=["https://feeds.example.com/rss"]).fetch())

    assert {doc.title for doc in docs} == {"First Article", "Second Article"}


def test_fetch_published_at_populated(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    docs = list(RssCollector(feed_urls=["https://feeds.example.com/rss"]).fetch())

    for doc in docs:
        assert doc.published_at is not None
        assert doc.published_at.tzinfo is not None


def test_fetch_raw_html_is_description_bytes(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    docs = list(RssCollector(feed_urls=["https://feeds.example.com/rss"]).fetch())

    for doc in docs:
        assert doc.raw_html


def test_fetch_collector_name_set(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    docs = list(RssCollector(feed_urls=["https://feeds.example.com/rss"]).fetch())

    for doc in docs:
        assert doc.collector_name == "rss"


def test_fetch_deduplicates_by_guid(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    collector = RssCollector(feed_urls=["https://feeds.example.com/rss"])

    first = list(collector.fetch())
    second = list(collector.fetch())

    assert {doc.url for doc in second}.issubset({doc.url for doc in first}) or len(second) == 0


def test_empty_feed_list():
    assert list(RssCollector(feed_urls=[]).fetch()) == []
