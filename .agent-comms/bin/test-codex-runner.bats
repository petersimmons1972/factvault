#!/usr/bin/env bats

setup() {
  export TEST_ROOT
  TEST_ROOT="$(mktemp -d)"
  export HOME="${TEST_ROOT}/home"
  export PATH="${TEST_ROOT}/bin:${PATH}"
  export RUNNER_STATE_DIR="${HOME}/.local/state/codex-runner"
  export QUEUE_STATE_DIR="${HOME}/.local/state/codex-agent-codex-queue"
  export QUEUE_FILE="${QUEUE_STATE_DIR}/latest.tsv"
  export RUNNER_SCRIPT="${BATS_TEST_DIRNAME}/codex-runner.sh"
  export PROJECTS_ROOT="${HOME}/projects"
  export STUB_LOG="${TEST_ROOT}/stub.log"
  export RUNNER_WAIT_FOR_CHILD=1

  mkdir -p "${HOME}/scripts" "${HOME}/.local/state" "${QUEUE_STATE_DIR}" "${PROJECTS_ROOT}/factvault"
  mkdir -p "${TEST_ROOT}/bin"
  : > "${STUB_LOG}"

  cat > "${TEST_ROOT}/bin/pgrep" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ -n "${PGREP_MATCH:-}" ]]; then
  exit 0
fi
exit 1
EOF

  cat > "${TEST_ROOT}/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'gh %s\n' "$*" >> "${STUB_LOG}"
repo=""
issue=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      repo="$2"
      shift 2
      ;;
    issue)
      shift
      ;;
    view)
      issue="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
key="${repo}#${issue}"
case "${key}" in
  petersimmons1972/factvault#113)
    printf '2026-05-24T05:40:46Z\tpriority/p0,agent/codex\n'
    ;;
  petersimmons1972/factvault#114)
    printf '2026-05-01T00:00:00Z\tpriority/p2,agent/codex\n'
    ;;
  petersimmons1972/factvault#115)
    printf '2026-05-02T00:00:00Z\tpriority/p1,agent/codex,agent/codex/working\n'
    ;;
  *)
    printf 'missing gh fixture for %s\n' "${key}" >&2
    exit 1
    ;;
esac
EOF

  cat > "${TEST_ROOT}/bin/nohup" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'nohup %s\n' "$*" >> "${STUB_LOG}"
"$@"
EOF

  cat > "${TEST_ROOT}/bin/codex" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'codex %s\n' "$*" >> "${STUB_LOG}"
exit 0
EOF

  chmod +x "${TEST_ROOT}/bin/pgrep" "${TEST_ROOT}/bin/gh" "${TEST_ROOT}/bin/nohup" "${TEST_ROOT}/bin/codex"
}

teardown() {
  rm -rf "${TEST_ROOT}"
}

@test "busy path exits cleanly without invoking codex" {
  export PGREP_MATCH=1
  cat > "${QUEUE_FILE}" <<'EOF'
agent/codex sweep at 2026-05-24T05:40:01Z
petersimmons1972/factvault	#113	2026-05-24T05:40:46Z	codex runner	https://github.com/petersimmons1972/factvault/issues/113
EOF

  run "${RUNNER_SCRIPT}"

  [ "$status" -eq 0 ]
  [[ "$output" == *"busy"* ]]
  ! grep -q '^codex ' "${STUB_LOG}"
  ! grep -q '^gh ' "${STUB_LOG}"
}

@test "empty queue exits cleanly without spawning codex" {
  cat > "${QUEUE_FILE}" <<'EOF'
agent/codex sweep at 2026-05-24T05:40:01Z
EOF

  run "${RUNNER_SCRIPT}"

  [ "$status" -eq 0 ]
  [[ "$output" == *"empty"* ]]
  ! grep -q '^codex ' "${STUB_LOG}"
}

@test "priority sort prefers p0 over older p2 and skips working issues" {
  cat > "${QUEUE_FILE}" <<'EOF'
agent/codex sweep at 2026-05-24T05:40:01Z
petersimmons1972/factvault	#114	2026-05-24T05:39:00Z	older p2	https://github.com/petersimmons1972/factvault/issues/114
petersimmons1972/factvault	#115	2026-05-24T05:39:10Z	working p1	https://github.com/petersimmons1972/factvault/issues/115
petersimmons1972/factvault	#113	2026-05-24T05:40:46Z	newer p0	https://github.com/petersimmons1972/factvault/issues/113
EOF

  run "${RUNNER_SCRIPT}"

  [ "$status" -eq 0 ]
  grep -q 'gh issue view 114 --repo petersimmons1972/factvault --json createdAt,labels --jq' "${STUB_LOG}"
  grep -q 'gh issue view 115 --repo petersimmons1972/factvault --json createdAt,labels --jq' "${STUB_LOG}"
  grep -q 'gh issue view 113 --repo petersimmons1972/factvault --json createdAt,labels --jq' "${STUB_LOG}"
  grep -q 'codex exec --cd .*/projects/factvault .*issue #113' "${STUB_LOG}"
  ! grep -q 'issue #114' "${STUB_LOG}"
  ! grep -q 'issue #115' "${STUB_LOG}"
}

@test "duplicate suppression skips issue with active run and same-minute rerun" {
  mkdir -p "${RUNNER_STATE_DIR}"
  cat > "${RUNNER_STATE_DIR}/runs.tsv" <<'EOF'
2026-05-24T05:40:05Z	petersimmons1972/factvault	113	99999
EOF
  cat > "${QUEUE_FILE}" <<'EOF'
agent/codex sweep at 2026-05-24T05:40:01Z
petersimmons1972/factvault	#113	2026-05-24T05:40:46Z	codex runner	https://github.com/petersimmons1972/factvault/issues/113
EOF

  run "${RUNNER_SCRIPT}"

  [ "$status" -eq 0 ]
  [[ "$output" == *"no eligible issues"* ]]
  ! grep -q '^codex ' "${STUB_LOG}"

  rm -f "${RUNNER_STATE_DIR}/runs.tsv"
  touch "${RUNNER_STATE_DIR}/completed/petersimmons1972_factvault-113.done"

  run "${RUNNER_SCRIPT}"

  [ "$status" -eq 0 ]
  grep -q '^codex ' "${STUB_LOG}"

  run "${RUNNER_SCRIPT}"

  [ "$status" -eq 0 ]
  [[ "$output" == *"already started this minute"* ]]
}
