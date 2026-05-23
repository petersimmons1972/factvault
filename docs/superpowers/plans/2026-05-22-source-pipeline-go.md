# Plan 2 Go Rewrite Spec: Source Pipeline

Date: 2026-05-23
Status: Active
Issue: #73

## Scope

Implement Plan 2 source-pipeline behavior in Go and wire it into the `factvault`
binary:

1. `collect` stage: fetch candidate URLs and store canonical source records with
   `status='collected'`, compressed `raw_html`, and SHA-256 `content_hash`.
2. `archive` stage: process collected rows, populate `raw_text`, best-effort
   Wayback `archive_url`, and transition to `status='archived'`.
3. `verify` stage: re-check liveness/content and append `source_verifications`
   rows with status transitions on `sources`.

This plan intentionally keeps implementation minimal and deterministic so later
plans can extend collectors and extraction behavior without changing stage
contracts.

## Contracts

- `archive_url` capture is best-effort; failures do not block status progression.
- `source_verifications` is append-only; verify writes insert rows only.
- Worker execution supports single-run (`--once`) mode for CI/smoke tests.
- CLI entrypoint is Cobra subcommands under `factvault worker`.

## Deliverables

- New Go packages:
  - `internal/collectors`
  - `internal/workers`
- New CLI command:
  - `factvault worker collect|archive|verify`
- Test coverage for:
  - `collect` inserts compressed source rows with expected status/hash.
  - `archive` populates `raw_text` and transitions `collected -> archived`.
  - `verify` appends verification rows and updates source status.

## Acceptance checks

- `go test ./... -count=1`
- `go test ./... -race -count=1`
- `go vet ./...`
