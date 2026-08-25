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
- Invoke locally:
  - Runtime gate (standing policy): `make vuln-policy` or
    `./scripts/govulncheck-policy.sh ./...`
  - Test graph inventory: `govulncheck -json -test ./...`
- Adversarial self-test (fixture must fail, repo root must pass):
  `make vuln-policy-selftest`
- Deliberately vulnerable fixture (own module, not in root package graph):
  `testdata/vuln-fixture/`

### CI vulnerability policy

CI runs two govulncheck passes with different intent, and the runtime gate also
runs on a daily `schedule:` cron so a new OSV advisory is caught the same day
it is published rather than waiting for the next PR (B19 / #307):

- `runtime, blocking`: `scripts/govulncheck-policy.sh` scans `./...` without
  `-test` and fails on actionable findings (package/symbol-level). This is the
  release safety gate and must stay strict.
- `test graph, non-blocking`: scans with `-test` for visibility into vulnerabilities
  that enter only through test tooling (for example, Docker-related test harness
  dependencies). This output is reported but does not fail CI by itself.

Tradeoff:

- We do not hide runtime risk: shipping code is still blocked on actionable
  vulnerabilities.
- We avoid false stop-the-line churn from known test-only Docker dependency
  findings while keeping them visible for periodic dependency hygiene work.
- Schedule coverage closes the gap where `main` stayed red between PRs after
  an OSV publish with no code change.

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
7. govulncheck -json ./...
8. govulncheck -json -test ./...   # visibility for test-only dependency surface
```

## Protected files

`.golangci.yml` is the contract between coordinator and Codex. Codex
must not modify it to silence errors. To raise a real concern about a
linter rule, file an issue and stop work on the current task.
