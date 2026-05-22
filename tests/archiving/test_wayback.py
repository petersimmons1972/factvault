"""
Tests for the Wayback Save Page Now client.
"""
from __future__ import annotations

from unittest.mock import MagicMock, patch, call

from pytest_httpx import HTTPXMock

from factvault.archiving.wayback import submit_url, _RateLimiter


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


# ---------------------------------------------------------------------------
# Task 19: Rate-limit tests
# ---------------------------------------------------------------------------


def test_rate_limiter_sleeps_when_called_rapidly() -> None:
    """Rate limiter calls time.sleep when requests arrive faster than 1/interval."""
    limiter = _RateLimiter(max_per_minute=15)  # interval = 4 seconds
    expected_interval = 60.0 / 15  # 4.0 seconds

    # Simulate monotonic clock: first call at t=0, second at t=0 (same instant)
    time_values = iter([0.0, 0.0, 4.0, 8.0, 12.0, 16.0, 20.0, 24.0, 28.0,
                        32.0, 36.0, 40.0, 44.0, 48.0, 52.0, 56.0, 60.0])

    sleep_calls: list[float] = []

    def fake_monotonic() -> float:
        return next(time_values)

    def fake_sleep(secs: float) -> None:
        sleep_calls.append(secs)

    with patch("factvault.archiving.wayback.time.monotonic", side_effect=fake_monotonic):
        with patch("factvault.archiving.wayback.time.sleep", side_effect=fake_sleep):
            # First call: no sleep (clock at 0, next_allowed=0)
            limiter.acquire()
            # Second call: clock still at 0, next_allowed=4 → sleep(4)
            limiter.acquire()

    assert len(sleep_calls) >= 1
    assert abs(sleep_calls[0] - expected_interval) < 0.01


def test_rate_limiter_no_sleep_when_spaced_apart() -> None:
    """Rate limiter does NOT sleep when requests are spaced by more than the interval."""
    limiter = _RateLimiter(max_per_minute=15)  # interval = 4 seconds

    # Requests arrive 5 seconds apart — always beyond the 4s window
    time_values = iter([0.0, 5.0, 10.0])

    sleep_calls: list[float] = []

    with patch("factvault.archiving.wayback.time.monotonic", side_effect=time_values):
        with patch("factvault.archiving.wayback.time.sleep", side_effect=sleep_calls.append):
            limiter.acquire()
            limiter.acquire()

    assert sleep_calls == []


def test_rate_limiter_configurable_rate() -> None:
    """_RateLimiter respects max_per_minute constructor argument."""
    limiter_fast = _RateLimiter(max_per_minute=60)
    limiter_slow = _RateLimiter(max_per_minute=6)

    assert abs(limiter_fast._interval - 1.0) < 0.001
    assert abs(limiter_slow._interval - 10.0) < 0.001
