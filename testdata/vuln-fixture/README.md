# vuln-fixture

Deliberately vulnerable Go module used to prove the standing `govulncheck`
policy gate can fail (B19 / factvault#307).

- Pins `golang.org/x/net` to a historical version with published OSV entries.
- Calls `html.Parse` / `html.Render` so findings are actionable (reachable),
  not module-only inventory.
- Own `go.mod` — excluded from the parent module's `go build ./...` /
  `go test ./...` package graph.

Do not "fix" the pin; a clean fixture would make the policy gate a no-op.
