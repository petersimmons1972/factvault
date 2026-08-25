#!/usr/bin/env bash
# Adversarial tests for scripts/govulncheck-policy.sh (B19 / #307).
#
# Premises (deliberately distinct from the policy script's own exit path):
#   - Fixture module with a real OSV-tracked reachable call MUST fail.
#   - Repo root MUST pass.
#   - jq actionable filter must IGNORE module-only inventory (empty package+function).
#   - jq actionable filter must CATCH package-level and function-level findings.
#   - Missing govulncheck on PATH must fail loudly (nonzero), not look clean.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
POLICY="$ROOT/scripts/govulncheck-policy.sh"
FAIL=0

pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*" >&2; FAIL=$((FAIL + 1)); }

# --- 1. Fixture must go red ---
set +e
( cd "$ROOT/testdata/vuln-fixture" && "$POLICY" ./... >/tmp/b19_policy_fixture.out 2>&1 )
rc=$?
set -e
if [ "$rc" -ne 0 ]; then
  pass "fixture exits nonzero (rc=$rc)"
else
  fail "fixture unexpectedly clean"
fi

# --- 2. Repo root must stay green ---
set +e
( cd "$ROOT" && "$POLICY" ./... >/tmp/b19_policy_root.out 2>&1 )
rc=$?
set -e
if [ "$rc" -eq 0 ]; then
  pass "repo root exits zero"
else
  fail "repo root unexpectedly dirty (rc=$rc); see /tmp/b19_policy_root.out"
fi

# --- 3. jq filter: module-only inventory must NOT count as actionable ---
module_only='{"finding":{"osv":"GO-FAKE-MODULE","trace":[{"module":"example.com/m"}]}}'
count="$(printf '%s\n' "$module_only" | jq -r '
  select(.finding != null)
  | .finding.trace[0] as $f
  | select((($f.package // "") != "") or (($f.function // "") != ""))
  | .finding.osv
' | sort -u | wc -l | tr -d ' ')"
if [ "$count" -eq 0 ]; then
  pass "module-only finding ignored by actionable filter"
else
  fail "module-only finding counted as actionable (count=$count)"
fi

# --- 4. jq filter: package-level finding MUST count ---
pkg_finding='{"finding":{"osv":"GO-FAKE-PKG","trace":[{"package":"example.com/m/pkg"}]}}'
count="$(printf '%s\n' "$pkg_finding" | jq -r '
  select(.finding != null)
  | .finding.trace[0] as $f
  | select((($f.package // "") != "") or (($f.function // "") != ""))
  | .finding.osv
' | sort -u | wc -l | tr -d ' ')"
if [ "$count" -eq 1 ]; then
  pass "package-level finding counted"
else
  fail "package-level finding not counted (count=$count)"
fi

# --- 5. jq filter: function-level finding MUST count ---
fn_finding='{"finding":{"osv":"GO-FAKE-FN","trace":[{"function":"Parse"}]}}'
count="$(printf '%s\n' "$fn_finding" | jq -r '
  select(.finding != null)
  | .finding.trace[0] as $f
  | select((($f.package // "") != "") or (($f.function // "") != ""))
  | .finding.osv
' | sort -u | wc -l | tr -d ' ')"
if [ "$count" -eq 1 ]; then
  pass "function-level finding counted"
else
  fail "function-level finding not counted (count=$count)"
fi

# --- 6. Missing govulncheck must fail loudly ---
set +e
( cd "$ROOT" && PATH="/usr/bin:/bin" "$POLICY" ./... >/tmp/b19_policy_missing.out 2>&1 )
rc=$?
set -e
if [ "$rc" -ne 0 ] && grep -q 'govulncheck not found' /tmp/b19_policy_missing.out; then
  pass "missing govulncheck exits nonzero with loud error"
else
  fail "missing govulncheck did not fail loudly (rc=$rc)"
fi

if [ "$FAIL" -ne 0 ]; then
  echo "govulncheck-policy_test: $FAIL failure(s)" >&2
  exit 1
fi
echo "govulncheck-policy_test: all adversarial cases passed"
exit 0
