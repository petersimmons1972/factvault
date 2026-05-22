"""
XML sitemap crawler.

Fetches sitemap XML, parses URL entries, and yields one RawDocument per URL.
"""
from __future__ import annotations

import logging
from datetime import date, datetime, timezone
from typing import Iterator
from xml.etree import ElementTree as ET

import httpx

from factvault.collectors.base import Collector, RawDocument, register_collector

logger = logging.getLogger(__name__)

_SM_NS = "http://www.sitemaps.org/schemas/sitemap/0.9"


def _tag(local: str) -> str:
    return f"{{{_SM_NS}}}{local}"


def _parse_lastmod(text: str | None) -> date | None:
    if not text:
        return None
    try:
        return date.fromisoformat(text.strip()[:10])
    except ValueError:
        return None


@register_collector
class SitemapCollector(Collector):
    """XML sitemap crawler with one-level sitemap index support."""

    name = "sitemap"

    def __init__(
        self,
        sitemap_urls: list[str],
        lastmod_after: date | None = None,
        timeout: float = 30.0,
    ) -> None:
        self.sitemap_urls = sitemap_urls
        self.lastmod_after = lastmod_after
        self.timeout = timeout

    def fetch(self) -> Iterator[RawDocument]:
        with httpx.Client(timeout=self.timeout, follow_redirects=True) as client:
            for sitemap_url in self.sitemap_urls:
                yield from self._fetch_sitemap(client, sitemap_url)

    def _fetch_sitemap(self, client: httpx.Client, url: str) -> Iterator[RawDocument]:
        try:
            response = client.get(url)
            response.raise_for_status()
            root = ET.fromstring(response.content)
        except Exception as exc:
            logger.warning("Failed to fetch sitemap %s: %s", url, exc)
            return

        if root.tag == _tag("sitemapindex"):
            for sitemap_el in root.findall(_tag("sitemap")):
                loc_el = sitemap_el.find(_tag("loc"))
                if loc_el is not None and loc_el.text:
                    yield from self._fetch_urlset(client, loc_el.text.strip())
            return

        yield from self._yield_urlset(root)

    def _fetch_urlset(self, client: httpx.Client, url: str) -> Iterator[RawDocument]:
        try:
            response = client.get(url)
            response.raise_for_status()
            root = ET.fromstring(response.content)
        except Exception as exc:
            logger.warning("Failed to fetch child sitemap %s: %s", url, exc)
            return

        yield from self._yield_urlset(root)

    def _yield_urlset(self, root: ET.Element) -> Iterator[RawDocument]:
        for url_el in root.findall(_tag("url")):
            loc_el = url_el.find(_tag("loc"))
            if loc_el is None or not loc_el.text:
                continue

            lastmod_el = url_el.find(_tag("lastmod"))
            lastmod = _parse_lastmod(lastmod_el.text if lastmod_el is not None else None)
            if self.lastmod_after is not None and lastmod is not None:
                if lastmod < self.lastmod_after:
                    continue

            yield RawDocument(
                url=loc_el.text.strip(),
                raw_html=b"",
                fetched_at=datetime.now(tz=timezone.utc),
                collector_name=self.name,
                metadata={"lastmod": lastmod.isoformat() if lastmod else None},
            )
