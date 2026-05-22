# factvault/workers/cli.py
from __future__ import annotations

import sys
import click

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
