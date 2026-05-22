"""
Wayback CDX collector.

Queries the Internet Archive CDX API for archived snapshots of target URLs.
"""
from __future__ import annotations

import logging
from datetime import datetime, timezone
from typing import Iterator

import httpx

from factvault.collectors.base import Collector, RawDocument, register_collector

logger = logging.getLogger(__name__)

_CDX_API = "https://web.archive.org/cdx/search/cdx"
_WAYBACK_REPLAY = "https://web.archive.org/web/{timestamp}/{original}"


@register_collector
class WaybackCdxCollector(Collector):
    """Wayback CDX archive replay collector."""

    name = "wayback_cdx"

    def __init__(
        self,
        target_urls: list[str],
        limit: int = 10,
        timeout: float = 30.0,
    ) -> None:
        self.target_urls = target_urls
        self.limit = limit
        self.timeout = timeout

    def fetch(self) -> Iterator[RawDocument]:
        with httpx.Client(timeout=self.timeout, follow_redirects=True) as client:
            for original_url in self.target_urls:
                yield from self._fetch_cdx(client, original_url)

    def _fetch_cdx(self, client: httpx.Client, original_url: str) -> Iterator[RawDocument]:
        params = {
            "url": original_url,
            "output": "json",
            "fl": "timestamp,original,statuscode",
            "filter": "statuscode:200",
            "limit": str(self.limit),
        }
        try:
            response = client.get(_CDX_API, params=params)
            response.raise_for_status()
            data = response.json()
        except Exception as exc:
            logger.warning("CDX query for %s failed: %s", original_url, exc)
            return

        if len(data) < 2:
            return

        header = data[0]
        try:
            timestamp_idx = header.index("timestamp")
            original_idx = header.index("original")
        except ValueError:
            logger.warning("Unexpected CDX header format for %s: %s", original_url, header)
            return

        for row in data[1:]:
            if len(row) <= max(timestamp_idx, original_idx):
                continue

            timestamp = row[timestamp_idx]
            original = row[original_idx]
            if original != original_url:
                continue
            replay_url = _WAYBACK_REPLAY.format(timestamp=timestamp, original=original)

            yield RawDocument(
                url=replay_url,
                raw_html=b"",
                fetched_at=datetime.now(tz=timezone.utc),
                collector_name=self.name,
                metadata={
                    "original_url": original_url,
                    "wayback_timestamp": timestamp,
                },
            )
