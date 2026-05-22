from pathlib import Path

from pytest_httpx import HTTPXMock

from factvault.collectors.wayback_cdx import WaybackCdxCollector

FIXTURE_DIR = Path(__file__).parent.parent / "fixtures" / "wayback_responses"


def _cdx_bytes() -> bytes:
    return (FIXTURE_DIR / "cdx_response.json").read_bytes()


def test_wayback_cdx_collector_name():
    assert WaybackCdxCollector.name == "wayback_cdx"


def test_fetch_yields_one_doc_per_snapshot(httpx_mock: HTTPXMock):
    httpx_mock.add_response(content=_cdx_bytes(), headers={"content-type": "application/json"})

    docs = list(WaybackCdxCollector(target_urls=["https://example.com/article-1"]).fetch())

    assert len(docs) == 2


def test_fetch_url_is_wayback_replay_url(httpx_mock: HTTPXMock):
    httpx_mock.add_response(content=_cdx_bytes(), headers={"content-type": "application/json"})

    docs = list(WaybackCdxCollector(target_urls=["https://example.com/article-1"]).fetch())

    for doc in docs:
        assert "web.archive.org" in doc.url


def test_fetch_original_url_in_metadata(httpx_mock: HTTPXMock):
    httpx_mock.add_response(content=_cdx_bytes(), headers={"content-type": "application/json"})

    docs = list(WaybackCdxCollector(target_urls=["https://example.com/article-1"]).fetch())

    for doc in docs:
        assert doc.metadata.get("original_url") == "https://example.com/article-1"


def test_fetch_wayback_timestamp_in_metadata(httpx_mock: HTTPXMock):
    httpx_mock.add_response(content=_cdx_bytes(), headers={"content-type": "application/json"})

    docs = list(WaybackCdxCollector(target_urls=["https://example.com/article-1"]).fetch())

    assert {doc.metadata.get("wayback_timestamp") for doc in docs} == {
        "20260101120000",
        "20260315080000",
    }


def test_fetch_raw_html_is_empty_bytes(httpx_mock: HTTPXMock):
    httpx_mock.add_response(content=_cdx_bytes(), headers={"content-type": "application/json"})

    docs = list(WaybackCdxCollector(target_urls=["https://example.com/article-1"]).fetch())

    for doc in docs:
        assert doc.raw_html == b""


def test_fetch_collector_name_set(httpx_mock: HTTPXMock):
    httpx_mock.add_response(content=_cdx_bytes(), headers={"content-type": "application/json"})

    docs = list(WaybackCdxCollector(target_urls=["https://example.com/article-1"]).fetch())

    for doc in docs:
        assert doc.collector_name == "wayback_cdx"


def test_fetch_cdx_error_skipped(httpx_mock: HTTPXMock):
    httpx_mock.add_response(status_code=503)

    docs = list(WaybackCdxCollector(target_urls=["https://example.com/article-1"]).fetch())

    assert docs == []


def test_empty_target_list():
    assert list(WaybackCdxCollector(target_urls=[]).fetch()) == []
