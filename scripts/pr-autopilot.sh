#!/usr/bin/env bash
set -euo pipefail

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI is required" >&2
  exit 2
fi

PR_INPUT="${1:-}"
if [[ -z "$PR_INPUT" ]]; then
  PR_INPUT="$(gh pr view --json number -q .number)"
fi

if [[ "$PR_INPUT" =~ ^[0-9]+$ ]]; then
  PR_NUM="$PR_INPUT"
else
  PR_NUM="$(gh pr view "$PR_INPUT" --json number -q .number)"
fi

REPO="$(gh repo view --json nameWithOwner -q .nameWithOwner)"

echo "Watching CI for PR #$PR_NUM in $REPO"
gh pr checks "$PR_NUM" --watch --interval 10 || true
echo

STATUS_JSON="$(gh pr view "$PR_NUM" --json statusCheckRollup -q .statusCheckRollup)"
FAILED_COUNT="$(jq '[.[] | select(.conclusion == "FAILURE")] | length' <<<"$STATUS_JSON")"

if [[ "$FAILED_COUNT" -eq 0 ]]; then
  echo "All checks passed for PR #$PR_NUM"
  exit 0
fi

echo "Detected $FAILED_COUNT failing checks:"
jq -r '.[] | select(.conclusion == "FAILURE") | "- " + .name + " -> " + .detailsUrl' <<<"$STATUS_JSON"

echo
echo "Attempting to print logs for failing checks..."
while IFS= read -r line; do
  run_id="$(sed -E 's#.*/runs/([0-9]+).*#\1#' <<<"$line")"
  name="$(sed -E 's/^- (.*) -> .*/\1/' <<<"$line")"
  if [[ -n "$run_id" && "$run_id" =~ ^[0-9]+$ ]]; then
    echo
    echo "===== $name (run $run_id) ====="
    gh run view "$run_id" --repo "$REPO" --log-failed || true
  fi
done < <(jq -r '.[] | select(.conclusion == "FAILURE") | "- " + .name + " -> " + .detailsUrl' <<<"$STATUS_JSON")

exit 1
