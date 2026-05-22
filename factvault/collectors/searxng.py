"""
SearXNG query collector.

Issues search queries to a SearXNG instance and yields one RawDocument per
result URL.
"""
from __future__ import annotations

import logging
from datetime import datetime, timezone
from typing import Iterator

import httpx

from factvault.collectors.base import Collector, RawDocument, register_collector

logger = logging.getLogger(__name__)


@register_collector
class SearxngCollector(Collector):
    """SearXNG query collector."""

    name = "searxng"

    def __init__(
        self,
        searxng_url: str,
        queries: list[str],
        categories: list[str] | None = None,
        language: str = "en",
        timeout: float = 30.0,
    ) -> None:
        self.searxng_url = searxng_url.rstrip("/")
        self.queries = queries
        self.categories = categories or ["general"]
        self.language = language
        self.timeout = timeout

    def fetch(self) -> Iterator[RawDocument]:
        with httpx.Client(timeout=self.timeout, follow_redirects=True) as client:
            for query in self.queries:
                yield from self._fetch_query(client, query)

    def _fetch_query(self, client: httpx.Client, query: str) -> Iterator[RawDocument]:
        params = {
            "q": query,
            "format": "json",
            "categories": ",".join(self.categories),
            "language": self.language,
        }
        try:
            response = client.get(f"{self.searxng_url}/search", params=params)
            response.raise_for_status()
            data = response.json()
        except Exception as exc:
            logger.warning("SearXNG query '%s' failed: %s", query, exc)
            return

        for result in data.get("results", []):
            url = result.get("url")
            if not url:
                continue

            yield RawDocument(
                url=url,
                raw_html=b"",
                fetched_at=datetime.now(tz=timezone.utc),
                title=result.get("title"),
                collector_name=self.name,
                metadata={
                    "snippet": result.get("content", ""),
                    "engine": result.get("engine", ""),
                    "query": query,
                },
            )
