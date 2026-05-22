"""
Tests for the Wayback Save Page Now client.
"""
from __future__ import annotations

from pytest_httpx import HTTPXMock

from factvault.archiving.wayback import submit_url


_SPN_URL = "https://web.archive.org/save"


def test_submit_url_returns_archive_url_on_success(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        url=_SPN_URL,
        method="POST",
        json={
            "url": "https://example.com/article",
            "job_id": "spn2-abc123",
            "timestamp": "20260522120000",
        },
        status_code=200,
    )

    result = submit_url("https://example.com/article")

    assert result is not None
    assert "web.archive.org" in result


def test_submit_url_returns_archive_url_format(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        url=_SPN_URL,
        method="POST",
        json={
            "url": "https://example.com/page",
            "job_id": "spn2-xyz999",
            "timestamp": "20260101080000",
        },
        status_code=200,
    )

    result = submit_url("https://example.com/page")

    assert result == "https://web.archive.org/web/20260101080000/https://example.com/page"


def test_submit_url_returns_none_on_429(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(url=_SPN_URL, method="POST", status_code=429)
    httpx_mock.add_response(url=_SPN_URL, method="POST", status_code=429)
    httpx_mock.add_response(url=_SPN_URL, method="POST", status_code=429)

    result = submit_url(
        "https://example.com/rate-limited",
        max_retries=3,
        base_delay=0.0,
    )

    assert result is None


def test_submit_url_returns_none_on_500(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(url=_SPN_URL, method="POST", status_code=500)
    httpx_mock.add_response(url=_SPN_URL, method="POST", status_code=500)
    httpx_mock.add_response(url=_SPN_URL, method="POST", status_code=500)

    result = submit_url(
        "https://example.com/server-error",
        max_retries=3,
        base_delay=0.0,
    )

    assert result is None


def test_submit_url_retries_on_429_then_succeeds(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(url=_SPN_URL, method="POST", status_code=429)
    httpx_mock.add_response(url=_SPN_URL, method="POST", status_code=429)
    httpx_mock.add_response(
        url=_SPN_URL,
        method="POST",
        json={
            "url": "https://example.com/retry-success",
            "job_id": "spn2-retry",
            "timestamp": "20260522150000",
        },
        status_code=200,
    )

    result = submit_url(
        "https://example.com/retry-success",
        max_retries=3,
        base_delay=0.0,
    )

    assert result is not None
    assert "web.archive.org" in result


def test_submit_url_returns_none_on_network_error(httpx_mock: HTTPXMock) -> None:
    import httpx as _httpx

    httpx_mock.add_exception(
        _httpx.ConnectError("connection refused"),
        url=_SPN_URL,
        method="POST",
    )
    httpx_mock.add_exception(
        _httpx.ConnectError("connection refused"),
        url=_SPN_URL,
        method="POST",
    )
    httpx_mock.add_exception(
        _httpx.ConnectError("connection refused"),
        url=_SPN_URL,
        method="POST",
    )

    result = submit_url(
        "https://example.com/network-fail",
        max_retries=3,
        base_delay=0.0,
    )

    assert result is None
