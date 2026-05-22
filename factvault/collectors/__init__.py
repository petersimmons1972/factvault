"""Collector implementations for source ingestion."""
# Auto-import all concrete collector modules so their @register_collector decorators fire.
from factvault.collectors import http as _http  # noqa: F401
from factvault.collectors import rss as _rss  # noqa: F401
from factvault.collectors import sitemap as _sitemap  # noqa: F401
from factvault.collectors import searxng as _searxng  # noqa: F401
from factvault.collectors import wayback_cdx as _wayback_cdx  # noqa: F401
from factvault.collectors import upload as _upload  # noqa: F401
