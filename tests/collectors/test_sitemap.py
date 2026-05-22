from datetime import date
from pathlib import Path

from pytest_httpx import HTTPXMock

from factvault.collectors.sitemap import SitemapCollector

FIXTURE_DIR = Path(__file__).parent.parent / "fixtures" / "articles"


def test_sitemap_collector_name():
    assert SitemapCollector.name == "sitemap"


def test_fetch_yields_one_doc_per_url(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://example.com/sitemap.xml",
        content=(FIXTURE_DIR / "sample_sitemap.xml").read_bytes(),
    )

    docs = list(SitemapCollector(sitemap_urls=["https://example.com/sitemap.xml"]).fetch())

    assert len(docs) == 3


def test_fetch_urls_correct(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://example.com/sitemap.xml",
        content=(FIXTURE_DIR / "sample_sitemap.xml").read_bytes(),
    )

    docs = list(SitemapCollector(sitemap_urls=["https://example.com/sitemap.xml"]).fetch())

    assert {doc.url for doc in docs} == {
        "https://example.com/page-1",
        "https://example.com/page-2",
        "https://example.com/page-3",
    }


def test_fetch_raw_html_is_empty_bytes(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://example.com/sitemap.xml",
        content=(FIXTURE_DIR / "sample_sitemap.xml").read_bytes(),
    )

    docs = list(SitemapCollector(sitemap_urls=["https://example.com/sitemap.xml"]).fetch())

    for doc in docs:
        assert doc.raw_html == b""


def test_fetch_collector_name_set(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://example.com/sitemap.xml",
        content=(FIXTURE_DIR / "sample_sitemap.xml").read_bytes(),
    )

    docs = list(SitemapCollector(sitemap_urls=["https://example.com/sitemap.xml"]).fetch())

    for doc in docs:
        assert doc.collector_name == "sitemap"


def test_fetch_follows_sitemap_index(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://example.com/sitemap-index.xml",
        content=(FIXTURE_DIR / "sample_sitemap_index.xml").read_bytes(),
    )
    httpx_mock.add_response(
        url="https://example.com/sitemap-news.xml",
        content=(FIXTURE_DIR / "sample_sitemap.xml").read_bytes(),
    )

    docs = list(
        SitemapCollector(sitemap_urls=["https://example.com/sitemap-index.xml"]).fetch()
    )

    assert len(docs) == 3


def test_fetch_lastmod_filter(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://example.com/sitemap.xml",
        content=(FIXTURE_DIR / "sample_sitemap.xml").read_bytes(),
    )

    docs = list(
        SitemapCollector(
            sitemap_urls=["https://example.com/sitemap.xml"],
            lastmod_after=date(2026, 5, 21),
        ).fetch()
    )

    assert len(docs) == 2
    assert "https://example.com/page-1" not in {doc.url for doc in docs}


def test_empty_sitemap_list():
    assert list(SitemapCollector(sitemap_urls=[]).fetch()) == []
