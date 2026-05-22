"""
Internet Archive Save Page Now (SPN2) client.

submit_url(url) -> str | None

Rate limiting: a per-process token bucket enforces max 15 req/min (SPN2 documented
limit). Configurable via FACTVAULT_WAYBACK_RATE_LIMIT_PER_MIN env var.
"""
from __future__ import annotations

import logging
import os
import threading
import time

import httpx

logger = logging.getLogger(__name__)

_SPN_ENDPOINT = "https://web.archive.org/save"
_WAYBACK_REPLAY_BASE = "https://web.archive.org/web/{timestamp}/{url}"


class _RateLimiter:
    """Simple token-bucket rate limiter (no external deps)."""

    def __init__(self, max_per_minute: int = 15) -> None:
        self._max = max_per_minute
        self._interval = 60.0 / max_per_minute  # seconds between requests
        self._next_allowed: float = 0.0
        self._lock = threading.Lock()

    def acquire(self) -> None:
        """Block until the next request slot is available."""
        with self._lock:
            now = time.monotonic()
            if now < self._next_allowed:
                sleep_for = self._next_allowed - now
                time.sleep(sleep_for)
                now = self._next_allowed
            self._next_allowed = max(self._next_allowed, now) + self._interval


_LIMITER = _RateLimiter(
    max_per_minute=int(os.environ.get("FACTVAULT_WAYBACK_RATE_LIMIT_PER_MIN", "15"))
)


def submit_url(
    url: str,
    max_retries: int = 3,
    base_delay: float = 5.0,
    timeout: float = 60.0,
) -> str | None:
    """
    Submit a URL to Internet Archive Save Page Now API.

    Returns the Wayback replay URL on success or None on final failure.
    """
    _LIMITER.acquire()
    for attempt in range(max_retries):
        if attempt > 0 and base_delay > 0.0:
            delay = base_delay * (2 ** (attempt - 1))
            logger.info(
                "Wayback SPN retry %d/%d for %s in %.1fs",
                attempt,
                max_retries,
                url,
                delay,
            )
            time.sleep(delay)

        try:
            with httpx.Client(timeout=timeout) as client:
                response = client.post(
                    _SPN_ENDPOINT,
                    data={"url": url, "capture_screenshot": "0"},
                    headers={"Accept": "application/json"},
                )

            if response.status_code == 200:
                data = response.json()
                timestamp = data.get("timestamp")
                original = data.get("url", url)
                if timestamp:
                    archive_url = _WAYBACK_REPLAY_BASE.format(
                        timestamp=timestamp,
                        url=original,
                    )
                    logger.info("Wayback archived %s -> %s", url, archive_url)
                    return archive_url
                logger.warning(
                    "Wayback SPN returned 200 but no timestamp for %s: %s",
                    url,
                    data,
                )
                return None

            if response.status_code in (429, 500, 502, 503, 504):
                logger.warning(
                    "Wayback SPN returned %d for %s (attempt %d/%d)",
                    response.status_code,
                    url,
                    attempt + 1,
                    max_retries,
                )
                continue

            logger.warning(
                "Wayback SPN returned non-retryable %d for %s",
                response.status_code,
                url,
            )
            return None
        except httpx.RequestError as exc:
            logger.warning(
                "Wayback SPN network error for %s (attempt %d/%d): %s",
                url,
                attempt + 1,
                max_retries,
                exc,
            )
            continue

    logger.error("Wayback SPN failed for %s after %d attempts", url, max_retries)
    return None
