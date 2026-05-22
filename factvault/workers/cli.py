# factvault/workers/cli.py
from __future__ import annotations

import hashlib
import sys
import uuid
from datetime import datetime, timezone

import click
from sqlalchemy import text

from factvault.collectors.base import get_collector
from factvault.config import load_yaml_config
from factvault.db.engine import get_engine
from factvault.db.rls import tenant_context
from factvault.workers.base import get_worker, list_workers


@click.group()
def main() -> None:
    """factvault worker CLI."""


@main.command("list")
def _list() -> None:
    """List all registered workers."""
    names = list_workers()
    if not names:
        click.echo("No workers registered.")
    for name in names:
        cls = get_worker(name)
        doc = (cls.__doc__ or "").strip().splitlines()[0] if cls.__doc__ else ""
        click.echo(f"{name:<20}  {doc}")


@main.command("run")
@click.argument("name")
@click.option("--tenant", default=None, help="Tenant UUID")
@click.option("--once", is_flag=True, default=False, help="Run one iteration then exit")
@click.option("--interval", default=60, type=int, show_default=True,
              help="Seconds between polling iterations (ignored if --once)")
def _run(name: str, tenant: str | None, once: bool, interval: int) -> None:
    """Run the named worker."""
    try:
        worker_cls = get_worker(name)
    except KeyError:
        click.echo(f"Unknown worker: '{name}'. Run 'factvault-worker list' to see options.",
                   err=True)
        sys.exit(1)

    args: dict = {
        "tenant_id": tenant,
        "once": once,
        "interval": interval,
    }
    worker = worker_cls()
    code = worker.run(args)
    sys.exit(code)


@main.command("collect")
@click.argument("collector_name")
@click.option("--config", "config_path", required=True, help="Path to YAML config file")
@click.option("--tenant", required=True, help="Tenant UUID")
@click.option("--dry-run", is_flag=True, default=False, help="Validate and print URLs without writing to DB")
def _collect(collector_name: str, config_path: str, tenant: str, dry_run: bool) -> None:
    """Collect sources via a named collector and insert into the DB."""
    # Validate tenant UUID
    try:
        tenant_uuid = uuid.UUID(tenant)
    except ValueError:
        click.echo(f"Invalid tenant UUID: {tenant!r}", err=True)
        sys.exit(1)

    # Load and validate YAML config
    cfg = load_yaml_config(config_path)

    # Find the collector block matching collector_name
    collector_cfg = next(
        (c for c in cfg.collectors if c.name == collector_name), None
    )
    if collector_cfg is None:
        click.echo(
            f"No collector named '{collector_name}' found in config {config_path!r}.",
            err=True,
        )
        sys.exit(1)

    # Instantiate the collector from the registry
    try:
        collector_cls = get_collector(collector_name)
    except KeyError:
        click.echo(
            f"Unknown collector: '{collector_name}'. No implementation registered.",
            err=True,
        )
        sys.exit(1)

    collector = collector_cls(**collector_cfg.config)

    # Iterate fetch()
    docs = list(collector.fetch())

    if dry_run:
        click.echo(f"Dry-run: would insert {len(docs)} source(s) via '{collector_name}':")
        for doc in docs:
            click.echo(f"  {doc.url}")
        return

    # Write to DB
    engine = get_engine()
    count = 0
    with engine.connect() as conn:
        with conn.begin():
            with tenant_context(conn, tenant_uuid):
                for doc in docs:
                    content_hash = "sha256:" + hashlib.sha256(doc.raw_html or b"").hexdigest()
                    conn.execute(
                        text("""
                            INSERT INTO sources
                                (id, tenant_id, url, status, raw_html, title, publisher,
                                 published_at, fetched_at, content_hash)
                            VALUES
                                (gen_random_uuid(), :tenant_id, :url, 'collected',
                                 :raw_html, :title, :publisher, :published_at,
                                 :fetched_at, :content_hash)
                            ON CONFLICT (tenant_id, url) DO NOTHING
                        """),
                        {
                            "tenant_id": str(tenant_uuid),
                            "url": doc.url,
                            "raw_html": doc.raw_html,
                            "title": doc.title,
                            "publisher": doc.publisher,
                            "published_at": doc.published_at,
                            "fetched_at": doc.fetched_at or datetime.now(tz=timezone.utc),
                            "content_hash": content_hash,
                        },
                    )
                    count += 1

    click.echo(f"Collected {count} sources via '{collector_name}' for tenant {tenant}")
