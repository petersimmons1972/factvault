from pathlib import Path

from pytest_httpx import HTTPXMock

from factvault.collectors.searxng import SearxngCollector

FIXTURE_DIR = Path(__file__).parent.parent / "fixtures" / "wayback_responses"


def _searxng_response() -> bytes:
    return (FIXTURE_DIR / "searxng_response.json").read_bytes()


def test_searxng_collector_name():
    assert SearxngCollector.name == "searxng"


def test_fetch_yields_one_doc_per_result(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://searxng.example.com/search?q=AI+startup+funding+2026&format=json&categories=general&language=en",
        content=_searxng_response(),
        headers={"content-type": "application/json"},
    )
    docs = list(
        SearxngCollector(
            searxng_url="https://searxng.example.com",
            queries=["AI startup funding 2026"],
        ).fetch()
    )

    assert len(docs) == 2


def test_fetch_urls_correct(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://searxng.example.com/search?q=AI+startup+funding+2026&format=json&categories=general&language=en",
        content=_searxng_response(),
        headers={"content-type": "application/json"},
    )
    docs = list(
        SearxngCollector(
            searxng_url="https://searxng.example.com",
            queries=["AI startup funding 2026"],
        ).fetch()
    )

    assert {doc.url for doc in docs} == {
        "https://techcrunch.com/2026/05/01/ai-funding-round",
        "https://venturebeat.com/2026/05/02/another-ai-deal",
    }


def test_fetch_titles_correct(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://searxng.example.com/search?q=AI+startup+funding+2026&format=json&categories=general&language=en",
        content=_searxng_response(),
        headers={"content-type": "application/json"},
    )
    docs = list(
        SearxngCollector(
            searxng_url="https://searxng.example.com",
            queries=["AI startup funding 2026"],
        ).fetch()
    )

    assert "AI Startup Raises $50M Series B" in {doc.title for doc in docs}


def test_fetch_raw_html_is_none(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://searxng.example.com/search?q=AI+startup+funding+2026&format=json&categories=general&language=en",
        content=_searxng_response(),
        headers={"content-type": "application/json"},
    )
    docs = list(
        SearxngCollector(
            searxng_url="https://searxng.example.com",
            queries=["AI startup funding 2026"],
        ).fetch()
    )

    for doc in docs:
        assert doc.raw_html == b""


def test_fetch_snippet_in_metadata(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://searxng.example.com/search?q=AI+startup+funding+2026&format=json&categories=general&language=en",
        content=_searxng_response(),
        headers={"content-type": "application/json"},
    )
    docs = list(
        SearxngCollector(
            searxng_url="https://searxng.example.com",
            queries=["AI startup funding 2026"],
        ).fetch()
    )

    for doc in docs:
        assert "snippet" in doc.metadata


def test_fetch_collector_name_set(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://searxng.example.com/search?q=AI+startup+funding+2026&format=json&categories=general&language=en",
        content=_searxng_response(),
        headers={"content-type": "application/json"},
    )
    docs = list(
        SearxngCollector(
            searxng_url="https://searxng.example.com",
            queries=["AI startup funding 2026"],
        ).fetch()
    )

    for doc in docs:
        assert doc.collector_name == "searxng"


def test_fetch_api_error_skipped(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://searxng.example.com/search?q=failing+query&format=json&categories=general&language=en",
        status_code=500,
    )
    docs = list(
        SearxngCollector(
            searxng_url="https://searxng.example.com",
            queries=["failing query"],
        ).fetch()
    )

    assert docs == []


def test_empty_query_list():
    assert (
        list(
            SearxngCollector(
                searxng_url="https://searxng.example.com",
                queries=[],
            ).fetch()
        )
        == []
    )
