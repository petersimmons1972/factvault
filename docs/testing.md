# Testing

## Reliable Go Test Runs

Run the suite exactly as CI does:

```bash
go test ./... -count=1
```

The test harness starts disposable Postgres containers for integration tests.

## Test Postgres Image

Default image:

```text
ankane/pgvector:latest
```

Override if needed:

```bash
export FACTVAULT_TEST_POSTGRES_IMAGE='ankane/pgvector:latest'
go test ./... -count=1
```

## Notes

- The shared startup lock is only held during test DB initialization.
- If a local run is interrupted, remove stale containers named `factvault-testdb-*` before rerunning.
