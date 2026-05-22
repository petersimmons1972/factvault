"""
RSS/Atom feed collector.

Fetches one or more RSS/Atom feeds, parses with feedparser, and yields one
RawDocument per item or entry. Deduplicates by GUID/link within the collector
lifetime.
"""
from __future__ import annotations

import calendar
import logging
from datetime import datetime, timezone
from typing import Iterator

import feedparser
import httpx

from factvault.collectors.base import Collector, RawDocument, register_collector

logger = logging.getLogger(__name__)


def _parse_published(entry: object) -> datetime | None:
    published = getattr(entry, "published_parsed", None) or getattr(
        entry, "updated_parsed", None
    )
    if published is None:
        return None
    return datetime.fromtimestamp(calendar.timegm(published), tz=timezone.utc)


@register_collector
class RssCollector(Collector):
    """RSS or Atom feed collector."""

    name = "rss"

    def __init__(self, feed_urls: list[str], timeout: float = 30.0) -> None:
        self.feed_urls = feed_urls
        self.timeout = timeout
        self._seen_guids: set[str] = set()

    def fetch(self) -> Iterator[RawDocument]:
        with httpx.Client(timeout=self.timeout, follow_redirects=True) as client:
            for feed_url in self.feed_urls:
                try:
                    response = client.get(feed_url)
                    response.raise_for_status()
                except Exception as exc:
                    logger.warning("Failed to fetch RSS feed %s: %s", feed_url, exc)
                    continue

                feed = feedparser.parse(response.text)
                publisher = getattr(feed.feed, "title", None)

                for entry in feed.entries:
                    guid = getattr(entry, "id", None) or getattr(entry, "link", None)
                    if guid is None:
                        logger.warning(
                            "RSS entry in %s has no guid or link; skipping.",
                            feed_url,
                        )
                        continue
                    if guid in self._seen_guids:
                        continue
                    self._seen_guids.add(guid)

                    summary = getattr(entry, "summary", "") or ""
                    url = getattr(entry, "link", guid)

                    yield RawDocument(
                        url=url,
                        raw_html=summary.encode("utf-8"),
                        fetched_at=datetime.now(tz=timezone.utc),
                        title=getattr(entry, "title", None),
                        published_at=_parse_published(entry),
                        publisher=publisher,
                        collector_name=self.name,
                    )
