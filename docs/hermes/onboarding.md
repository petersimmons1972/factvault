# Hermes Onboarding — GitHub Issues Webhook Contract

## Purpose

This runbook defines how Hermes consumes GitHub `issues.labeled` events from
`/webhooks/github-issues` and executes work for issues labeled `agent/hermes`.

## Intake Route

- Public ingress: `https://hermes.petersimmons.com/webhooks/github-issues`
- Public listener: port `8644`
- Public path restriction: `/webhooks/*` only
- Preserve `X-Hub-Signature-256` header end-to-end
- Do **not** expose 7788, 6379, 5432, or any other internal Hermes port

## Secrets and auth

- `WEBHOOK_ENABLED=true`
- `WEBHOOK_PORT=8644`
- `WEBHOOK_SECRET` must come from Infisical `personal/hermes/prod`
  - minimum entropy: 32+ random bytes
  - never hardcoded in repo/config
- `GITHUB_TOKEN` must be Infisical-sourced with only:
  - `contents:write`
  - `pull-requests:write`
  - `issues:write`
  - repository allowlist only: `engram-go`, `olla`, `aifleet`, `factvault`,
    `agent-gateway`, `instinct`, `harness-port`, `yourai`

## Event mapping

Route only GitHub `issues` events where:
- `action == labeled`
- `label.name == agent/hermes`

Prompt contract:
1. Read full issue body using `github-issues` skill
2. Execute requested task
3. Comment progress/result to issue
4. Open PR and include `Closes #N` in PR body and final commit footer

## Label state machine

- `agent/hermes` → queued for Hermes
- `agent/hermes/working` → actively being processed
- optional `agent/hermes/blocked` → external/tooling block

Transition on successful webhook intake:
- apply `agent/hermes/working` quickly after `agent/hermes`

Completion:
- PR merged with `Closes #N`; queue labels may then be removed

## Guardrails

- Do NOT broaden Hermes Discord allowlist
- Do NOT change cron_mode from deny to auto (handled separately)
- Keep Discord intake working through webhook rollout

## Verification

Use:

```bash
bin/test-hermes-deployment.sh
```

Required env:
- `ISSUE_NUMBER` (test issue id)
- `WEBHOOK_SECRET`
- `GITHUB_TOKEN`

The test sends synthetic `issues.labeled` payload (`agent/hermes`) and asserts:
- HTTP 200 from webhook endpoint
- `agent/hermes/working` appears on target issue within 60 seconds
