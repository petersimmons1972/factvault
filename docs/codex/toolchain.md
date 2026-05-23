# Codex Go Toolchain

These tools are mandatory. Run the full sequence after every edit batch.
Do NOT silence linters by editing `.golangci.yml` — fix the code.
Do NOT add `//nolint` comments without a `// reason: <explanation>` on the same line.

## Tier 1 — required

### golangci-lint v2
Meta-runner for 50+ linters. Single binary, single config, parseable output.

- Install: `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`
- Invoke: `golangci-lint run --fix ./...` after every edit batch

### gofumpt
Stricter superset of `gofmt`. Deterministic, no config debates.

- Install: `go install mvdan.cc/gofumpt@latest`
- Invoke: `gofumpt -w .` before every commit

### go build + go vet (stdlib)
Compiler = zero-latency type checker. `go vet` catches ~20 classes of bugs.

- Invoke: `go build ./...` then `go vet ./...` after every edit

### govulncheck
Official Go vuln scanner. Reachability-aware, low false-positive.

- Install: `go install golang.org/x/vuln/cmd/govulncheck@latest`
- Invoke: on `go.mod` changes and in CI: `govulncheck ./...`

### go test -race -count=1 ./...
Standard test runner with race detector. `-count=1` defeats caching.

- Stdlib
- Invoke: after every change that touches handlers, middleware, or shared state
- Integration tests gated behind `//go:build integration` build tag

## Tier 2 — add if Tier 1 isn't enough

### goimports / gci
Auto-add and group imports. `gci` enforces stdlib / external / internal sections.

- Install: `go install golang.org/x/tools/cmd/goimports@latest` or `go install github.com/daixiang0/gci@latest`
- Invoke: part of the format pass

### go mod tidy
Prunes unused deps, adds missing ones.

- Stdlib
- CI check: `go mod tidy && git diff --exit-code go.mod go.sum`

## Invocation order (the loop)

After every edit batch, stop on first failure:

```
1. go build ./...                  # type errors first
2. gofumpt -w .                    # deterministic format
3. goimports -w .                  # import hygiene
4. golangci-lint run --fix ./...   # lint + auto-fix
5. go test -race -count=1 ./...    # correctness + races
```

On `go.mod` changes, additionally:
```
6. go mod tidy
7. govulncheck ./...
```

## Protected files

`.golangci.yml` is the contract between coordinator and Codex. Codex
must not modify it to silence errors. To raise a real concern about a
linter rule, file an issue and stop work on the current task.
