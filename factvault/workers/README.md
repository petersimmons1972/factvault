# Workers — Source Pipeline

The source pipeline moves documents through five statuses:

```
collected → archived → extracted → verified
```

Workers run as CLI commands via `factvault-worker` and as Kubernetes CronJobs.

---

## Pipeline Stages

| Stage | Worker command | Input status | Output status |
|-------|---------------|--------------|---------------|
| 1 — Collect | `factvault-worker collect <name>` | — | `collected` |
| 2 — Archive | `factvault-worker run archive` | `collected` | `archived` |
| 5 — Verify | `factvault-worker run verify` | `archived` / `verified` | _(no change)_ |

Stages 3 (Extract), 4 (Corroborate), and 6 (Relate) are implemented in Plan 3.

---

## Pipeline Overview

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│  Stage 1    │     │  Stage 2    │     │  Stage 5    │
│  COLLECT    │────▶│  ARCHIVE    │────▶│  VERIFY     │
│             │     │             │     │             │
│ RSS / HTTP  │     │ Wayback SPN │     │ Re-fetch +  │
│ Sitemap /   │     │ trafilatura │     │ hash check  │
│ Upload /    │     │ raw_html    │     │ excerpt     │
│ SearXNG /   │     │ raw_text    │     │ continuity  │
│ WaybackCDX  │     │ content_hash│     │             │
└─────────────┘     └─────────────┘     └─────────────┘
     writes               writes              writes
  status='collected'  status='archived'  source_verifications
```

**Collect** (Stage 1): A named collector fetches URLs from an external source — an RSS
feed, HTTP page, sitemap, SearXNG query, Wayback CDX index, or a direct upload. Each
URL is inserted into `sources` with `status='collected'` via an idempotent
`ON CONFLICT (tenant_id, url) DO NOTHING`.

**Archive** (Stage 2): Picks up rows with `status='collected'` in batches. For each
source it: (1) fetches the live page with httpx, (2) runs trafilatura to extract
`raw_text`, (3) computes a SHA-256 `content_hash`, (4) stores `raw_html` zlib-
compressed at level 6, and (5) submits the URL to the Internet Archive's SPN2 endpoint
(best-effort; `archive_url=NULL` is valid if Wayback is unavailable). Sets
`status='archived'` on success.

**Verify** (Stage 5): Periodically re-fetches archived sources older than a configured
threshold. Compares the current SHA-256 hash against the stored `content_hash`. Writes
an append-only row to `source_verifications` with one of: `live` (hash unchanged),
`content-changed` (hash changed, excerpts still found), `excerpt-missing` (hash changed,
excerpts gone), or `link-rot` (connection failure). The stored `raw_html` and `raw_text`
are NEVER overwritten.

---

## Running Workers

### Collect

```bash
# Ingest an RSS feed defined in config.yaml
factvault-worker collect rss --config config.yaml --tenant <tenant-uuid>

# Ingest HTTP pages
factvault-worker collect http --config config.yaml --tenant <tenant-uuid>

# Dry-run: validate config + instantiate collector without DB writes
factvault-worker collect rss --config config.yaml --tenant <tenant-uuid> --dry-run
```

### Archive (continuous)

```bash
# Run continuously, poll every 60 seconds
factvault-worker run archive --tenant <tenant-uuid> --interval 60

# Run once and exit
factvault-worker run archive --tenant <tenant-uuid> --once
```

### Verify (continuous)

```bash
# Run continuously, poll every 300 seconds
factvault-worker run verify --tenant <tenant-uuid> --interval 300

# Run once and exit
factvault-worker run verify --tenant <tenant-uuid> --once
```

### List registered workers

```bash
factvault-worker list
```

### Help

```bash
factvault-worker --help
factvault-worker run --help
factvault-worker collect --help
```

---

## YAML Config Schema

```yaml
tenants:
  - id: <uuid>        # required — the tenant UUID from the database
    name: <string>    # required — human-readable label

collectors:
  - name: rss         # collector name matching the registered entry point
    config:
      feed_urls:      # list of RSS/Atom feed URLs to poll
        - https://example.com/feed.xml

  - name: http
    config:
      urls:           # list of article page URLs to fetch directly
        - https://example.com/article-1

  - name: sitemap
    config:
      sitemap_urls:   # list of sitemap.xml URLs
        - https://example.com/sitemap.xml
      lastmod_after:  # optional ISO date — skip entries older than this
        "2024-01-01"

  - name: searxng
    config:
      query: "fact-check topic"  # search query
      base_url: "https://searxng.example.com"

  - name: wayback_cdx
    config:
      urls:           # original URLs to look up in Wayback CDX API
        - https://example.com/page

archive_worker:
  interval_seconds: 60    # polling interval when not using --once
  batch_size: 50          # sources processed per batch

verify_worker:
  age_threshold_days: 30  # re-verify sources archived more than N days ago
  fetch_age_threshold_days: 7  # skip sources verified less than N days ago
  batch_size: 50
```

Config is loaded and validated by `factvault.config.load_yaml_config` using Pydantic.
Unknown top-level keys raise a `ValidationError`.

---

## Adding a New Collector

1. Create `factvault/collectors/my_collector.py`:

```python
from __future__ import annotations

from typing import Iterator

from factvault.collectors.base import Collector, RawDocument, register_collector


@register_collector
class MyCollector(Collector):
    """One-line description shown in factvault-worker list."""

    name = "my_collector"   # must be unique; used as CLI arg and YAML key

    def __init__(self, my_param: str, timeout: float = 30.0) -> None:
        self.my_param = my_param
        self.timeout = timeout

    def fetch(self) -> Iterator[RawDocument]:
        # Yield one RawDocument per discovered URL.
        yield RawDocument(
            url="https://example.com/article",
            raw_html=b"<html>...</html>",
            collector_name=self.name,
        )
```

2. Add to `factvault/collectors/__init__.py` (so it is imported on package load and the
   `@register_collector` decorator fires):

```python
from factvault.collectors import my_collector  # noqa: F401
```

3. Add the collector block to `config.yaml`:

```yaml
collectors:
  - name: my_collector
    config:
      my_param: value
```

The YAML `config:` keys are passed as keyword arguments to `__init__`, so the YAML key
names must match the parameter names exactly.

4. Add tests in `tests/collectors/test_my_collector.py`. Use `pytest-httpx` to mock HTTP
   calls. Verify `collector.name`, `fetch()` returns `RawDocument` instances with the
   correct fields, and deduplication behavior if applicable.

---

## Kubernetes Deployment

See `deploy/k8s/verify-worker-cronjob.yaml` for a production CronJob running verify
daily at 03:00 UTC. It uses a Chainguard Wolfi base image, tini as PID 1, and runs as
nonroot UID 65532 with `fsGroup: 65532` set in the pod security context.

The **archive worker** runs as a long-lived Deployment (Plan 3, forthcoming). The
**dossier worker** runs as a CronJob (Plan 4, forthcoming).

Environment variables required by all workers:

| Variable | Required | Description |
|----------|----------|-------------|
| `FACTVAULT_DATABASE_URL` | Yes | SQLAlchemy connection string (`postgresql+psycopg://...`) |
| `FACTVAULT_TENANT_ID` | Conditional | Default tenant UUID (can be overridden with `--tenant`) |
| `FACTVAULT_WAYBACK_RATE_LIMIT_PER_MIN` | No | Wayback SPN2 rate limit (default: 15) |

---

## Troubleshooting

### Wayback 429 / rate limits

The archive worker rate-limits itself to 15 Wayback SPN2 requests per minute using a
per-process token bucket. If you see persistent 429 errors:

- Check whether another process on the same IP is also submitting to SPN.
- Lower the rate limit: set `FACTVAULT_WAYBACK_RATE_LIMIT_PER_MIN=8` (or lower) in
  the worker's environment.
- Wayback failure never blocks ingestion — sources reach `archived` with
  `archive_url=NULL`, which is valid.

### trafilatura returns None on paywalled pages

`raw_text` will be `None` or empty for paywalled or JavaScript-rendered content. The
source still reaches `archived` status; `raw_html` is always captured. Downstream
extractors must handle empty `raw_text` gracefully. To enable trafilatura's generic
extraction fallback, set `no_fallback=False` in `factvault/workers/archive.py`.

### Source stays in `status='collected'` forever

1. Is the archive worker running? Check with `factvault-worker list` and confirm
   `archive` appears.
2. Does the worker have `FACTVAULT_DATABASE_URL` set? Without it the worker exits with
   a `RuntimeError` at startup.
3. Is the tenant UUID correct? The worker only processes rows for the configured tenant.
4. Check logs for `httpx` connection errors — the worker skips a source on fetch failure
   and retries on the next batch cycle.

### RLS no-rows-visible

If queries against `sources` return zero rows unexpectedly:

- Confirm `app.tenant_id` GUC is set. Use `SHOW app.tenant_id;` in psql.
- Confirm you are connecting as `app_user` (not superuser). The superuser bypasses RLS
  policies; `app_user` is subject to them. Use a superuser connection only for schema
  migrations and admin operations.
- Confirm the row's `tenant_id` column matches the GUC value exactly.

### Hash mismatch without excerpt drift

A content hash change without excerpt drift is recorded as `content-changed` in
`source_verifications`. The stored `raw_text` and `raw_html` are NEVER overwritten —
the captured body is the durable evidence. Inspect `source_verifications` to see the
new hash and timestamp. Use the stored `raw_text` snapshot for downstream fact-checking
regardless of subsequent page changes.
