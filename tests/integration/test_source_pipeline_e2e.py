# tests/integration/test_source_pipeline_e2e.py
"""
End-to-end integration test for the source-existence pipeline.

Pipeline under test:
  collect (rss) → archive (ArchiveWorker) → verify (VerifyWorker)

All external HTTP calls are intercepted by pytest-httpx (HTTPXMock).
The test exercises a real migrated Postgres container (testcontainers) so
that schema constraints, RLS policies, and trigger invariants fire as in
production.

KNOWN PLAN-BUG PATTERNS applied:
  #5  conn vs app_engine — all RLS-sensitive reads use app_engine + tenant GUC
  #6  GUC is app.tenant_id (not app.current_tenant_id)
  #8  pytest-httpx / HTTPXMock (not respx)
  #9  RSS config key is feed_urls (RssCollector.__init__ signature)

ADDITIONAL FINDING (production bug, not patched here):
  factvault/collectors/__init__.py does not auto-import concrete collector modules,
  so @register_collector decorators never fire at CLI startup.  Test works around
  this by explicitly importing factvault.collectors.rss before invoking the CLI.
  The equivalent fix in production: add auto-imports to collectors/__init__.py,
  mirroring the pattern in workers/__init__.py.

ARCHITECTURE NOTE — raw_text extraction:
  The RSS collector stores entry.summary.encode() as raw_html.  feedparser returns
  the description's inner HTML as a fragment (without <html>/<body> wrapper).
  trafilatura requires a proper HTML document to extract text; it returns None for
  bare HTML fragments.  This test patches extract_text to return predictable text
  so the pipeline mechanics are tested independently of trafilatura's heuristics.

ARCHITECTURE NOTE — status='live':
  For status='live' the verify worker's re-fetch must return bytes whose sha256
  equals the stored content_hash.  content_hash is computed from the raw bytes
  stored in raw_html at archive time (the feedparser summary bytes).  Therefore
  the verify mock must return the exact same bytes that feedparser stored.
  These bytes are pre-computed in ARTICLE_VERIFY_BODIES below.
"""
from __future__ import annotations

import html as _html_mod
import uuid
from pathlib import Path
from unittest.mock import patch

import feedparser as _feedparser
import pytest
from click.testing import CliRunner
from pytest_httpx import HTTPXMock
from sqlalchemy import text

from factvault.collectors import rss as _rss_module  # noqa: F401 — triggers @register_collector
from factvault.db.rls import tenant_context
from factvault.workers.archive import ArchiveWorker
from factvault.workers.cli import main as worker_main
from factvault.workers.verify import VerifyWorker

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

TENANT_A = uuid.UUID("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
TENANT_B = uuid.UUID("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")

# Unique URL prefix ensures no collision with other test modules.
_BASE = "https://e2e-pipeline.example.com"
FEED_URL = f"{_BASE}/feed.xml"
ARTICLE_URLS = [
    f"{_BASE}/article-1",
    f"{_BASE}/article-2",
    f"{_BASE}/article-3",
]

# RSS description HTML.  feedparser preserves the inner HTML of xml-escaped
# <description> entries.  We use simple <p> snippets so feedparser returns
# them byte-for-byte without normalization.
_ARTICLE_DESC_HTML = [
    "<p>Breaking: AI reaches new milestone in 2026. Scientists confirmed significant progress.</p>",
    "<p>Markets surge on positive economic data. Investors respond to strong indicators.</p>",
    "<p>Scientists discover new exoplanet candidate. Webb telescope findings published.</p>",
]

# Pre-compute exact bytes that feedparser returns as entry.summary.encode('utf-8').
# These are what the collect CLI stores as raw_html, and what content_hash is
# computed from.  The verify re-fetch mock must return these exact bytes to
# get status='live'.
def _compute_verify_bodies() -> list[bytes]:
    """Build a test RSS feed and parse it to capture exact feedparser summary bytes."""
    items = "".join(
        f"<item>"
        f"<title>Article {i + 1}</title>"
        f"<link>{ARTICLE_URLS[i]}</link>"
        f"<guid>e2e-guid-{i + 1}</guid>"
        f"<description>{_html_mod.escape(_ARTICLE_DESC_HTML[i])}</description>"
        f"</item>"
        for i in range(3)
    )
    feed_xml = (
        '<?xml version="1.0" encoding="UTF-8"?>'
        '<rss version="2.0"><channel>'
        "<title>E2E Test Feed</title>"
        "<link>https://e2e-pipeline.example.com</link>"
        + items
        + "</channel></rss>"
    )
    parsed = _feedparser.parse(feed_xml)
    return [entry.summary.encode("utf-8") for entry in parsed.entries]


# ARTICLE_VERIFY_BODIES[i] = exact bytes that feedparser returns for article i.
# Used as the verify-worker re-fetch mock body so hash comparison yields 'live'.
ARTICLE_VERIFY_BODIES: list[bytes] = _compute_verify_bodies()

# Map URL → verify body for easy lookup in mock registration.
ARTICLE_BODIES = dict(zip(ARTICLE_URLS, ARTICLE_VERIFY_BODIES))

WAYBACK_ARCHIVE_URL = (
    "https://web.archive.org/web/20260101000000/"
    "https://e2e-pipeline.example.com"
)

# Predictable raw_text returned by patched extract_text.
_FAKE_RAW_TEXT = "E2E test article text extracted by patched trafilatura."


def _build_rss_feed() -> bytes:
    items = "".join(
        f"<item>"
        f"<title>Article {i + 1}</title>"
        f"<link>{ARTICLE_URLS[i]}</link>"
        f"<guid>e2e-guid-{i + 1}</guid>"
        f"<description>{_html_mod.escape(_ARTICLE_DESC_HTML[i])}</description>"
        f"</item>"
        for i in range(3)
    )
    feed = (
        '<?xml version="1.0" encoding="UTF-8"?>'
        '<rss version="2.0"><channel>'
        "<title>E2E Test Feed</title>"
        "<link>https://e2e-pipeline.example.com</link>"
        + items
        + "</channel></rss>"
    )
    return feed.encode("utf-8")


RSS_FEED = _build_rss_feed()


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _count_sources(app_engine, tenant_id: uuid.UUID, status: str) -> int:
    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, tenant_id):
                row = conn.execute(
                    text(
                        "SELECT count(*) AS n FROM sources "
                        "WHERE tenant_id = :t AND status = :s"
                    ),
                    {"t": str(tenant_id), "s": status},
                ).fetchone()
    return row.n


def _get_archived_sources(app_engine, tenant_id: uuid.UUID):
    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, tenant_id):
                return conn.execute(
                    text(
                        "SELECT id, url, status, content_hash, raw_text, raw_html, "
                        "archive_url, fetched_at "
                        "FROM sources WHERE tenant_id = :t AND status = 'archived'"
                    ),
                    {"t": str(tenant_id)},
                ).fetchall()


def _get_verifications(app_engine, tenant_id: uuid.UUID):
    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, tenant_id):
                return conn.execute(
                    text(
                        "SELECT source_id, status "
                        "FROM source_verifications WHERE tenant_id = :t"
                    ),
                    {"t": str(tenant_id)},
                ).fetchall()


def _get_sources_snapshot(app_engine, tenant_id: uuid.UUID):
    """Return {id: row} for all sources belonging to tenant."""
    with app_engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, tenant_id):
                rows = conn.execute(
                    text(
                        "SELECT id, last_verified_at, raw_text, raw_html "
                        "FROM sources WHERE tenant_id = :t"
                    ),
                    {"t": str(tenant_id)},
                ).fetchall()
    return {row.id: row for row in rows}


def _cleanup_tenant(app_engine, migrated_engine, tenant_id: uuid.UUID) -> None:
    """Remove all rows for a tenant from source_verifications and sources.

    source_verifications is append-only — an INSERT-only trigger blocks DELETE
    for normal users.  We temporarily disable the trigger using the superuser
    migrated_engine, then re-enable it.
    """
    with migrated_engine.connect() as conn:
        conn.execute(
            text(
                "ALTER TABLE source_verifications "
                "DISABLE TRIGGER trg_source_verifications_no_delete"
            )
        )
        conn.execute(
            text("DELETE FROM source_verifications WHERE tenant_id = :t"),
            {"t": str(tenant_id)},
        )
        conn.execute(
            text(
                "ALTER TABLE source_verifications "
                "ENABLE TRIGGER trg_source_verifications_no_delete"
            )
        )
        conn.execute(
            text("DELETE FROM sources WHERE tenant_id = :t"),
            {"t": str(tenant_id)},
        )
        conn.commit()


def _build_config_file(tmp_path: Path, tenant_id: uuid.UUID, feed_url: str) -> str:
    """Write a minimal YAML config and return its path as a string."""
    cfg = tmp_path / f"config_{tenant_id.hex[:8]}.yaml"
    cfg.write_text(
        f"""
tenants:
  - id: {tenant_id}
    name: e2e-test

collectors:
  - name: rss
    config:
      feed_urls:
        - {feed_url}

archive_worker:
  interval_seconds: 60
  batch_size: 50

verify_worker:
  age_threshold_days: 0
  fetch_age_threshold_days: 0
  batch_size: 50
"""
    )
    return str(cfg)


# ---------------------------------------------------------------------------
# Main E2E test
# ---------------------------------------------------------------------------


def test_full_source_pipeline(
    app_engine,
    migrated_engine,
    tmp_path: Path,
    httpx_mock: HTTPXMock,
) -> None:
    """
    Stage 1 (collect rss): 3 sources land with status='collected'.
    Stage 2 (archive --once): all 3 reach status='archived' with raw_text,
      content_hash (starts 'sha256:'), archive_url, and fetched_at set.
    Stage 5 (verify --once): 3 source_verifications rows appear with
      status='live'; sources' last_verified_at is populated.

    Invariants verified post-verify:
    - raw_text and raw_html are UNCHANGED (verify never overwrites captured body).
    - content_hash is UNCHANGED (same bytes re-fetched → same hash).
    """
    config_file = _build_config_file(tmp_path, TENANT_A, FEED_URL)

    # Register HTTP mocks:
    # - RSS feed (1 call by collect)
    # - Article URLs (1 call each by verify re-fetch; archive skips fetch because
    #   raw_html is already populated from the RSS description at collect time)
    httpx_mock.add_response(url=FEED_URL, content=RSS_FEED)
    for url, body in ARTICLE_BODIES.items():
        httpx_mock.add_response(url=url, content=body)  # verify re-fetch

    runner = CliRunner()

    try:
        # ----------------------------------------------------------------
        # Stage 1: Collect
        # ----------------------------------------------------------------
        with patch("factvault.workers.cli.get_engine", return_value=app_engine), \
             patch("factvault.workers.archive.submit_to_wayback",
                   return_value=WAYBACK_ARCHIVE_URL):
            result = runner.invoke(
                worker_main,
                ["collect", "rss", "--config", config_file, "--tenant", str(TENANT_A)],
            )

        assert result.exit_code == 0, (
            f"collect failed (exit {result.exit_code}):\n{result.output}"
        )
        assert _count_sources(app_engine, TENANT_A, "collected") == 3, (
            "Expected 3 collected sources after Stage 1"
        )

        # ----------------------------------------------------------------
        # Stage 2: Archive
        # trafilatura is patched to return predictable text — extract_text
        # is imported and bound in factvault.workers.archive at import time.
        # ----------------------------------------------------------------
        with patch("factvault.workers.archive.submit_to_wayback",
                   return_value=WAYBACK_ARCHIVE_URL), \
             patch("factvault.workers.archive.extract_text",
                   return_value=_FAKE_RAW_TEXT):
            worker = ArchiveWorker()
            code = worker.run(
                {
                    "tenant_id": str(TENANT_A),
                    "once": True,
                    "interval": 60,
                    "engine": app_engine,
                }
            )

        assert code == 0, "archive worker returned non-zero exit code"

        archived = _get_archived_sources(app_engine, TENANT_A)
        assert len(archived) == 3, (
            f"Expected 3 archived sources after Stage 2, got {len(archived)}"
        )

        # Snapshot raw_text and raw_html for invariant check after verify.
        pre_verify_snapshot = {
            row.id: {"raw_text": row.raw_text, "raw_html": row.raw_html,
                     "content_hash": row.content_hash}
            for row in archived
        }

        for row in archived:
            assert row.content_hash is not None, (
                f"Missing content_hash for source {row.id}"
            )
            assert row.content_hash.startswith("sha256:"), (
                f"content_hash for {row.id} does not start with 'sha256:'"
            )
            assert row.raw_text == _FAKE_RAW_TEXT, (
                f"raw_text mismatch for source {row.id}: {row.raw_text!r}"
            )
            assert row.fetched_at is not None, (
                f"fetched_at is None for source {row.id}"
            )
            assert row.archive_url == WAYBACK_ARCHIVE_URL, (
                f"Unexpected archive_url {row.archive_url!r} for source {row.id}"
            )

        # ----------------------------------------------------------------
        # Stage 5: Verify
        # The verify worker re-fetches each article URL.  The mock returns
        # the exact same bytes that feedparser stored as raw_html, so
        # compute_hash(new_bytes) == stored content_hash → status='live'.
        # ----------------------------------------------------------------
        verify_worker = VerifyWorker()
        code = verify_worker.run(
            {
                "tenant_id": str(TENANT_A),
                "once": True,
                "interval": 60,
                "age_threshold_days": 0,
                "fetch_age_threshold_days": 0,
                "engine": app_engine,
            }
        )

        assert code == 0, "verify worker returned non-zero exit code"

        verifications = _get_verifications(app_engine, TENANT_A)
        assert len(verifications) == 3, (
            f"Expected 3 source_verifications rows after Stage 5, "
            f"got {len(verifications)}"
        )
        for v in verifications:
            assert v.status == "live", (
                f"Expected verification status 'live', got '{v.status}' "
                f"for source {v.source_id}"
            )

        # ----------------------------------------------------------------
        # Invariant: raw_text and raw_html UNCHANGED after verify
        # ----------------------------------------------------------------
        post_verify = _get_sources_snapshot(app_engine, TENANT_A)
        for source_id, pre in pre_verify_snapshot.items():
            post = post_verify[source_id]
            assert post.raw_text == pre["raw_text"], (
                f"raw_text mutated by verify worker for source {source_id}"
            )
            assert post.raw_html == pre["raw_html"], (
                f"raw_html mutated by verify worker for source {source_id}"
            )
            assert post.last_verified_at is not None, (
                f"last_verified_at not set after verify for source {source_id}"
            )

        # ----------------------------------------------------------------
        # Invariant: content_hash UNCHANGED (verify never overwrites it)
        # ----------------------------------------------------------------
        with app_engine.connect() as conn:
            with conn.begin():
                with tenant_context(conn, TENANT_A):
                    hash_rows = conn.execute(
                        text(
                            "SELECT id, content_hash FROM sources WHERE tenant_id = :t"
                        ),
                        {"t": str(TENANT_A)},
                    ).fetchall()
        for row in hash_rows:
            assert row.content_hash == pre_verify_snapshot[row.id]["content_hash"], (
                f"content_hash changed after verify for source {row.id}"
            )

    finally:
        _cleanup_tenant(app_engine, migrated_engine, TENANT_A)


# ---------------------------------------------------------------------------
# Tenant isolation cross-check
# ---------------------------------------------------------------------------


def test_tenant_isolation(
    app_engine,
    migrated_engine,
    tmp_path: Path,
    httpx_mock: HTTPXMock,
) -> None:
    """
    Run the same RSS feed for TENANT_A and TENANT_B.
    Each tenant gets exactly 3 sources and 3 verifications.
    RLS ensures neither tenant can see the other's rows.
    """
    config_a = _build_config_file(tmp_path, TENANT_A, FEED_URL)
    config_b = _build_config_file(tmp_path, TENANT_B, FEED_URL)

    # HTTP mocks: feed × 2 tenants (collect), article URLs × 2 tenants (verify)
    for _ in range(2):  # once per tenant
        httpx_mock.add_response(url=FEED_URL, content=RSS_FEED)
        for url, body in ARTICLE_BODIES.items():
            httpx_mock.add_response(url=url, content=body)  # verify re-fetch

    runner = CliRunner()

    try:
        for tenant_id, config_file in [(TENANT_A, config_a), (TENANT_B, config_b)]:
            # Stage 1: Collect
            with patch("factvault.workers.cli.get_engine", return_value=app_engine), \
                 patch("factvault.workers.archive.submit_to_wayback",
                       return_value=WAYBACK_ARCHIVE_URL):
                result = runner.invoke(
                    worker_main,
                    [
                        "collect", "rss",
                        "--config", config_file,
                        "--tenant", str(tenant_id),
                    ],
                )
            assert result.exit_code == 0, (
                f"collect failed for tenant {tenant_id}:\n{result.output}"
            )

            # Stage 2: Archive
            with patch("factvault.workers.archive.submit_to_wayback",
                       return_value=WAYBACK_ARCHIVE_URL), \
                 patch("factvault.archiving.extract.extract_text",
                       return_value=_FAKE_RAW_TEXT):
                code = ArchiveWorker().run(
                    {
                        "tenant_id": str(tenant_id),
                        "once": True,
                        "interval": 60,
                        "engine": app_engine,
                    }
                )
            assert code == 0, f"archive failed for tenant {tenant_id}"

            # Stage 5: Verify
            code = VerifyWorker().run(
                {
                    "tenant_id": str(tenant_id),
                    "once": True,
                    "interval": 60,
                    "age_threshold_days": 0,
                    "fetch_age_threshold_days": 0,
                    "engine": app_engine,
                }
            )
            assert code == 0, f"verify failed for tenant {tenant_id}"

        # Each tenant must have exactly 3 archived sources and 3 verifications
        for tenant_id in (TENANT_A, TENANT_B):
            archived = _get_archived_sources(app_engine, tenant_id)
            assert len(archived) == 3, (
                f"Tenant {tenant_id}: expected 3 archived sources, got {len(archived)}"
            )
            verifications = _get_verifications(app_engine, tenant_id)
            assert len(verifications) == 3, (
                f"Tenant {tenant_id}: expected 3 verifications, got {len(verifications)}"
            )

        # Cross-tenant isolation: source IDs must not overlap.
        tenant_a_ids = {row.id for row in _get_archived_sources(app_engine, TENANT_A)}
        tenant_b_ids = {row.id for row in _get_archived_sources(app_engine, TENANT_B)}
        assert tenant_a_ids.isdisjoint(tenant_b_ids), (
            "Tenant isolation breach: source IDs overlap between TENANT_A and TENANT_B"
        )

    finally:
        _cleanup_tenant(app_engine, migrated_engine, TENANT_A)
        _cleanup_tenant(app_engine, migrated_engine, TENANT_B)
