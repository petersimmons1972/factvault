#!/usr/bin/env bash
set -euo pipefail

# Integration probe for Hermes webhook adapter route:
#   POST issues.labeled(label=agent/hermes) to /webhooks/github-issues
#   assert 200 + agent/hermes/working label applied within timeout.

WEBHOOK_URL="${WEBHOOK_URL:-https://hermes.petersimmons.com/webhooks/github-issues}"
ISSUE_OWNER="${ISSUE_OWNER:-petersimmons1972}"
ISSUE_REPO="${ISSUE_REPO:-factvault}"
ISSUE_NUMBER="${ISSUE_NUMBER:-}"
WEBHOOK_SECRET="${WEBHOOK_SECRET:-}"
GITHUB_TOKEN="${GITHUB_TOKEN:-}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-60}"

if [[ -z "${ISSUE_NUMBER}" ]]; then
  echo "ISSUE_NUMBER is required" >&2
  exit 2
fi
if [[ -z "${WEBHOOK_SECRET}" ]]; then
  echo "WEBHOOK_SECRET is required (Infisical personal/hermes/prod, 32+ random bytes)" >&2
  exit 2
fi
if [[ ${#WEBHOOK_SECRET} -lt 32 ]]; then
  echo "WEBHOOK_SECRET must be 32+ bytes" >&2
  exit 2
fi
if [[ -z "${GITHUB_TOKEN}" ]]; then
  echo "GITHUB_TOKEN is required (issues:write, pull-requests:write, contents:write on allowlist repos only)" >&2
  exit 2
fi

# Ingress policy reminder for operators:
# - Public ingress must expose only /webhooks/* on 8644.
# - Do NOT expose 7788, 6379, 5432.
# - Keep Discord intake intact; do NOT broaden allowlist.
# - Do NOT change cron_mode from deny to auto.

payload=$(cat <<JSON
{
  "action": "labeled",
  "issue": {
    "number": ${ISSUE_NUMBER}
  },
  "repository": {
    "name": "${ISSUE_REPO}",
    "owner": {"login": "${ISSUE_OWNER}"}
  },
  "label": {
    "name": "agent/hermes"
  }
}
JSON
)

sig=$(PAYLOAD="$payload" WEBHOOK_SECRET="$WEBHOOK_SECRET" python3 - <<'PY'
import hashlib, hmac, os
payload = os.environ["PAYLOAD"].encode("utf-8")
secret = os.environ["WEBHOOK_SECRET"].encode("utf-8")
print("sha256=" + hmac.new(secret, payload, hashlib.sha256).hexdigest())
PY
)

status=$(curl -sS -o /tmp/hermes-webhook-response.json -w "%{http_code}" \
  -X POST "$WEBHOOK_URL" \
  -H "Content-Type: application/json" \
  -H "X-GitHub-Event: issues" \
  -H "X-Hub-Signature-256: $sig" \
  -d "$payload")

if [[ "$status" != "200" ]]; then
  echo "Webhook POST failed: HTTP $status" >&2
  cat /tmp/hermes-webhook-response.json >&2 || true
  exit 1
fi

deadline=$(( $(date +%s) + TIMEOUT_SECONDS ))
while [[ $(date +%s) -le $deadline ]]; do
  labels=$(curl -sS \
    -H "Authorization: Bearer $GITHUB_TOKEN" \
    -H "Accept: application/vnd.github+json" \
    "https://api.github.com/repos/${ISSUE_OWNER}/${ISSUE_REPO}/issues/${ISSUE_NUMBER}" \
    | python3 - <<'PY'
import json, sys
obj = json.load(sys.stdin)
print("\n".join(sorted([x["name"] for x in obj.get("labels",[])])))
PY
)
  if grep -qx "agent/hermes/working" <<<"$labels"; then
    echo "PASS: received 200 and applied agent/hermes/working"
    exit 0
  fi
  sleep 3
done

echo "Timed out waiting for agent/hermes/working label after ${TIMEOUT_SECONDS}s" >&2
exit 1
