# RSS Ingestion: `worker rss`

The `worker rss` subcommand polls RSS/Atom feeds defined in a YAML configuration file and feeds
each item into the standard collect pipeline. It is the recommended way to populate factvault with
a continuous stream of sources from known publishers.

---

## How It Works

```
config/feeds.yaml
       |
       v
  [worker rss]
       |   Load feeds from YAML
       |   For each due feed (per-feed interval):
       |     Fetch RSS/Atom feed URL
       |     Parse items (URL + title from each <item> or <entry>)
       |     Call SourcePipeline.CollectOnce
       |       -> INSERT sources WHERE NOT EXISTS (tenant_id, url)
       |       -> status='collected'
       |
       v
  Normal pipeline continues:
    worker archive -> worker extract -> worker corroborate
```

`worker rss` is a loop by default. It polls each feed on its configured interval, sleeping
between rounds. Use `--once` to run a single polling cycle and exit.

---

## config/feeds.yaml Schema

Every field is validated against the `FeedSpec` struct in `internal/collectors/feeds.go`.

```yaml
feeds:
  - name: "CISA Alerts"            # string, human-readable label (optional)
    url: "https://..."             # string, REQUIRED: full RSS/Atom feed URL
    tenant: "uuid-here"            # string, REQUIRED: tenant UUID (feeds without tenant are skipped)
    topic: "threat-intelligence"   # string, free-form topic tag (optional)
    tags: ["cisa", "security"]     # []string, free-form tag list (optional)
    interval: "15m"               # string, Go duration format (optional; falls back to --interval)
```

Field details:

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `url` | string | yes | Full URL to RSS or Atom feed |
| `tenant` | string | yes | UUID of the tenant to collect into; feeds missing this field are silently skipped |
| `name` | string | no | Human-readable label for logs |
| `topic` | string | no | Free-form topic string stored with the source |
| `tags` | []string | no | Free-form tag list |
| `interval` | string | no | Go duration (e.g. `15m`, `1h`, `30s`); falls back to `--interval` default if absent or unparseable |

**Valid interval formats:** any string accepted by Go's `time.ParseDuration`: `15m`, `1h30m`,
`30s`, `24h`, etc. Negative or zero durations fall back to the `--interval` default.

Feeds with an empty or missing `tenant` field are skipped by the scheduler. This is logged but is
not an error -- it allows commenting out a feed by removing its `tenant` line.

---

## Usage

Run one polling cycle and exit (recommended for cron jobs or first-run verification):

```bash
./bin/factvault worker rss \
  --feeds config/feeds.yaml \
  --once \
  --dsn "$FACTVAULT_DATABASE_URL"
```

Run in loop mode (default) with a 15-minute minimum interval between polls:

```bash
./bin/factvault worker rss \
  --feeds config/feeds.yaml \
  --interval 15m \
  --dsn "$FACTVAULT_DATABASE_URL"
```

---

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--feeds` | `config/feeds.yaml` | Path to the YAML feed config file |
| `--once` | `false` | Run one polling cycle then exit |
| `--interval` | `15m` | Default polling interval for feeds without an explicit `interval` field |
| `--dsn` | `FACTVAULT_DATABASE_URL` | Postgres DSN |

The `--tenant` persistent flag is NOT required for `worker rss` -- tenant is read from each
feed's YAML `tenant` field instead.

---

## worker rss vs. worker collect

`worker collect` is a stub with a static seed URL (see TODO #94 in the source). It inserts a
single placeholder source (`https://example.com/factvault-seed`) to exercise the pipeline
mechanically. It does not fetch real URLs and is not intended for production use.

`worker rss` is the real ingestion path for operator-defined feed sources. `worker research` is
the active acquisition path for entity-driven research.

For a new deployment, define your feeds in `config/feeds.yaml` and run `worker rss --once` to
verify they collect correctly before enabling the loop.

---

## Full Workflow

```bash
# 1. Define your feeds
cp config/feeds.yaml config/feeds.yaml.bak
vi config/feeds.yaml  # add your feeds with tenant UUIDs

# 2. Verify one polling cycle
./bin/factvault worker rss --feeds config/feeds.yaml --once

# 3. Run the rest of the pipeline
./bin/factvault worker archive  --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker extract  --tenant "$FACTVAULT_DEV_TENANT_ID" \
  --llm-model llama3.1:8b --llm-base-url http://localhost:11434/v1
./bin/factvault worker corroborate --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker embed    --tenant "$FACTVAULT_DEV_TENANT_ID"
./bin/factvault worker dossier  --tenant "$FACTVAULT_DEV_TENANT_ID"

# 4. Start the loop
./bin/factvault worker rss --feeds config/feeds.yaml --interval 15m
```

---

## Related

- [Active Acquisition](active-acquisition.md) -- entity-driven research as an alternative source path
- [Operator Guide](../operator-guide.md) -- full worker order and sequencing
- [CLI Reference](../reference/cli.md) -- full flag reference for all subcommands
