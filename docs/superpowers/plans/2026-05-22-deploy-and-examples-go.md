# Deploy and Examples Go Implementation Plan

**Date:** 2026-05-23
**Status:** Implementation (Plan 5 in Go)
**Depends on:** Plan 4 merged
**Authority:** `docs/superpowers/specs/2026-05-22-go-transition.md`

## Scope

Plan 5 wires the operational Go stack: a single Go binary image, docker-compose, Kubernetes manifests, first-boot doctor checks, and language-neutral examples. The Python embedder remains a bounded microservice under `services/embedder/` and is not rewritten.

## Deliverables

- `deploy/docker/Dockerfile`: Chainguard Go build stage, Wolfi runtime, `/sbin/tini`, UID 65532.
- `docker-compose.yml`: three services: `postgres`, `embedder`, `factvault`.
- `deploy/k8s/`: API deployment, embedder deployment, collect/archive/verify/extract/corroborate/dossier CronJobs, ConfigMap.
- `internal/doctor/`: seven checks: Postgres+pgvector, migrations, RLS, LLM endpoint, embedder health, Wayback, canary fact assembly.
- `internal/examples/`: YAML fixture loader with `factvault example list|info|load`.
- `examples/`: four domain examples using the same YAML layout.
- CLI: `factvault doctor` and `factvault example` subcommands.

## Locked Decisions

- Single app image, multiple commands.
- No Python runtime in the Go app image.
- Nonroot UID/GID 65532 everywhere.
- K8s security context: `fsGroup: 65532`, `allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]`.
- The example YAML format is intentionally simple and language neutral.

## Verification

- `go test ./... -count=1`
- `go test ./... -race -count=1`
- `go vet ./...`
- `sqlc generate` leaves `internal/db` unchanged.
