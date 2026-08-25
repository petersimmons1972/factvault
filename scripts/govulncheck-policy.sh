#!/usr/bin/env bash
# govulncheck-policy.sh — standing actionable-runtime vulnerability gate (B19 / #307)
#
# Mirrors the "govulncheck (runtime, blocking)" step in .github/workflows/ci.yml:
# fail only when a finding's first trace frame has a non-empty package or function
# (reachable / imported), not on module-only inventory messages.
#
# Usage (cwd = module root to scan):
#   scripts/govulncheck-policy.sh
#   make vuln-policy
#
# Exit codes:
#   0 — no actionable runtime findings
#   1 — actionable finding(s) present, or required tooling missing / scan failed loudly

set -euo pipefail

PACKAGES="${1:-./...}"
OUT="$(mktemp "${TMPDIR:-/tmp}/govulncheck-policy.XXXXXX.json")"
trap 'rm -f "$OUT"' EXIT

if ! command -v govulncheck >/dev/null 2>&1; then
  echo "govulncheck-policy: govulncheck not found on PATH" >&2
  echo "Install: go install golang.org/x/vuln/cmd/govulncheck@latest" >&2
  exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "govulncheck-policy: jq not found on PATH (required to filter actionable findings)" >&2
  exit 1
fi

# Capture JSON even when govulncheck exits non-zero (findings present).
set +e
govulncheck -json "$PACKAGES" >"$OUT"
scan_status=$?
set -e

# govulncheck: 0 = clean, 3 = vulns found, other = tooling/build failure.
# Tooling/build failures must fail loudly (never look like a clean policy pass).
if [ "$scan_status" -ne 0 ] && [ "$scan_status" -ne 3 ]; then
  echo "govulncheck-policy: govulncheck failed with exit ${scan_status}" >&2
  # Surface any JSON error events if present; otherwise dump raw output.
  if ! jq -e 'select(.error != null)' "$OUT" >/dev/null 2>&1; then
    cat "$OUT" >&2 || true
  else
    jq -r 'select(.error != null) | .error.message // .' "$OUT" >&2 || cat "$OUT" >&2
  fi
  exit 1
fi

actionable_count="$(jq -r '
  select(.finding != null)
  | .finding.trace[0] as $f
  | select((($f.package // "") != "") or (($f.function // "") != ""))
  | .finding.osv
' "$OUT" | sort -u | wc -l | tr -d ' ')"

if [ "${actionable_count}" -gt 0 ]; then
  echo "Actionable runtime vulnerabilities found: ${actionable_count}"
  jq -r '
    select(.finding != null)
    | .finding.trace[0] as $f
    | select((($f.package // "") != "") or (($f.function // "") != ""))
    | .finding.osv
  ' "$OUT" | sort -u
  exit 1
fi

echo "govulncheck-policy: clean (no actionable runtime findings)"
exit 0
