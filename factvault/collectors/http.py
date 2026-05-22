"""
Generic HTTP collector.

Fetches a static list of URLs with httpx. For each URL that returns HTTP 200,
yields one RawDocument with raw_html populated and title extracted from the
<title> tag. raw_text is NOT populated here.
"""
from __future__ import annotations

import logging
import re
from datetime import datetime, timezone
from typing import Iterator

import httpx

from factvault.collectors.base import Collector, RawDocument, register_collector

logger = logging.getLogger(__name__)

_TITLE_RE = re.compile(rb"<title[^>]*>(.*?)</title>", re.IGNORECASE | re.DOTALL)


def _extract_title(raw_html: bytes) -> str | None:
    match = _TITLE_RE.search(raw_html)
    if match:
        return match.group(1).decode("utf-8", errors="replace").strip()
    return None


@register_collector
class HttpCollector(Collector):
    """Fetches a static list of URLs."""

    name = "http"

    def __init__(
        self,
        urls: list[str],
        timeout: float = 30.0,
        headers: dict[str, str] | None = None,
    ) -> None:
        self.urls = urls
        self.timeout = timeout
        self.headers = headers or {}

    def fetch(self) -> Iterator[RawDocument]:
        with httpx.Client(
            timeout=self.timeout,
            headers=self.headers,
            follow_redirects=True,
        ) as client:
            for url in self.urls:
                try:
                    response = client.get(url)
                    response.raise_for_status()
                except httpx.HTTPStatusError as exc:
                    logger.warning(
                        "HTTP error fetching %s: %s %s",
                        url,
                        exc.response.status_code,
                        exc.response.reason_phrase,
                    )
                    continue
                except httpx.RequestError as exc:
                    logger.warning("Network error fetching %s: %s", url, exc)
                    continue

                yield RawDocument(
                    url=url,
                    raw_html=response.content,
                    fetched_at=datetime.now(tz=timezone.utc),
                    title=_extract_title(response.content),
                    collector_name=self.name,
                )
