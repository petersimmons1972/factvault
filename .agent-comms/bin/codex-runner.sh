#!/usr/bin/env bash
set -euo pipefail

STATE_DIR="${RUNNER_STATE_DIR:-${HOME}/.local/state/codex-runner}"
QUEUE_FILE="${QUEUE_FILE:-${HOME}/.local/state/codex-agent-codex-queue/latest.tsv}"
PROJECTS_ROOT="${PROJECTS_ROOT:-${HOME}/projects}"
LOG_DIR="${CODEX_RUNNER_LOG_DIR:-${STATE_DIR}}"
RUNS_FILE="${STATE_DIR}/runs.tsv"
COMPLETED_DIR="${STATE_DIR}/completed"
ERROR_LOG="${STATE_DIR}/errors.log"
LOCK_FILE="${STATE_DIR}/runner.lock"
LAST_START_FILE="${STATE_DIR}/last-start-minute"
NOHUP_BIN="${NOHUP_BIN:-nohup}"
SHELL_BIN="${SHELL_BIN:-bash}"
WAIT_FOR_CHILD="${RUNNER_WAIT_FOR_CHILD:-0}"

mkdir -p "${STATE_DIR}" "${LOG_DIR}" "${COMPLETED_DIR}"
touch "${RUNS_FILE}" "${ERROR_LOG}"

log_error() {
  printf '%s\t%s\n' "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" "$*" >> "${ERROR_LOG}"
}

fail() {
  log_error "$*"
  printf '%s\n' "$*" >&2
  exit 1
}

priority_rank() {
  case "$1" in
    *priority/p0*) printf '0' ;;
    *priority/p1*) printf '1' ;;
    *priority/p2*) printf '2' ;;
    *priority/p3*) printf '3' ;;
    *) printf '9' ;;
  esac
}

repo_slug() {
  printf '%s' "$1" | tr '/' '-'
}

issue_slug() {
  printf '%s-%s' "$(printf '%s' "$1" | tr '/' '_')" "$2"
}

agent_comms_root_for_repo() {
  local repo_path
  repo_path="$1"
  if [[ -n "${AGENT_COMMS_ROOT:-}" ]]; then
    printf '%s\n' "${AGENT_COMMS_ROOT}"
    return
  fi

  printf '%s/.agent-comms\n' "${repo_path}"
}

fetch_issue_metadata() {
  local repo issue
  repo="$1"
  issue="$2"
  gh issue view "${issue}" --repo "${repo}" --json createdAt,labels --jq '[.createdAt, (.labels | map(.name) | join(","))] | @tsv'
}

has_active_run() {
  local repo issue marker line ts run_repo run_issue run_pid
  repo="$1"
  issue="$2"
  marker="${COMPLETED_DIR}/$(issue_slug "${repo}" "${issue}").done"
  [[ -f "${RUNS_FILE}" ]] || return 1

  while IFS=$'\t' read -r ts run_repo run_issue run_pid; do
    [[ -n "${run_repo}" ]] || continue
    if [[ "${run_repo}" == "${repo}" && "${run_issue}" == "${issue}" && ! -f "${marker}" ]]; then
      return 0
    fi
  done < "${RUNS_FILE}"

  return 1
}

pick_candidate() {
  local line repo issue updated_at title url created_at labels priority repo_path
  local candidates=()

  [[ -f "${QUEUE_FILE}" ]] || fail "queue file not found: ${QUEUE_FILE}"

  while IFS= read -r line || [[ -n "${line}" ]]; do
    [[ -n "${line}" ]] || continue
    [[ "${line}" == agent/codex\ sweep\ at* ]] && continue

    IFS=$'\t' read -r repo issue updated_at title url created_at labels <<< "${line}"
    [[ -n "${repo:-}" && -n "${issue:-}" ]] || continue

    issue="${issue#\#}"
    if [[ -z "${created_at:-}" || -z "${labels:-}" ]]; then
      IFS=$'\t' read -r created_at labels < <(fetch_issue_metadata "${repo}" "${issue}") || fail "failed to fetch metadata for ${repo}#${issue}"
    fi

    case ",${labels}," in
      *,agent/codex/working,*|*,agent/codex/needs-input,*|*,agent/codex/blocked,*)
        continue
        ;;
    esac

    if has_active_run "${repo}" "${issue}"; then
      continue
    fi

    repo_path="${PROJECTS_ROOT}/$(basename "${repo}")"
    [[ -d "${repo_path}" ]] || fail "repo path not found for ${repo}: ${repo_path}"
    priority="$(priority_rank "${labels}")"
    candidates+=("${priority}"$'\t'"${created_at}"$'\t'"${repo}"$'\t'"${issue}"$'\t'"${repo_path}")
  done < "${QUEUE_FILE}"

  if [[ "${#candidates[@]}" -eq 0 ]]; then
    return 1
  fi

  printf '%s\n' "${candidates[@]}" | sort -t $'\t' -k1,1n -k2,2 | head -n1
}

start_issue() {
  local repo issue repo_path agent_comms_root log_file marker brief escaped_repo_path escaped_brief escaped_agent_root runner_cmd pid minute_key
  repo="$1"
  issue="$2"
  repo_path="$3"
  minute_key="$(date -u +"%Y-%m-%dT%H:%M")"

  if [[ -f "${LAST_START_FILE}" && "$(cat "${LAST_START_FILE}")" == "${minute_key}" ]]; then
    printf 'already started this minute\n'
    return 0
  fi

  agent_comms_root="$(agent_comms_root_for_repo "${repo_path}")"
  log_file="${LOG_DIR}/$(repo_slug "${repo}")-${issue}.log"
  marker="${COMPLETED_DIR}/$(issue_slug "${repo}" "${issue}").done"
  brief="Read ${repo} issue #${issue} via \`gh issue view ${issue}\` and execute it end-to-end per the brief. Respect AGENTS.md and the issue constraints."

  escaped_repo_path="$(printf '%q' "${repo_path}")"
  escaped_brief="$(printf '%q' "${brief}")"
  escaped_agent_root="$(printf '%q' "${agent_comms_root}")"

  runner_cmd="mkdir -p $(printf '%q' "${COMPLETED_DIR}") && AGENT_COMMS_ROOT=${escaped_agent_root} codex exec --cd ${escaped_repo_path} --sandbox danger-full-access ${escaped_brief}; status=\$?; printf '%s\t%s\t%s\n' \"\$(date -u +\"%Y-%m-%dT%H:%M:%SZ\")\" \"${repo}\" \"\$status\" > $(printf '%q' "${marker}"); exit \$status"

  "${NOHUP_BIN}" "${SHELL_BIN}" -lc "${runner_cmd}" > "${log_file}" 2>&1 &
  pid=$!
  if [[ "${WAIT_FOR_CHILD}" == "1" ]]; then
    wait "${pid}" || true
  fi
  printf '%s\n' "${minute_key}" > "${LAST_START_FILE}"
  printf '%s\t%s\t%s\t%s\n' "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" "${repo}" "${issue}" "${pid}" >> "${RUNS_FILE}"
  printf 'started %s#%s pid=%s\n' "${repo}" "${issue}" "${pid}"
}

main() {
  exec 9>"${LOCK_FILE}"
  if ! flock -n 9; then
    printf 'runner locked\n'
    exit 0
  fi

  if pgrep -f "codex exec" >/dev/null 2>&1; then
    printf 'busy\n'
    exit 0
  fi

  local candidate priority created_at repo issue repo_path
  if ! candidate="$(pick_candidate)"; then
    printf 'no eligible issues: queue empty or already covered\n'
    exit 0
  fi

  IFS=$'\t' read -r priority created_at repo issue repo_path <<< "${candidate}"
  start_issue "${repo}" "${issue}" "${repo_path}"
}

main "$@"
