from pytest_httpx import HTTPXMock

from factvault.collectors.http import HttpCollector


def test_http_collector_name():
    assert HttpCollector.name == "http"


def test_fetch_returns_one_doc_per_url(httpx_mock: HTTPXMock):
    html_1 = b"<html><head><title>Article One</title></head><body>Body 1</body></html>"
    html_2 = b"<html><head><title>Article Two</title></head><body>Body 2</body></html>"

    httpx_mock.add_response(url="https://example.com/article-1", content=html_1)
    httpx_mock.add_response(url="https://example.com/article-2", content=html_2)

    collector = HttpCollector(
        urls=["https://example.com/article-1", "https://example.com/article-2"]
    )
    docs = list(collector.fetch())

    assert len(docs) == 2
    assert {doc.url for doc in docs} == {
        "https://example.com/article-1",
        "https://example.com/article-2",
    }


def test_fetch_extracts_title_from_html(httpx_mock: HTTPXMock):
    html = b"<html><head><title>My Article</title></head><body>text</body></html>"
    httpx_mock.add_response(url="https://example.com/article", content=html)

    docs = list(HttpCollector(urls=["https://example.com/article"]).fetch())

    assert docs[0].title == "My Article"


def test_fetch_raw_html_populated(httpx_mock: HTTPXMock):
    html = b"<html><body>Hello</body></html>"
    httpx_mock.add_response(url="https://example.com/page", content=html)

    docs = list(HttpCollector(urls=["https://example.com/page"]).fetch())

    assert docs[0].raw_html == html


def test_fetch_collector_name_set(httpx_mock: HTTPXMock):
    httpx_mock.add_response(url="https://example.com/page", content=b"<html/>")

    docs = list(HttpCollector(urls=["https://example.com/page"]).fetch())

    assert docs[0].collector_name == "http"


def test_fetch_raw_text_is_none(httpx_mock: HTTPXMock):
    httpx_mock.add_response(url="https://example.com/page", content=b"<html/>")

    docs = list(HttpCollector(urls=["https://example.com/page"]).fetch())

    assert docs[0].raw_text is None


def test_fetch_http_error_skipped(httpx_mock: HTTPXMock):
    httpx_mock.add_response(url="https://example.com/ok", content=b"<html><title>OK</title></html>")
    httpx_mock.add_response(url="https://example.com/bad", status_code=404)

    docs = list(
        HttpCollector(urls=["https://example.com/ok", "https://example.com/bad"]).fetch()
    )

    assert len(docs) == 1
    assert docs[0].url == "https://example.com/ok"


def test_empty_url_list():
    assert list(HttpCollector(urls=[]).fetch()) == []
