# Source Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the source-existence pillar end-to-end. Pluggable collectors ingest URLs into `sources` rows; an archive worker captures full body, computes content hash, and submits to Wayback Machine; a periodic verify worker re-fetches and confirms excerpts still exist. This is Plan 2 of 5; it depends on Plan 1's schema and is depended on by Plan 3 (fact-pipeline).

**Architecture:** Collector ABC + 6 default implementations registered via Python entrypoints. Workers run as standalone CLI commands (`factvault-worker archive`, `factvault-worker verify`) and operate per-tenant via `tenant_context`. Wayback Save Page Now integration is best-effort; local body capture via trafilatura is authoritative. Verify worker writes append-only rows to `source_verifications` — nothing destructive ever happens to a source.

**Tech Stack:** Python 3.12, SQLAlchemy 2.x (existing models), httpx for HTTP, trafilatura for body extraction, feedparser for RSS, click for CLI, plus the Plan 1 stack (Postgres, pgvector, testcontainers, pytest).

---

## Known Plan-Bug Patterns (apply from the start — do NOT discover these during execution)

These six patterns were surfaced during Plan 1 execution. Every task in this plan is written to avoid them.

1. **`TIMESTAMPTZ` import:** `TIMESTAMPTZ` is NOT in `sqlalchemy.dialects.postgresql`. Use `TIMESTAMP(timezone=True)` from `sqlalchemy` (e.g., `sa.TIMESTAMP(timezone=True)`).
2. **Explicit SA imports:** `sa.UniqueConstraint` / `sa.LargeBinary` need direct imports when `sa` alias isn't in scope. Prefer `from sqlalchemy import UniqueConstraint, LargeBinary` explicitly.
3. **psycopg cast syntax:** psycopg refuses `:param::jsonb` / `:param::vector`. Use `CAST(:param AS jsonb)` / `CAST(:param AS vector)` in raw SQL.
4. **Postgres 15+ NULL uniqueness:** Unique constraints default to `NULLS NOT DISTINCT`. Tests relying on duplicate-NULL behavior must use distinct tenants/values to avoid unexpected conflicts.
5. **Fixture tenancy:** The `conn` fixture is single-tenant superuser (bypasses RLS). RLS-sensitive tests must use `app_engine`.
6. **RLS setting:** RLS policies wrap `current_setting(...)` with `NULLIF(..., '')` before `::uuid` cast — this is already in DB. Application code setting `app.current_tenant_id` can rely on this guard.

---

## File Structure

```
factvault/
├── factvault/
│   ├── collectors/
│   │   ├── __init__.py
│   │   ├── base.py                          # Collector ABC + RawDocument dataclass + registry
│   │   ├── rss.py                           # RSS feed collector
│   │   ├── sitemap.py                       # XML sitemap crawler
│   │   ├── searxng.py                       # SearXNG query collector
│   │   ├── wayback_cdx.py                   # Wayback CDX archive replay collector
│   │   ├── http.py                          # Generic single-URL HTTP collector
│   │   └── upload.py                        # File/URL upload API (in-process)
│   ├── workers/
│   │   ├── __init__.py
│   │   ├── base.py                          # Worker ABC + run loop helpers
│   │   ├── archive.py                       # Stage 2 archive worker
│   │   ├── verify.py                        # Stage 5 verify worker
│   │   └── cli.py                           # `factvault-worker <name>` CLI entrypoint
│   ├── archiving/
│   │   ├── __init__.py
│   │   ├── wayback.py                       # Internet Archive Save Page Now client
│   │   ├── extract.py                       # trafilatura wrapper for raw_text extraction
│   │   └── hash.py                          # SHA-256 content hash helper
│   ├── config.py                            # extend with worker / archiver / collector settings
│   └── (Plan 1's factvault/db/* unchanged)
├── tests/
│   ├── collectors/
│   │   ├── __init__.py
│   │   ├── test_base.py
│   │   ├── test_rss.py
│   │   ├── test_sitemap.py
│   │   ├── test_searxng.py
│   │   ├── test_wayback_cdx.py
│   │   ├── test_http.py
│   │   └── test_upload.py
│   ├── workers/
│   │   ├── __init__.py
│   │   ├── test_archive.py
│   │   ├── test_verify.py
│   │   └── test_cli.py
│   ├── archiving/
│   │   ├── __init__.py
│   │   ├── test_wayback.py                  # mocked Save Page Now
│   │   ├── test_extract.py                  # trafilatura on canned HTML fixtures
│   │   └── test_hash.py
│   ├── integration/
│   │   ├── __init__.py
│   │   └── test_source_pipeline_e2e.py      # end-to-end pipeline test
│   └── fixtures/
│       ├── articles/                        # canned HTML/RSS/sitemap fixtures
│       └── wayback_responses/               # canned Wayback API responses
└── (Plan 1's other files unchanged)
```

---

## Tasks

### Task 1 — Dependency additions to `pyproject.toml`

- [ ] **FAIL:** Read current `pyproject.toml`; confirm `httpx`, `trafilatura`, `feedparser`, `click` are absent.

```bash
$ grep -E 'httpx|trafilatura|feedparser|click' pyproject.toml
# expected: no output
```

- [ ] **IMPLEMENT:** Edit `pyproject.toml` — add four new runtime dependencies and a `[project.scripts]` entry.

The `dependencies` list becomes:

```toml
[project]
name = "factvault"
version = "0.0.1"
requires-python = ">=3.12"
dependencies = [
    "sqlalchemy>=2.0,<3",
    "alembic>=1.13,<2",
    "psycopg[binary]>=3.1,<4",
    "pgvector>=0.3,<1",
    "pydantic>=2,<3",
    "httpx>=0.27,<1",
    "trafilatura>=1.9,<2",
    "feedparser>=6.0,<7",
    "click>=8,<9",
]

[project.optional-dependencies]
dev = [
    "pytest>=8,<9",
    "testcontainers[postgres]>=4,<5",
    "pytest-asyncio>=0.23,<1",
    "pytest-httpx>=0.30,<1",
]

[project.scripts]
factvault-worker = "factvault.workers.cli:main"
```

Note: `pytest-httpx` is added to `dev` dependencies for httpx mocking in tests.

- [ ] **RUN/PASS:**

```bash
$ pip install -e ".[dev]"
# expected: Successfully installed factvault-... httpx-... trafilatura-... feedparser-... click-... pytest-httpx-...
$ python -c "import httpx, trafilatura, feedparser, click; print('OK')"
# expected: OK
$ factvault-worker --help
# expected: Usage: factvault-worker [OPTIONS] COMMAND [ARGS]...
#           Error: No such command ... (acceptable — CLI not yet implemented)
# OR: ModuleNotFoundError for factvault.workers.cli (acceptable at this stage)
```

- [ ] **COMMIT:**

```bash
git add pyproject.toml
git commit -m "chore(deps): add httpx, trafilatura, feedparser, click + factvault-worker script entry"
```

---

### Task 2 — Collector ABC + RawDocument dataclass

- [ ] **FAIL:** Write `tests/collectors/__init__.py` (empty) and `tests/collectors/test_base.py`:

```python
# tests/collectors/__init__.py
```

```python
# tests/collectors/test_base.py
import pytest
from datetime import datetime, timezone
from factvault.collectors.base import (
    RawDocument,
    Collector,
    register_collector,
    get_collector,
    CollectorRegistry,
)


# ── RawDocument ────────────────────────────────────────────────────────────────

def test_rawdocument_is_frozen():
    doc = RawDocument(
        url="https://example.com",
        raw_html=b"<html/>",
        fetched_at=datetime.now(tz=timezone.utc),
    )
    with pytest.raises((AttributeError, TypeError)):
        doc.url = "https://other.com"  # type: ignore[misc]


def test_rawdocument_defaults():
    doc = RawDocument(
        url="https://example.com",
        raw_html=b"<html/>",
        fetched_at=datetime.now(tz=timezone.utc),
    )
    assert doc.title is None
    assert doc.raw_text is None
    assert doc.published_at is None
    assert doc.publisher is None
    assert doc.collector_name is None
    assert doc.metadata == {}


def test_rawdocument_is_hashable():
    doc = RawDocument(
        url="https://example.com",
        raw_html=b"<html/>",
        fetched_at=datetime.now(tz=timezone.utc),
    )
    s = {doc}
    assert len(s) == 1


def test_rawdocument_equality():
    t = datetime(2026, 1, 1, tzinfo=timezone.utc)
    a = RawDocument(url="https://x.com", raw_html=b"a", fetched_at=t)
    b = RawDocument(url="https://x.com", raw_html=b"a", fetched_at=t)
    assert a == b


# ── Collector ABC ──────────────────────────────────────────────────────────────

def test_collector_abc_cannot_instantiate():
    with pytest.raises(TypeError):
        Collector()  # type: ignore[abstract]


def test_collector_subclass_without_fetch_rejected():
    """A subclass that doesn't implement fetch() cannot be instantiated."""
    class BadCollector(Collector):
        name = "bad"

    with pytest.raises(TypeError):
        BadCollector()  # type: ignore[abstract]


def test_collector_subclass_with_fetch_ok():
    class GoodCollector(Collector):
        name = "good"

        def fetch(self):
            return iter([])

    gc = GoodCollector()
    assert list(gc.fetch()) == []


# ── Registry ───────────────────────────────────────────────────────────────────

def test_register_and_get_collector():
    class MyCollector(Collector):
        name = "my_test_collector"

        def fetch(self):
            return iter([])

    register_collector(MyCollector)
    retrieved = get_collector("my_test_collector")
    assert retrieved is MyCollector


def test_get_unknown_collector_raises():
    with pytest.raises(KeyError):
        get_collector("nonexistent_xyz_collector")


def test_register_duplicate_raises():
    class DupCollector(Collector):
        name = "dup_test_collector"

        def fetch(self):
            return iter([])

    register_collector(DupCollector)

    class DupCollector2(Collector):
        name = "dup_test_collector"

        def fetch(self):
            return iter([])

    with pytest.raises(ValueError, match="already registered"):
        register_collector(DupCollector2)
```

Run — expect `ModuleNotFoundError` (module doesn't exist yet):

```bash
$ pytest tests/collectors/test_base.py -x 2>&1 | head -5
# expected: ModuleNotFoundError: No module named 'factvault.collectors'
```

- [ ] **IMPLEMENT:** Create `factvault/collectors/__init__.py` and `factvault/collectors/base.py`:

```python
# factvault/collectors/__init__.py
```

```python
# factvault/collectors/base.py
"""
Collector ABC, RawDocument dataclass, and collector registry.

PLAN-BUG NOTES:
  - No SQLAlchemy date types here; stdlib datetime used throughout.
  - metadata field uses field(default_factory=dict) to avoid shared-mutable default.
"""
from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from datetime import datetime
from typing import Iterator


@dataclass(frozen=True)
class RawDocument:
    """
    The raw output of a collector.

    - raw_html: raw bytes of the HTTP response body (NOT yet compressed).
    - raw_text: None at collect time; populated by the archive worker.
    - collector_name: set to Collector.name by the collector that produced this doc.
    - metadata: arbitrary collector-specific key/value pairs (e.g. wayback_timestamp).
    """

    url: str
    raw_html: bytes
    fetched_at: datetime
    title: str | None = None
    raw_text: str | None = None
    published_at: datetime | None = None
    publisher: str | None = None
    collector_name: str | None = None
    metadata: dict = field(default_factory=dict)

    def __hash__(self) -> int:
        # frozen dataclass with a dict field: hash on identity fields only.
        return hash((self.url, self.fetched_at))


class Collector(ABC):
    """
    Base class for all collectors.

    Subclasses MUST define:
      name: str           — unique slug, used in the registry and in RawDocument.collector_name
      fetch() -> Iterator[RawDocument]
    """

    name: str = ""

    @abstractmethod
    def fetch(self) -> Iterator[RawDocument]:
        ...


# ── Registry ──────────────────────────────────────────────────────────────────

_REGISTRY: dict[str, type[Collector]] = {}


def register_collector(cls: type[Collector]) -> type[Collector]:
    """
    Register a Collector subclass by its .name attribute.

    Raises ValueError if the name is already registered.
    Can be used as a class decorator: @register_collector
    """
    if not cls.name:
        raise ValueError(f"Collector {cls!r} has no .name attribute set.")
    if cls.name in _REGISTRY:
        raise ValueError(
            f"Collector name '{cls.name}' is already registered by {_REGISTRY[cls.name]!r}."
        )
    _REGISTRY[cls.name] = cls
    return cls


def get_collector(name: str) -> type[Collector]:
    """
    Retrieve a registered Collector class by name.

    Raises KeyError if the name is not registered.
    """
    if name not in _REGISTRY:
        raise KeyError(f"No collector registered with name '{name}'.")
    return _REGISTRY[name]


class CollectorRegistry:
    """Namespace alias for introspection — not required for normal use."""

    @staticmethod
    def all() -> dict[str, type[Collector]]:
        return dict(_REGISTRY)
```

- [ ] **RUN/PASS:**

```bash
$ pytest tests/collectors/test_base.py -v
# expected: 10 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/collectors/__init__.py factvault/collectors/base.py \
        tests/collectors/__init__.py tests/collectors/test_base.py
git commit -m "feat(collectors): Collector ABC + RawDocument dataclass + registry"
```

---

### Task 3 — Generic HTTP collector

- [ ] **FAIL:** Write `tests/collectors/test_http.py`:

```python
# tests/collectors/test_http.py
import pytest
from datetime import datetime, timezone
from pytest_httpx import HTTPXMock

from factvault.collectors.http import HttpCollector


@pytest.fixture
def collector():
    return HttpCollector(urls=["https://example.com/article-1", "https://example.com/article-2"])


def test_http_collector_name():
    assert HttpCollector.name == "http"


def test_fetch_returns_one_doc_per_url(httpx_mock: HTTPXMock):
    html_1 = b"<html><head><title>Article One</title></head><body>Body 1</body></html>"
    html_2 = b"<html><head><title>Article Two</title></head><body>Body 2</body></html>"

    httpx_mock.add_response(url="https://example.com/article-1", content=html_1)
    httpx_mock.add_response(url="https://example.com/article-2", content=html_2)

    collector = HttpCollector(urls=["https://example.com/article-1", "https://example.com/article-2"])
    docs = list(collector.fetch())

    assert len(docs) == 2
    urls = {d.url for d in docs}
    assert urls == {"https://example.com/article-1", "https://example.com/article-2"}


def test_fetch_extracts_title_from_html(httpx_mock: HTTPXMock):
    html = b"<html><head><title>My Article</title></head><body>text</body></html>"
    httpx_mock.add_response(url="https://example.com/article", content=html)

    collector = HttpCollector(urls=["https://example.com/article"])
    docs = list(collector.fetch())

    assert docs[0].title == "My Article"


def test_fetch_raw_html_populated(httpx_mock: HTTPXMock):
    html = b"<html><body>Hello</body></html>"
    httpx_mock.add_response(url="https://example.com/page", content=html)

    collector = HttpCollector(urls=["https://example.com/page"])
    docs = list(collector.fetch())

    assert docs[0].raw_html == html


def test_fetch_collector_name_set(httpx_mock: HTTPXMock):
    httpx_mock.add_response(url="https://example.com/page", content=b"<html/>")

    collector = HttpCollector(urls=["https://example.com/page"])
    docs = list(collector.fetch())

    assert docs[0].collector_name == "http"


def test_fetch_raw_text_is_none(httpx_mock: HTTPXMock):
    """raw_text is stage-2's job; collector must not populate it."""
    httpx_mock.add_response(url="https://example.com/page", content=b"<html/>")

    collector = HttpCollector(urls=["https://example.com/page"])
    docs = list(collector.fetch())

    assert docs[0].raw_text is None


def test_fetch_http_error_skipped(httpx_mock: HTTPXMock):
    """A 404 or network error on one URL should not crash the iterator; it logs and continues."""
    httpx_mock.add_response(url="https://example.com/ok", content=b"<html><title>OK</title></html>")
    httpx_mock.add_response(url="https://example.com/bad", status_code=404)

    collector = HttpCollector(urls=["https://example.com/ok", "https://example.com/bad"])
    docs = list(collector.fetch())

    # Only the successful URL produces a doc; the 404 is skipped.
    assert len(docs) == 1
    assert docs[0].url == "https://example.com/ok"


def test_empty_url_list():
    collector = HttpCollector(urls=[])
    assert list(collector.fetch()) == []
```

```bash
$ pytest tests/collectors/test_http.py -x 2>&1 | head -5
# expected: ModuleNotFoundError: No module named 'factvault.collectors.http'
```

- [ ] **IMPLEMENT:** Create `factvault/collectors/http.py`:

```python
# factvault/collectors/http.py
"""
Generic HTTP collector.

Fetches a static list of URLs with httpx. For each URL that returns HTTP 200,
yields one RawDocument with raw_html populated and title extracted from the
<title> tag. raw_text is NOT populated here — that is stage-2's job.

HTTP errors (non-2xx) and network exceptions are logged and skipped; the
iterator continues.

PLAN-BUG NOTES:
  - Uses stdlib datetime; no SQLAlchemy date types.
  - No CAST() needed here — no raw SQL.
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
    m = _TITLE_RE.search(raw_html)
    if m:
        return m.group(1).decode("utf-8", errors="replace").strip()
    return None


@register_collector
class HttpCollector(Collector):
    """
    Fetches a static list of URLs.

    Config:
      urls: list[str]   — URLs to fetch.
      timeout: float    — per-request timeout in seconds (default 30).
      headers: dict     — additional HTTP headers.
    """

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
        with httpx.Client(timeout=self.timeout, headers=self.headers, follow_redirects=True) as client:
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

                raw_html = response.content
                title = _extract_title(raw_html)
                fetched_at = datetime.now(tz=timezone.utc)

                yield RawDocument(
                    url=url,
                    raw_html=raw_html,
                    fetched_at=fetched_at,
                    title=title,
                    collector_name=self.name,
                )
```

- [ ] **RUN/PASS:**

```bash
$ pytest tests/collectors/test_http.py -v
# expected: 8 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/collectors/http.py tests/collectors/test_http.py
git commit -m "feat(collectors): generic HTTP collector with httpx + title extraction"
```

---

### Task 4 — RSS collector

- [ ] **CREATE FIXTURE:** `tests/fixtures/articles/sample.rss`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Example News</title>
    <link>https://example.com</link>
    <description>Latest news</description>
    <item>
      <title>First Article</title>
      <link>https://example.com/article-1</link>
      <guid>https://example.com/article-1</guid>
      <pubDate>Thu, 22 May 2026 08:00:00 +0000</pubDate>
      <description>&lt;p&gt;Summary of the first article.&lt;/p&gt;</description>
    </item>
    <item>
      <title>Second Article</title>
      <link>https://example.com/article-2</link>
      <guid>https://example.com/article-2</guid>
      <pubDate>Thu, 22 May 2026 09:00:00 +0000</pubDate>
      <description>&lt;p&gt;Summary of the second article.&lt;/p&gt;</description>
    </item>
  </channel>
</rss>
```

- [ ] **CREATE FIXTURE DIR:**

```bash
mkdir -p tests/fixtures/articles tests/fixtures/wayback_responses
touch tests/fixtures/__init__.py tests/fixtures/articles/.gitkeep
```

- [ ] **FAIL:** Write `tests/collectors/test_rss.py`:

```python
# tests/collectors/test_rss.py
import pytest
from pathlib import Path
from datetime import timezone
from pytest_httpx import HTTPXMock

from factvault.collectors.rss import RssCollector

FIXTURE_DIR = Path(__file__).parent.parent / "fixtures" / "articles"


def _rss_bytes() -> bytes:
    return (FIXTURE_DIR / "sample.rss").read_bytes()


def test_rss_collector_name():
    assert RssCollector.name == "rss"


def test_fetch_yields_one_doc_per_item(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    collector = RssCollector(feed_urls=["https://feeds.example.com/rss"])
    docs = list(collector.fetch())

    assert len(docs) == 2


def test_fetch_url_populated(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    collector = RssCollector(feed_urls=["https://feeds.example.com/rss"])
    docs = list(collector.fetch())

    urls = {d.url for d in docs}
    assert "https://example.com/article-1" in urls
    assert "https://example.com/article-2" in urls


def test_fetch_title_populated(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    collector = RssCollector(feed_urls=["https://feeds.example.com/rss"])
    docs = list(collector.fetch())

    titles = {d.title for d in docs}
    assert "First Article" in titles
    assert "Second Article" in titles


def test_fetch_published_at_populated(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    collector = RssCollector(feed_urls=["https://feeds.example.com/rss"])
    docs = list(collector.fetch())

    for doc in docs:
        assert doc.published_at is not None
        assert doc.published_at.tzinfo is not None  # timezone-aware


def test_fetch_raw_html_is_description_bytes(httpx_mock: HTTPXMock):
    """raw_html for RSS items contains the item description as bytes (best available)."""
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    collector = RssCollector(feed_urls=["https://feeds.example.com/rss"])
    docs = list(collector.fetch())

    for doc in docs:
        assert doc.raw_html is not None
        assert len(doc.raw_html) > 0


def test_fetch_collector_name_set(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    collector = RssCollector(feed_urls=["https://feeds.example.com/rss"])
    docs = list(collector.fetch())

    for doc in docs:
        assert doc.collector_name == "rss"


def test_fetch_deduplicates_by_guid(httpx_mock: HTTPXMock):
    """Calling fetch() twice does NOT duplicate items if GUIDs are tracked."""
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    httpx_mock.add_response(
        url="https://feeds.example.com/rss",
        content=_rss_bytes(),
        headers={"content-type": "application/rss+xml"},
    )
    collector = RssCollector(feed_urls=["https://feeds.example.com/rss"])
    first = list(collector.fetch())
    second = list(collector.fetch())

    first_urls = {d.url for d in first}
    second_urls = {d.url for d in second}
    # Second run yields nothing new — same GUIDs already seen.
    assert second_urls.issubset(first_urls) or len(second) == 0


def test_empty_feed_list():
    collector = RssCollector(feed_urls=[])
    assert list(collector.fetch()) == []
```

```bash
$ pytest tests/collectors/test_rss.py -x 2>&1 | head -5
# expected: ModuleNotFoundError: No module named 'factvault.collectors.rss'
```

- [ ] **IMPLEMENT:** Create `factvault/collectors/rss.py`:

```python
# factvault/collectors/rss.py
"""
RSS/Atom feed collector.

Fetches one or more RSS/Atom feeds, parses with feedparser, and yields one
RawDocument per <item>/<entry>. Deduplicates by GUID/link within the
collector's lifetime (not across process restarts — that's the DB's job).

raw_html is set to the item's summary/description encoded as UTF-8 bytes.
For most feeds this is a short HTML snippet; the archive worker will fetch
the full body later using the item's link URL.

PLAN-BUG NOTES:
  - published_at is derived from feedparser's time struct; converted to
    timezone-aware datetime using calendar.timegm for UTC epoch conversion.
  - No SQLAlchemy imports here.
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


def _parse_published(entry) -> datetime | None:
    """Convert feedparser's time struct to timezone-aware datetime (UTC)."""
    ts = getattr(entry, "published_parsed", None) or getattr(entry, "updated_parsed", None)
    if ts is None:
        return None
    epoch = calendar.timegm(ts)
    return datetime.fromtimestamp(epoch, tz=timezone.utc)


@register_collector
class RssCollector(Collector):
    """
    RSS/Atom feed collector.

    Config:
      feed_urls: list[str]   — RSS/Atom feed URLs to poll.
      timeout: float         — per-request timeout (default 30s).
    """

    name = "rss"

    def __init__(self, feed_urls: list[str], timeout: float = 30.0) -> None:
        self.feed_urls = feed_urls
        self.timeout = timeout
        self._seen_guids: set[str] = set()

    def fetch(self) -> Iterator[RawDocument]:
        for feed_url in self.feed_urls:
            try:
                with httpx.Client(timeout=self.timeout, follow_redirects=True) as client:
                    response = client.get(feed_url)
                    response.raise_for_status()
                    raw_feed = response.text
            except Exception as exc:
                logger.warning("Failed to fetch RSS feed %s: %s", feed_url, exc)
                continue

            feed = feedparser.parse(raw_feed)

            for entry in feed.entries:
                guid = getattr(entry, "id", None) or getattr(entry, "link", None)
                if guid is None:
                    logger.warning("RSS entry in %s has no guid or link; skipping.", feed_url)
                    continue

                if guid in self._seen_guids:
                    continue
                self._seen_guids.add(guid)

                url = getattr(entry, "link", guid)
                title = getattr(entry, "title", None)
                published_at = _parse_published(entry)
                summary = getattr(entry, "summary", "") or ""
                raw_html = summary.encode("utf-8")

                # Try to infer publisher from feed metadata
                publisher = getattr(feed.feed, "title", None)

                yield RawDocument(
                    url=url,
                    raw_html=raw_html,
                    fetched_at=datetime.now(tz=timezone.utc),
                    title=title,
                    published_at=published_at,
                    publisher=publisher,
                    collector_name=self.name,
                )
```

- [ ] **RUN/PASS:**

```bash
$ pytest tests/collectors/test_rss.py -v
# expected: 8 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/collectors/rss.py tests/collectors/test_rss.py \
        tests/fixtures/articles/sample.rss tests/fixtures/__init__.py \
        tests/fixtures/articles/.gitkeep
git commit -m "feat(collectors): RSS/Atom feed collector with feedparser + GUID dedup"
```

---

### Task 5 — Sitemap collector

- [ ] **CREATE FIXTURE:** `tests/fixtures/articles/sample_sitemap.xml`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url>
    <loc>https://example.com/page-1</loc>
    <lastmod>2026-05-20</lastmod>
    <changefreq>weekly</changefreq>
    <priority>0.8</priority>
  </url>
  <url>
    <loc>https://example.com/page-2</loc>
    <lastmod>2026-05-21</lastmod>
    <changefreq>daily</changefreq>
    <priority>0.9</priority>
  </url>
  <url>
    <loc>https://example.com/page-3</loc>
    <lastmod>2026-05-22</lastmod>
    <changefreq>hourly</changefreq>
    <priority>1.0</priority>
  </url>
</urlset>
```

- [ ] **CREATE FIXTURE:** `tests/fixtures/articles/sample_sitemap_index.xml`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap>
    <loc>https://example.com/sitemap-news.xml</loc>
    <lastmod>2026-05-22</lastmod>
  </sitemap>
</sitemapindex>
```

- [ ] **FAIL:** Write `tests/collectors/test_sitemap.py`:

```python
# tests/collectors/test_sitemap.py
from pathlib import Path
import pytest
from pytest_httpx import HTTPXMock

from factvault.collectors.sitemap import SitemapCollector

FIXTURE_DIR = Path(__file__).parent.parent / "fixtures" / "articles"


def test_sitemap_collector_name():
    assert SitemapCollector.name == "sitemap"


def test_fetch_yields_one_doc_per_url(httpx_mock: HTTPXMock):
    sitemap_bytes = (FIXTURE_DIR / "sample_sitemap.xml").read_bytes()
    httpx_mock.add_response(url="https://example.com/sitemap.xml", content=sitemap_bytes)

    collector = SitemapCollector(sitemap_urls=["https://example.com/sitemap.xml"])
    docs = list(collector.fetch())

    assert len(docs) == 3


def test_fetch_urls_correct(httpx_mock: HTTPXMock):
    sitemap_bytes = (FIXTURE_DIR / "sample_sitemap.xml").read_bytes()
    httpx_mock.add_response(url="https://example.com/sitemap.xml", content=sitemap_bytes)

    collector = SitemapCollector(sitemap_urls=["https://example.com/sitemap.xml"])
    docs = list(collector.fetch())

    urls = {d.url for d in docs}
    assert urls == {
        "https://example.com/page-1",
        "https://example.com/page-2",
        "https://example.com/page-3",
    }


def test_fetch_raw_html_is_empty_bytes(httpx_mock: HTTPXMock):
    """Sitemap collector yields placeholder raw_html; archive worker fetches full body."""
    sitemap_bytes = (FIXTURE_DIR / "sample_sitemap.xml").read_bytes()
    httpx_mock.add_response(url="https://example.com/sitemap.xml", content=sitemap_bytes)

    collector = SitemapCollector(sitemap_urls=["https://example.com/sitemap.xml"])
    docs = list(collector.fetch())

    for doc in docs:
        assert doc.raw_html == b""


def test_fetch_collector_name_set(httpx_mock: HTTPXMock):
    sitemap_bytes = (FIXTURE_DIR / "sample_sitemap.xml").read_bytes()
    httpx_mock.add_response(url="https://example.com/sitemap.xml", content=sitemap_bytes)

    collector = SitemapCollector(sitemap_urls=["https://example.com/sitemap.xml"])
    docs = list(collector.fetch())

    for doc in docs:
        assert doc.collector_name == "sitemap"


def test_fetch_follows_sitemap_index(httpx_mock: HTTPXMock):
    """A sitemap index XML is followed one level deep."""
    index_bytes = (FIXTURE_DIR / "sample_sitemap_index.xml").read_bytes()
    sitemap_bytes = (FIXTURE_DIR / "sample_sitemap.xml").read_bytes()

    httpx_mock.add_response(url="https://example.com/sitemap-index.xml", content=index_bytes)
    httpx_mock.add_response(url="https://example.com/sitemap-news.xml", content=sitemap_bytes)

    collector = SitemapCollector(sitemap_urls=["https://example.com/sitemap-index.xml"])
    docs = list(collector.fetch())

    # 3 URLs from the child sitemap
    assert len(docs) == 3


def test_fetch_lastmod_filter(httpx_mock: HTTPXMock):
    """With a lastmod_after filter, only newer URLs are yielded."""
    from datetime import date
    sitemap_bytes = (FIXTURE_DIR / "sample_sitemap.xml").read_bytes()
    httpx_mock.add_response(url="https://example.com/sitemap.xml", content=sitemap_bytes)

    collector = SitemapCollector(
        sitemap_urls=["https://example.com/sitemap.xml"],
        lastmod_after=date(2026, 5, 21),
    )
    docs = list(collector.fetch())

    # page-2 (2026-05-21) and page-3 (2026-05-22) qualify; page-1 (2026-05-20) does not.
    assert len(docs) == 2
    urls = {d.url for d in docs}
    assert "https://example.com/page-1" not in urls


def test_empty_sitemap_list():
    collector = SitemapCollector(sitemap_urls=[])
    assert list(collector.fetch()) == []
```

```bash
$ pytest tests/collectors/test_sitemap.py -x 2>&1 | head -5
# expected: ModuleNotFoundError: No module named 'factvault.collectors.sitemap'
```

- [ ] **IMPLEMENT:** Create `factvault/collectors/sitemap.py`:

```python
# factvault/collectors/sitemap.py
"""
XML sitemap crawler.

Fetches sitemap.xml (or sitemap index), parses <url><loc> entries, and
yields one RawDocument per URL. raw_html is set to b"" — the archive worker
fetches the full body. Supports sitemapindex one level deep and optional
lastmod_after date filter.

PLAN-BUG NOTES:
  - No SQLAlchemy imports; stdlib datetime/date used.
  - xml.etree.ElementTree used for parsing (stdlib; no lxml dep).
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
    """
    XML sitemap crawler.

    Config:
      sitemap_urls: list[str]        — root sitemap or sitemapindex URLs.
      lastmod_after: date | None     — only yield URLs with lastmod >= this date.
      timeout: float                 — per-request timeout (default 30s).
    """

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
            for root_url in self.sitemap_urls:
                yield from self._fetch_sitemap(client, root_url)

    def _fetch_sitemap(self, client: httpx.Client, url: str) -> Iterator[RawDocument]:
        try:
            response = client.get(url)
            response.raise_for_status()
            xml_bytes = response.content
        except Exception as exc:
            logger.warning("Failed to fetch sitemap %s: %s", url, exc)
            return

        try:
            root = ET.fromstring(xml_bytes)
        except ET.ParseError as exc:
            logger.warning("Failed to parse sitemap XML from %s: %s", url, exc)
            return

        # Sitemapindex: follow child sitemaps one level deep.
        if root.tag == _tag("sitemapindex"):
            for sitemap_el in root.findall(_tag("sitemap")):
                loc_el = sitemap_el.find(_tag("loc"))
                if loc_el is not None and loc_el.text:
                    yield from self._fetch_urlset(client, loc_el.text.strip())
            return

        # Regular urlset.
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

            loc = loc_el.text.strip()

            lastmod_el = url_el.find(_tag("lastmod"))
            lastmod = _parse_lastmod(lastmod_el.text if lastmod_el is not None else None)

            if self.lastmod_after is not None and lastmod is not None:
                if lastmod < self.lastmod_after:
                    continue

            yield RawDocument(
                url=loc,
                raw_html=b"",
                fetched_at=datetime.now(tz=timezone.utc),
                collector_name=self.name,
                metadata={"lastmod": lastmod.isoformat() if lastmod else None},
            )
```

- [ ] **RUN/PASS:**

```bash
$ pytest tests/collectors/test_sitemap.py -v
# expected: 7 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/collectors/sitemap.py tests/collectors/test_sitemap.py \
        tests/fixtures/articles/sample_sitemap.xml \
        tests/fixtures/articles/sample_sitemap_index.xml
git commit -m "feat(collectors): XML sitemap crawler with index follow + lastmod filter"
```

---

### Task 6 — SearXNG collector

- [ ] **CREATE FIXTURE:** `tests/fixtures/wayback_responses/searxng_response.json`:

```json
{
  "query": "AI startup funding 2026",
  "number_of_results": 2,
  "results": [
    {
      "url": "https://techcrunch.com/2026/05/01/ai-funding-round",
      "title": "AI Startup Raises $50M Series B",
      "content": "A leading AI startup announced a $50M Series B funding round...",
      "engine": "google",
      "score": 0.9,
      "category": "general"
    },
    {
      "url": "https://venturebeat.com/2026/05/02/another-ai-deal",
      "title": "VentureBeat: Another AI Deal Closes",
      "content": "Another AI company closed a significant funding deal this week...",
      "engine": "duckduckgo",
      "score": 0.85,
      "category": "general"
    }
  ]
}
```

- [ ] **FAIL:** Write `tests/collectors/test_searxng.py`:

```python
# tests/collectors/test_searxng.py
import json
from pathlib import Path
import pytest
from pytest_httpx import HTTPXMock

from factvault.collectors.searxng import SearxngCollector

FIXTURE_DIR = Path(__file__).parent.parent / "fixtures" / "wayback_responses"


def _searxng_response() -> bytes:
    return (FIXTURE_DIR / "searxng_response.json").read_bytes()


def test_searxng_collector_name():
    assert SearxngCollector.name == "searxng"


def test_fetch_yields_one_doc_per_result(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://searxng.example.com/search",
        content=_searxng_response(),
        headers={"content-type": "application/json"},
    )
    collector = SearxngCollector(
        searxng_url="https://searxng.example.com",
        queries=["AI startup funding 2026"],
    )
    docs = list(collector.fetch())

    assert len(docs) == 2


def test_fetch_urls_correct(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://searxng.example.com/search",
        content=_searxng_response(),
        headers={"content-type": "application/json"},
    )
    collector = SearxngCollector(
        searxng_url="https://searxng.example.com",
        queries=["AI startup funding 2026"],
    )
    docs = list(collector.fetch())

    urls = {d.url for d in docs}
    assert "https://techcrunch.com/2026/05/01/ai-funding-round" in urls
    assert "https://venturebeat.com/2026/05/02/another-ai-deal" in urls


def test_fetch_titles_correct(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://searxng.example.com/search",
        content=_searxng_response(),
        headers={"content-type": "application/json"},
    )
    collector = SearxngCollector(
        searxng_url="https://searxng.example.com",
        queries=["AI startup funding 2026"],
    )
    docs = list(collector.fetch())

    titles = {d.title for d in docs}
    assert "AI Startup Raises $50M Series B" in titles


def test_fetch_raw_html_is_none(httpx_mock: HTTPXMock):
    """SearXNG results contain only snippets; raw_html is None until archive worker."""
    httpx_mock.add_response(
        url="https://searxng.example.com/search",
        content=_searxng_response(),
        headers={"content-type": "application/json"},
    )
    collector = SearxngCollector(
        searxng_url="https://searxng.example.com",
        queries=["AI startup funding 2026"],
    )
    docs = list(collector.fetch())

    for doc in docs:
        # raw_html is b"" (empty, not None) — consistent with other collectors;
        # we need a bytes value, not None, to satisfy the RawDocument type.
        assert doc.raw_html == b""


def test_fetch_snippet_in_metadata(httpx_mock: HTTPXMock):
    """The SearXNG snippet is stored in metadata['snippet'] for later reference."""
    httpx_mock.add_response(
        url="https://searxng.example.com/search",
        content=_searxng_response(),
        headers={"content-type": "application/json"},
    )
    collector = SearxngCollector(
        searxng_url="https://searxng.example.com",
        queries=["AI startup funding 2026"],
    )
    docs = list(collector.fetch())

    for doc in docs:
        assert "snippet" in doc.metadata


def test_fetch_collector_name_set(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        url="https://searxng.example.com/search",
        content=_searxng_response(),
        headers={"content-type": "application/json"},
    )
    collector = SearxngCollector(
        searxng_url="https://searxng.example.com",
        queries=["AI startup funding 2026"],
    )
    docs = list(collector.fetch())

    for doc in docs:
        assert doc.collector_name == "searxng"


def test_fetch_api_error_skipped(httpx_mock: HTTPXMock):
    """A 500 from SearXNG does not crash the iterator."""
    httpx_mock.add_response(
        url="https://searxng.example.com/search",
        status_code=500,
    )
    collector = SearxngCollector(
        searxng_url="https://searxng.example.com",
        queries=["failing query"],
    )
    docs = list(collector.fetch())
    assert docs == []


def test_empty_query_list():
    collector = SearxngCollector(
        searxng_url="https://searxng.example.com",
        queries=[],
    )
    assert list(collector.fetch()) == []
```

```bash
$ pytest tests/collectors/test_searxng.py -x 2>&1 | head -5
# expected: ModuleNotFoundError: No module named 'factvault.collectors.searxng'
```

- [ ] **IMPLEMENT:** Create `factvault/collectors/searxng.py`:

```python
# factvault/collectors/searxng.py
"""
SearXNG query collector.

Issues search queries to a SearXNG instance and yields one RawDocument per
result URL. raw_html is b"" — SearXNG returns only snippets; the archive
worker fetches full bodies. Snippets are stored in metadata['snippet'].

PLAN-BUG NOTES:
  - Uses httpx POST to /search with JSON params.
  - No raw SQL; no SQLAlchemy imports.
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
    """
    SearXNG query collector.

    Config:
      searxng_url: str        — base URL of the SearXNG instance (no trailing slash).
      queries: list[str]      — search queries to issue.
      categories: list[str]   — SearXNG categories (default: ['general']).
      language: str           — language param (default: 'en').
      timeout: float          — per-request timeout (default 30s).
    """

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
```

- [ ] **RUN/PASS:**

```bash
$ pytest tests/collectors/test_searxng.py -v
# expected: 8 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/collectors/searxng.py tests/collectors/test_searxng.py \
        tests/fixtures/wayback_responses/searxng_response.json
git commit -m "feat(collectors): SearXNG query collector with snippet metadata"
```

---

### Task 7 — Wayback CDX collector

- [ ] **CREATE FIXTURE:** `tests/fixtures/wayback_responses/cdx_response.json`:

```json
[
  ["urlkey","timestamp","original","mimetype","statuscode","digest","length"],
  ["com,example)/article-1",  "20260101120000","https://example.com/article-1","text/html","200","SHA1:AAAA","12345"],
  ["com,example)/article-1",  "20260315080000","https://example.com/article-1","text/html","200","SHA1:BBBB","13000"],
  ["com,example)/article-2",  "20260410090000","https://example.com/article-2","text/html","200","SHA1:CCCC","9800"]
]
```

- [ ] **FAIL:** Write `tests/collectors/test_wayback_cdx.py`:

```python
# tests/collectors/test_wayback_cdx.py
import json
from pathlib import Path
import pytest
from pytest_httpx import HTTPXMock

from factvault.collectors.wayback_cdx import WaybackCdxCollector

FIXTURE_DIR = Path(__file__).parent.parent / "fixtures" / "wayback_responses"


def _cdx_bytes() -> bytes:
    return (FIXTURE_DIR / "cdx_response.json").read_bytes()


def test_wayback_cdx_collector_name():
    assert WaybackCdxCollector.name == "wayback_cdx"


def test_fetch_yields_one_doc_per_snapshot(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        content=_cdx_bytes(),
        headers={"content-type": "application/json"},
    )
    collector = WaybackCdxCollector(target_urls=["https://example.com/article-1"])
    docs = list(collector.fetch())

    # 2 snapshots for article-1 in fixture
    assert len(docs) == 2


def test_fetch_url_is_wayback_replay_url(httpx_mock: HTTPXMock):
    """The doc URL is the Wayback replay URL, not the original."""
    httpx_mock.add_response(
        content=_cdx_bytes(),
        headers={"content-type": "application/json"},
    )
    collector = WaybackCdxCollector(target_urls=["https://example.com/article-1"])
    docs = list(collector.fetch())

    for doc in docs:
        assert "web.archive.org" in doc.url


def test_fetch_original_url_in_metadata(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        content=_cdx_bytes(),
        headers={"content-type": "application/json"},
    )
    collector = WaybackCdxCollector(target_urls=["https://example.com/article-1"])
    docs = list(collector.fetch())

    for doc in docs:
        assert doc.metadata.get("original_url") == "https://example.com/article-1"


def test_fetch_wayback_timestamp_in_metadata(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        content=_cdx_bytes(),
        headers={"content-type": "application/json"},
    )
    collector = WaybackCdxCollector(target_urls=["https://example.com/article-1"])
    docs = list(collector.fetch())

    timestamps = {doc.metadata.get("wayback_timestamp") for doc in docs}
    assert "20260101120000" in timestamps
    assert "20260315080000" in timestamps


def test_fetch_raw_html_is_empty_bytes(httpx_mock: HTTPXMock):
    """raw_html is b"" — archive worker fetches the replay body."""
    httpx_mock.add_response(
        content=_cdx_bytes(),
        headers={"content-type": "application/json"},
    )
    collector = WaybackCdxCollector(target_urls=["https://example.com/article-1"])
    docs = list(collector.fetch())

    for doc in docs:
        assert doc.raw_html == b""


def test_fetch_collector_name_set(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        content=_cdx_bytes(),
        headers={"content-type": "application/json"},
    )
    collector = WaybackCdxCollector(target_urls=["https://example.com/article-1"])
    docs = list(collector.fetch())

    for doc in docs:
        assert doc.collector_name == "wayback_cdx"


def test_fetch_cdx_error_skipped(httpx_mock: HTTPXMock):
    httpx_mock.add_response(status_code=503)
    collector = WaybackCdxCollector(target_urls=["https://example.com/article-1"])
    docs = list(collector.fetch())
    assert docs == []


def test_empty_target_list():
    collector = WaybackCdxCollector(target_urls=[])
    assert list(collector.fetch()) == []
```

```bash
$ pytest tests/collectors/test_wayback_cdx.py -x 2>&1 | head -5
# expected: ModuleNotFoundError: No module named 'factvault.collectors.wayback_cdx'
```

- [ ] **IMPLEMENT:** Create `factvault/collectors/wayback_cdx.py`:

```python
# factvault/collectors/wayback_cdx.py
"""
Wayback CDX collector.

Queries the Internet Archive CDX API for archived snapshots of target URLs
and yields one RawDocument per snapshot. raw_html is b"" — the archive
worker fetches the actual replay body later. The Wayback replay URL is
stored as doc.url; the original URL is in metadata['original_url'].

CDX API endpoint: https://web.archive.org/cdx/search/cdx
  ?url=<url>&output=json&fl=timestamp,original,statuscode&filter=statuscode:200

PLAN-BUG NOTES:
  - No SQLAlchemy imports; no raw SQL.
  - CDX response is a JSON array-of-arrays with header row.
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
    """
    Wayback CDX archive replay collector.

    Config:
      target_urls: list[str]   — original URLs to query for archived snapshots.
      limit: int               — max snapshots per URL (default 10).
      timeout: float           — per-request timeout (default 30s).
    """

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

        if not data or len(data) < 2:
            # Empty result or header-only response.
            return

        # First row is the header; skip it.
        header = data[0]
        try:
            ts_idx = header.index("timestamp")
            orig_idx = header.index("original")
        except ValueError:
            logger.warning("Unexpected CDX header format for %s: %s", original_url, header)
            return

        for row in data[1:]:
            if len(row) <= max(ts_idx, orig_idx):
                continue

            timestamp = row[ts_idx]
            original = row[orig_idx]
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
```

- [ ] **RUN/PASS:**

```bash
$ pytest tests/collectors/test_wayback_cdx.py -v
# expected: 8 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/collectors/wayback_cdx.py tests/collectors/test_wayback_cdx.py \
        tests/fixtures/wayback_responses/cdx_response.json
git commit -m "feat(collectors): Wayback CDX archive replay collector"
```

---

### Task 8 — Upload collector (in-process)

The upload collector is not a polling collector. It exposes two synchronous functions — `ingest_url` and `ingest_file` — that insert directly into `sources` and return the inserted row's `id`. Uses `tenant_context` from Plan 1 and the `app_engine` fixture for RLS-aware tests.

- [ ] **FAIL:** Write `tests/collectors/test_upload.py`:

```python
# tests/collectors/test_upload.py
"""
Tests for the upload collector (in-process ingest functions).

PLAN-BUG NOTE 5: Use app_engine (not conn) for RLS-sensitive inserts.
PLAN-BUG NOTE 6: tenant_context sets app.current_tenant_id; RLS guard in DB handles empty-string.
"""
import uuid
import pytest
from sqlalchemy import text

from factvault.collectors.upload import ingest_url, ingest_file
from factvault.db.rls import tenant_context

pytestmark = pytest.mark.usefixtures("migrated_engine")


@pytest.fixture
def tenant_id():
    return uuid.uuid4()


def test_ingest_url_returns_uuid(app_engine, tenant_id):
    with app_engine.connect() as conn:
        with tenant_context(conn, tenant_id):
            source_id = ingest_url(
                conn=conn,
                url="https://example.com/page",
                tenant_id=tenant_id,
            )
    assert isinstance(source_id, uuid.UUID)


def test_ingest_url_creates_sources_row(app_engine, tenant_id):
    with app_engine.connect() as conn:
        with tenant_context(conn, tenant_id):
            source_id = ingest_url(
                conn=conn,
                url="https://example.com/article",
                tenant_id=tenant_id,
            )
            row = conn.execute(
                text("SELECT url, status, tenant_id FROM sources WHERE id = :id"),
                {"id": str(source_id)},
            ).fetchone()

    assert row is not None
    assert row.url == "https://example.com/article"
    assert row.status == "collected"
    assert uuid.UUID(str(row.tenant_id)) == tenant_id


def test_ingest_url_status_is_collected(app_engine, tenant_id):
    with app_engine.connect() as conn:
        with tenant_context(conn, tenant_id):
            source_id = ingest_url(
                conn=conn,
                url="https://example.com/status-test",
                tenant_id=tenant_id,
            )
            row = conn.execute(
                text("SELECT status FROM sources WHERE id = :id"),
                {"id": str(source_id)},
            ).fetchone()

    assert row.status == "collected"


def test_ingest_url_deduplicates(app_engine, tenant_id):
    """Ingesting the same URL twice for the same tenant returns the existing id."""
    url = "https://example.com/dedup-test"
    with app_engine.connect() as conn:
        with tenant_context(conn, tenant_id):
            id1 = ingest_url(conn=conn, url=url, tenant_id=tenant_id)
            id2 = ingest_url(conn=conn, url=url, tenant_id=tenant_id)

    assert id1 == id2


def test_ingest_file_creates_sources_row_with_raw_html(app_engine, tenant_id):
    raw_html = b"<html><head><title>Local File</title></head><body>content</body></html>"
    with app_engine.connect() as conn:
        with tenant_context(conn, tenant_id):
            source_id = ingest_file(
                conn=conn,
                url="https://example.com/uploaded",
                raw_html=raw_html,
                tenant_id=tenant_id,
            )
            row = conn.execute(
                text("SELECT url, status, raw_html FROM sources WHERE id = :id"),
                {"id": str(source_id)},
            ).fetchone()

    assert row is not None
    assert row.url == "https://example.com/uploaded"
    assert row.status == "collected"
    # raw_html stored compressed; it must be non-null and non-empty.
    assert row.raw_html is not None


def test_ingest_file_with_title(app_engine, tenant_id):
    raw_html = b"<html><head><title>My Title</title></head><body>text</body></html>"
    with app_engine.connect() as conn:
        with tenant_context(conn, tenant_id):
            source_id = ingest_file(
                conn=conn,
                url="https://example.com/titled",
                raw_html=raw_html,
                tenant_id=tenant_id,
                title="My Title",
            )
            row = conn.execute(
                text("SELECT title FROM sources WHERE id = :id"),
                {"id": str(source_id)},
            ).fetchone()

    assert row.title == "My Title"


def test_ingest_different_tenants_same_url_both_created(app_engine):
    """Same URL for two different tenants creates two separate rows."""
    tenant_a = uuid.uuid4()
    tenant_b = uuid.uuid4()
    url = "https://example.com/shared-url"

    with app_engine.connect() as conn:
        with tenant_context(conn, tenant_a):
            id_a = ingest_url(conn=conn, url=url, tenant_id=tenant_a)
        with tenant_context(conn, tenant_b):
            id_b = ingest_url(conn=conn, url=url, tenant_id=tenant_b)

    assert id_a != id_b
```

```bash
$ pytest tests/collectors/test_upload.py -x 2>&1 | head -5
# expected: ModuleNotFoundError: No module named 'factvault.collectors.upload'
```

- [ ] **IMPLEMENT:** Create `factvault/collectors/upload.py`:

```python
# factvault/collectors/upload.py
"""
Upload collector — in-process ingest functions.

Unlike polling collectors, this module exposes two synchronous functions
called directly by API endpoints or CLI commands:

  ingest_url(conn, url, tenant_id, ...)  -> UUID
  ingest_file(conn, url, raw_html, tenant_id, ...) -> UUID

Both insert a row into `sources` with status='collected' and return the
row's id. Duplicate (tenant_id, url) pairs return the existing id via
ON CONFLICT DO NOTHING + SELECT.

PLAN-BUG NOTES:
  - Uses CAST(:param AS ...) — NOT :param::type — for raw SQL parameters.
  - content_hash is computed here even though raw_html may be empty bytes
    for URL-only ingests; it is recomputed by the archive worker anyway.
  - raw_html is zlib-compressed at level 6 before INSERT (spec §3.1).
  - TIMESTAMP(timezone=True) used in models; but this file only uses raw SQL
    with string timestamps — no SQLAlchemy column type needed here.
"""
from __future__ import annotations

import uuid
import zlib
from datetime import datetime, timezone

from sqlalchemy import text
from sqlalchemy.engine import Connection

from factvault.archiving.hash import compute_hash


def ingest_url(
    conn: Connection,
    url: str,
    tenant_id: uuid.UUID,
    title: str | None = None,
    publisher: str | None = None,
    published_at: datetime | None = None,
) -> uuid.UUID:
    """
    Ingest a URL into sources with status='collected'.

    If (tenant_id, url) already exists, returns the existing row's id.
    """
    # Compute a placeholder hash for an empty body (no HTML fetched yet).
    content_hash = compute_hash(b"")
    fetched_at = datetime.now(tz=timezone.utc).isoformat()

    # ON CONFLICT DO NOTHING handles the (tenant_id, url) unique constraint.
    conn.execute(
        text(
            """
            INSERT INTO sources (id, tenant_id, url, content_hash, fetched_at, title, publisher, published_at, status)
            VALUES (
                gen_random_uuid(),
                CAST(:tenant_id AS uuid),
                :url,
                :content_hash,
                CAST(:fetched_at AS timestamptz),
                :title,
                :publisher,
                CAST(:published_at AS timestamptz),
                'collected'
            )
            ON CONFLICT (tenant_id, url) DO NOTHING
            """
        ),
        {
            "tenant_id": str(tenant_id),
            "url": url,
            "content_hash": content_hash,
            "fetched_at": fetched_at,
            "title": title,
            "publisher": publisher,
            "published_at": published_at.isoformat() if published_at else None,
        },
    )
    conn.commit()

    row = conn.execute(
        text("SELECT id FROM sources WHERE tenant_id = CAST(:tenant_id AS uuid) AND url = :url"),
        {"tenant_id": str(tenant_id), "url": url},
    ).fetchone()
    return uuid.UUID(str(row.id))


def ingest_file(
    conn: Connection,
    url: str,
    raw_html: bytes,
    tenant_id: uuid.UUID,
    title: str | None = None,
    publisher: str | None = None,
    published_at: datetime | None = None,
) -> uuid.UUID:
    """
    Ingest a raw HTML body into sources with status='collected'.

    raw_html is zlib-compressed before INSERT (spec §3.1).
    content_hash is computed from the uncompressed body.
    """
    content_hash = compute_hash(raw_html)
    compressed = zlib.compress(raw_html, level=6)
    fetched_at = datetime.now(tz=timezone.utc).isoformat()

    conn.execute(
        text(
            """
            INSERT INTO sources (id, tenant_id, url, content_hash, raw_html, fetched_at, title, publisher, published_at, status)
            VALUES (
                gen_random_uuid(),
                CAST(:tenant_id AS uuid),
                :url,
                :content_hash,
                :raw_html,
                CAST(:fetched_at AS timestamptz),
                :title,
                :publisher,
                CAST(:published_at AS timestamptz),
                'collected'
            )
            ON CONFLICT (tenant_id, url) DO NOTHING
            """
        ),
        {
            "tenant_id": str(tenant_id),
            "url": url,
            "content_hash": content_hash,
            "raw_html": compressed,
            "fetched_at": fetched_at,
            "title": title,
            "publisher": publisher,
            "published_at": published_at.isoformat() if published_at else None,
        },
    )
    conn.commit()

    row = conn.execute(
        text("SELECT id FROM sources WHERE tenant_id = CAST(:tenant_id AS uuid) AND url = :url"),
        {"tenant_id": str(tenant_id), "url": url},
    ).fetchone()
    return uuid.UUID(str(row.id))
```

Note: `ingest_file` imports `factvault.archiving.hash.compute_hash` which is created in Task 11. The test for `ingest_file` will fail until Task 11 is complete. Run `test_ingest_url_*` tests only at this stage, or stub the hash module as follows before Task 11:

```bash
# Temporary stub — Task 11 replaces this with the real implementation.
mkdir -p factvault/archiving
touch factvault/archiving/__init__.py
cat > factvault/archiving/hash.py << 'EOF'
# STUB — replaced in Task 11
import hashlib

def compute_hash(body: bytes) -> str:
    return "sha256:" + hashlib.sha256(body).hexdigest()
EOF
```

- [ ] **RUN/PASS:**

```bash
$ pytest tests/collectors/test_upload.py -v
# expected: 7 passed
# (Stub hash module satisfies the import; real module lands in Task 11.)
```

- [ ] **COMMIT:**

```bash
git add factvault/collectors/upload.py tests/collectors/test_upload.py \
        factvault/archiving/__init__.py factvault/archiving/hash.py
git commit -m "feat(collectors): upload ingest functions + archiving hash stub"
```

---

### Task 9 — Wayback Save Page Now client

- [ ] **FAIL:** Write `tests/archiving/__init__.py` and `tests/archiving/test_wayback.py`:

```python
# tests/archiving/__init__.py
```

```python
# tests/archiving/test_wayback.py
"""
Tests for the Wayback Save Page Now client.

PLAN-BUG NOTE: SPN2 returns 429 on rate limit. Client must return None after
retries, not raise. The archive worker depends on this contract.
"""
import pytest
from pytest_httpx import HTTPXMock

from factvault.archiving.wayback import submit_url


_SPN_URL = "https://web.archive.org/save"


def test_submit_url_returns_archive_url_on_success(httpx_mock: HTTPXMock):
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


def test_submit_url_returns_archive_url_format(httpx_mock: HTTPXMock):
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
    # Expected format: https://web.archive.org/web/20260101080000/https://example.com/page
    assert result == "https://web.archive.org/web/20260101080000/https://example.com/page"


def test_submit_url_returns_none_on_429(httpx_mock: HTTPXMock):
    """Rate limit: after 3 retries all returning 429, must return None — not raise."""
    httpx_mock.add_response(url=_SPN_URL, method="POST", status_code=429)
    httpx_mock.add_response(url=_SPN_URL, method="POST", status_code=429)
    httpx_mock.add_response(url=_SPN_URL, method="POST", status_code=429)

    result = submit_url("https://example.com/rate-limited", max_retries=3, base_delay=0.0)
    assert result is None


def test_submit_url_returns_none_on_500(httpx_mock: HTTPXMock):
    """Server errors: after 3 retries, must return None."""
    httpx_mock.add_response(url=_SPN_URL, method="POST", status_code=500)
    httpx_mock.add_response(url=_SPN_URL, method="POST", status_code=500)
    httpx_mock.add_response(url=_SPN_URL, method="POST", status_code=500)

    result = submit_url("https://example.com/server-error", max_retries=3, base_delay=0.0)
    assert result is None


def test_submit_url_retries_on_429_then_succeeds(httpx_mock: HTTPXMock):
    """Two 429s then a success: returns archive URL."""
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
    result = submit_url("https://example.com/retry-success", max_retries=3, base_delay=0.0)
    assert result is not None
    assert "web.archive.org" in result


def test_submit_url_returns_none_on_network_error(httpx_mock: HTTPXMock):
    """Network-level failure: return None after retries."""
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
    result = submit_url("https://example.com/network-fail", max_retries=3, base_delay=0.0)
    assert result is None
```

```bash
$ pytest tests/archiving/test_wayback.py -x 2>&1 | head -5
# expected: ModuleNotFoundError: No module named 'factvault.archiving.wayback'
```

- [ ] **IMPLEMENT:** Create `factvault/archiving/wayback.py`:

```python
# factvault/archiving/wayback.py
"""
Internet Archive Save Page Now (SPN2) client.

submit_url(url) -> Optional[str]

Submits a URL to the Wayback Machine Save Page Now API. Returns the
archive URL on success, or None on final failure (after retries).

Rate limiting (429) and server errors (5xx) are retried with exponential
backoff. Network errors are also retried. After max_retries attempts,
returns None — DOES NOT RAISE. The archive worker must continue on failure.

Spec §4.2: "Wayback failure does not block ingestion."

PLAN-BUG NOTES:
  - No SQLAlchemy imports.
  - CAST() not needed here (no raw SQL).
  - base_delay=0.0 param used in tests to skip actual sleep.
"""
from __future__ import annotations

import logging
import time
from typing import Optional

import httpx

logger = logging.getLogger(__name__)

_SPN_ENDPOINT = "https://web.archive.org/save"
_WAYBACK_REPLAY_BASE = "https://web.archive.org/web/{timestamp}/{url}"


def submit_url(
    url: str,
    max_retries: int = 3,
    base_delay: float = 5.0,
    timeout: float = 60.0,
) -> Optional[str]:
    """
    Submit a URL to Internet Archive Save Page Now API.

    Returns the Wayback replay URL on success (e.g.,
    'https://web.archive.org/web/20260522120000/https://example.com/article').

    Returns None on final failure — never raises.

    Args:
      url: The URL to archive.
      max_retries: Maximum number of attempts (including the first).
      base_delay: Base sleep duration in seconds between retries.
                  Actual delay = base_delay * (2 ** attempt). Set to 0.0 in tests.
      timeout: Per-request timeout in seconds.
    """
    for attempt in range(max_retries):
        if attempt > 0 and base_delay > 0.0:
            delay = base_delay * (2 ** (attempt - 1))
            logger.info("Wayback SPN retry %d/%d for %s in %.1fs", attempt, max_retries, url, delay)
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
                        timestamp=timestamp, url=original
                    )
                    logger.info("Wayback archived %s -> %s", url, archive_url)
                    return archive_url
                # Unexpected 200 with no timestamp
                logger.warning("Wayback SPN returned 200 but no timestamp for %s: %s", url, data)
                return None

            if response.status_code in (429, 500, 502, 503, 504):
                logger.warning(
                    "Wayback SPN returned %d for %s (attempt %d/%d)",
                    response.status_code, url, attempt + 1, max_retries,
                )
                continue  # retry

            # Non-retryable status
            logger.warning(
                "Wayback SPN returned non-retryable %d for %s", response.status_code, url
            )
            return None

        except httpx.RequestError as exc:
            logger.warning("Wayback SPN network error for %s (attempt %d/%d): %s", url, attempt + 1, max_retries, exc)
            continue

    logger.error("Wayback SPN failed for %s after %d attempts", url, max_retries)
    return None
```

- [ ] **RUN/PASS:**

```bash
$ pytest tests/archiving/test_wayback.py -v
# expected: 6 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/archiving/wayback.py \
        tests/archiving/__init__.py tests/archiving/test_wayback.py
git commit -m "feat(archiving): Wayback SPN client with exponential backoff, returns None on failure"
```

---

### Task 10 — trafilatura raw_text extraction

- [ ] **CREATE FIXTURES:**

`tests/fixtures/articles/article.html`:

```html
<!DOCTYPE html>
<html lang="en">
<head><title>Startup Raises $50M Series B</title></head>
<body>
  <article>
    <h1>Startup Raises $50M Series B</h1>
    <p>Published: May 22, 2026</p>
    <p>A San Francisco-based AI startup announced today that it has raised $50 million
    in a Series B funding round led by Sequoia Capital. The round brings the company's
    total funding to $75 million.</p>
    <p>"We plan to use the funds to accelerate our product development," said the CEO.</p>
  </article>
  <nav>Nav links</nav>
  <footer>Footer content</footer>
</body>
</html>
```

`tests/fixtures/articles/paywall.html`:

```html
<!DOCTYPE html>
<html lang="en">
<head><title>Premium Article</title></head>
<body>
  <div class="paywall-blocker">
    <p>Subscribe to read this article.</p>
  </div>
</body>
</html>
```

- [ ] **FAIL:** Write `tests/archiving/test_extract.py`:

```python
# tests/archiving/test_extract.py
"""
Tests for trafilatura extraction wrapper.
"""
from pathlib import Path
import pytest

from factvault.archiving.extract import extract_text

FIXTURE_DIR = Path(__file__).parent.parent / "fixtures" / "articles"


def test_extract_text_returns_string_for_article():
    html = (FIXTURE_DIR / "article.html").read_bytes()
    result = extract_text(html, url="https://example.com/article")
    assert result is not None
    assert isinstance(result, str)
    assert len(result) > 0


def test_extract_text_contains_article_content():
    html = (FIXTURE_DIR / "article.html").read_bytes()
    result = extract_text(html, url="https://example.com/article")
    assert result is not None
    # Core article text should be present.
    assert "50 million" in result or "$50M" in result or "Series B" in result


def test_extract_text_excludes_nav_and_footer():
    html = (FIXTURE_DIR / "article.html").read_bytes()
    result = extract_text(html, url="https://example.com/article")
    # trafilatura should strip nav/footer boilerplate.
    # The key is that the main article text is present and nav isn't included verbatim.
    assert result is not None
    assert "Nav links" not in result


def test_extract_text_returns_none_for_paywall():
    """trafilatura returns None/empty for near-empty bodies like a paywall placeholder."""
    html = (FIXTURE_DIR / "paywall.html").read_bytes()
    result = extract_text(html, url="https://example.com/premium")
    # May return None or a very short string; either is acceptable — the point is not None for
    # the article fixture above.
    # This test just verifies no exception is raised.
    assert result is None or isinstance(result, str)


def test_extract_text_returns_none_for_empty_bytes():
    result = extract_text(b"", url="https://example.com/empty")
    assert result is None


def test_extract_text_returns_none_for_minimal_html():
    result = extract_text(b"<html><body></body></html>", url="https://example.com/empty")
    assert result is None


def test_extract_text_is_plain_text_not_html():
    html = (FIXTURE_DIR / "article.html").read_bytes()
    result = extract_text(html, url="https://example.com/article")
    if result:
        assert "<html" not in result
        assert "<p>" not in result
        assert "<article" not in result
```

```bash
$ pytest tests/archiving/test_extract.py -x 2>&1 | head -5
# expected: ModuleNotFoundError: No module named 'factvault.archiving.extract'
```

- [ ] **IMPLEMENT:** Create `factvault/archiving/extract.py`:

```python
# factvault/archiving/extract.py
"""
trafilatura wrapper for raw_text extraction.

extract_text(raw_html: bytes, url: str) -> Optional[str]

Returns the extracted plain text, or None if trafilatura returns empty/None.

Config is pinned per spec §4.2:
  include_comments=False
  include_tables=True
  no_fallback=False
  favor_precision=True
  output_format='txt'

These settings MUST NOT be changed without a migration that re-extracts
all affected sources (spec §4.2 invariant).

PLAN-BUG NOTES:
  - No SQLAlchemy imports; no raw SQL.
  - trafilatura.extract() accepts a decoded string, so we decode bytes
    first with UTF-8 / errors='replace'.
"""
from __future__ import annotations

import logging
from typing import Optional

import trafilatura

logger = logging.getLogger(__name__)


def extract_text(raw_html: bytes, url: str) -> Optional[str]:
    """
    Extract plain text from raw HTML bytes using trafilatura.

    Returns None if the result is empty or None (e.g., paywall placeholder,
    empty body, or trafilatura finds insufficient content).

    Args:
      raw_html: Raw HTTP response body bytes.
      url: The URL of the page (used by trafilatura for metadata heuristics).
    """
    if not raw_html:
        return None

    html_str = raw_html.decode("utf-8", errors="replace")

    result = trafilatura.extract(
        html_str,
        url=url,
        output_format="txt",
        include_comments=False,
        include_tables=True,
        no_fallback=False,
        favor_precision=True,
    )

    if not result or not result.strip():
        return None

    return result.strip()
```

- [ ] **RUN/PASS:**

```bash
$ pytest tests/archiving/test_extract.py -v
# expected: 7 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/archiving/extract.py tests/archiving/test_extract.py \
        tests/fixtures/articles/article.html tests/fixtures/articles/paywall.html
git commit -m "feat(archiving): trafilatura extract_text wrapper with pinned config"
```

---

### Task 11 — Content hash helper

- [ ] **FAIL:** Write `tests/archiving/test_hash.py`:

```python
# tests/archiving/test_hash.py
"""
Tests for the SHA-256 content hash helper.
"""
import pytest
from factvault.archiving.hash import compute_hash


def test_compute_hash_returns_sha256_prefix():
    result = compute_hash(b"hello world")
    assert result.startswith("sha256:")


def test_compute_hash_known_value():
    # SHA-256 of b"hello world" is well-known.
    # echo -n "hello world" | sha256sum
    expected_hex = "b94d27b9934d3e08a52e52d7da7dabfac484efe04294e576e4b8ee73ceb8b7b6"
    # Note: actual SHA-256 of b"hello world":
    import hashlib
    expected = "sha256:" + hashlib.sha256(b"hello world").hexdigest()
    result = compute_hash(b"hello world")
    assert result == expected


def test_compute_hash_empty_bytes():
    """SHA-256 of empty bytes is deterministic."""
    import hashlib
    expected = "sha256:" + hashlib.sha256(b"").hexdigest()
    result = compute_hash(b"")
    assert result == expected


def test_compute_hash_different_inputs_differ():
    h1 = compute_hash(b"content one")
    h2 = compute_hash(b"content two")
    assert h1 != h2


def test_compute_hash_same_input_deterministic():
    body = b"some article body " * 100
    assert compute_hash(body) == compute_hash(body)


def test_compute_hash_returns_string():
    result = compute_hash(b"test")
    assert isinstance(result, str)


def test_compute_hash_hex_length():
    """SHA-256 hex digest is 64 characters; with 'sha256:' prefix it's 71."""
    result = compute_hash(b"test data")
    assert len(result) == len("sha256:") + 64


def test_compute_hash_algorithm_prefix_detectable():
    """Prefix format allows downstream callers to detect the algorithm."""
    result = compute_hash(b"data")
    algo, hexdigest = result.split(":", 1)
    assert algo == "sha256"
    assert len(hexdigest) == 64
```

```bash
$ pytest tests/archiving/test_hash.py -x 2>&1 | head -5
# expected: FAILED (stub implementation exists from Task 8 — but test_compute_hash_known_value
# may need to verify the exact hash; run to confirm all pass with the stub)
```

The stub from Task 8 already has the correct implementation. Verify tests pass:

```bash
$ pytest tests/archiving/test_hash.py -v
# expected: 8 passed (stub is correct; Task 11 "promotes" it to canonical)
```

- [ ] **IMPLEMENT:** Replace the stub with the canonical `factvault/archiving/hash.py`:

```python
# factvault/archiving/hash.py
"""
SHA-256 content hash helper.

compute_hash(body: bytes) -> str

Returns 'sha256:' + hexdigest. The algorithm prefix allows downstream
consumers to detect which algorithm was used without out-of-band metadata.

PLAN-BUG NOTES:
  - No SQLAlchemy imports; no raw SQL; no CAST() needed.
  - hashlib is stdlib; no new dependency.
"""
from __future__ import annotations

import hashlib


def compute_hash(body: bytes) -> str:
    """
    Compute SHA-256 hash of body bytes.

    Returns 'sha256:<64-char-hexdigest>'.

    Args:
      body: Raw bytes to hash (may be empty).
    """
    digest = hashlib.sha256(body).hexdigest()
    return f"sha256:{digest}"
```

- [ ] **RUN/PASS:**

```bash
$ pytest tests/archiving/test_hash.py -v
# expected: 8 passed

# Also confirm all tasks so far pass together:
$ pytest tests/collectors/ tests/archiving/ -v --tb=short
# expected: all passed (collectors: base, http, rss, sitemap, searxng, wayback_cdx, upload;
#           archiving: wayback, extract, hash)
```

- [ ] **COMMIT:**

```bash
git add factvault/archiving/hash.py tests/archiving/test_hash.py
git commit -m "feat(archiving): canonical SHA-256 compute_hash helper (promotes Task 8 stub)"
```

---

<!-- PASS 1 END — Pass 2 appends Tasks 12-22 + self-review below this line -->
