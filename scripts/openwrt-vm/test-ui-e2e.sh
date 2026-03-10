#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

SUITE="${TP_TEST_SUITE_TIER:-blocking}"
LIST_ONLY=0
GREP_TAG=""
FORCE_FAIL=0
declare -a PLAYWRIGHT_ARGS=()

RUN_ID="$(date +%Y%m%d-%H%M%S)"
PLAYWRIGHT_ARTIFACT_ROOT="${TP_PLAYWRIGHT_ARTIFACT_DIR}"
PLAYWRIGHT_ARTIFACT_DIR=""
WRAPPER_LOG_FILE=""

log() {
  printf '[ui-e2e] %s\n' "$*"
}

fail() {
  printf '[ui-e2e][ERROR] %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: bash scripts/openwrt-vm/test-ui-e2e.sh [options] [-- <extra-playwright-args>]

Options:
  --suite=<blocking|regression>   Playwright project suite (default: blocking)
  --suite <name>                  Same as above
  --grep=<pattern>                Forward --grep pattern to Playwright
  --grep <pattern>                Same as above
  --list                          List matching tests
  --deliberate-fail               Force wrapper non-zero after Playwright
  -h, --help                      Show this help message
EOF
}

collect_vm_artifacts_on_failure() {
  local exit_code=$?

  if (( exit_code != 0 )); then
    set +e
    log "检测到失败（rc=${exit_code}），收集 VM artifacts"
    bash "${SCRIPT_DIR}/collect-artifacts.sh"
    local collect_rc=$?
    if (( collect_rc != 0 )); then
      log "collect-artifacts.sh 返回非 0（rc=${collect_rc}），已尽力保留证据"
    fi
    set -e
  fi

  log "Playwright artifacts: ${PLAYWRIGHT_ARTIFACT_DIR}"
  log "VM artifacts root: ${OPENWRT_VM_WORK_DIR}/artifacts"

  trap - EXIT
  exit "${exit_code}"
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --suite=*)
        SUITE="${1#*=}"
        ;;
      --suite)
        shift
        [[ $# -gt 0 ]] || fail '--suite 缺少参数'
        SUITE="$1"
        ;;
      --grep=*)
        GREP_TAG="${1#*=}"
        ;;
      --grep)
        shift
        [[ $# -gt 0 ]] || fail '--grep 缺少参数'
        GREP_TAG="$1"
        ;;
      --list)
        LIST_ONLY=1
        ;;
      --deliberate-fail)
        FORCE_FAIL=1
        ;;
      --)
        shift
        if [[ $# -gt 0 ]]; then
          PLAYWRIGHT_ARGS+=("$@")
        fi
        return 0
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        PLAYWRIGHT_ARGS+=("$1")
        ;;
    esac
    shift
  done
}

validate_suite() {
  case "${SUITE}" in
    blocking|regression)
      ;;
    *)
      fail "无效 --suite: ${SUITE}（仅支持 blocking/regression）"
      ;;
  esac
}

run_playwright() {
  local portal_dir="${OPENWRT_VM_REPO_ROOT}/portal"
  local -a cmd=(npx playwright test --config=playwright.config.js --project "${SUITE}")

  [[ -n "${GREP_TAG}" ]] && cmd+=(--grep "${GREP_TAG}")
  (( LIST_ONLY == 1 )) && cmd+=(--list)
  ((${#PLAYWRIGHT_ARGS[@]} > 0)) && cmd+=("${PLAYWRIGHT_ARGS[@]}")

  log "执行命令: ${cmd[*]}"

  (
    cd "${portal_dir}"
    "${cmd[@]}"
  )
}

run_deliberate_failure_if_requested() {
  if (( FORCE_FAIL == 0 )); then
    return 0
  fi

  local marker_file="${PLAYWRIGHT_ARTIFACT_DIR}/harness-deliberate-failure.marker"
  printf 'deliberate failure at %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "${marker_file}"
  fail "按请求触发 deliberate failure（marker: ${marker_file}）"
}

main() {
  parse_args "$@"
  validate_suite

  export TP_TEST_SUITE_TIER="${SUITE}"
  PLAYWRIGHT_ARTIFACT_DIR="${PLAYWRIGHT_ARTIFACT_ROOT}/${RUN_ID}-${SUITE}"
  WRAPPER_LOG_FILE="${PLAYWRIGHT_ARTIFACT_DIR}/wrapper.log"

  export PORTAL_API_TARGET="${PORTAL_API_TARGET:-${TP_API_BASE_URL}}"
  export TP_CHNROUTE_FIXTURE_PATH="${TP_CHNROUTE_FIXTURE_PATH:-${TP_REFRESH_ROUTE_FIXTURE}}"
  export TP_PLAYWRIGHT_ARTIFACT_DIR="${PLAYWRIGHT_ARTIFACT_DIR}"

  mkdir -p "${PLAYWRIGHT_ARTIFACT_DIR}" "${OPENWRT_VM_LOG_DIR}" "${OPENWRT_VM_RUN_DIR}"
  exec > >(tee -a "${WRAPPER_LOG_FILE}") 2>&1

  trap collect_vm_artifacts_on_failure EXIT

  log "suite=${SUITE} list_only=${LIST_ONLY} grep=${GREP_TAG:-<none>}"
  log "deliberate_fail=${FORCE_FAIL}"
  log "PORTAL_API_TARGET=${PORTAL_API_TARGET}"
  log "TP_CHNROUTE_FIXTURE_PATH=${TP_CHNROUTE_FIXTURE_PATH}"

  log '调用共享 helper，确保 VM ready'
  bash "${SCRIPT_DIR}/test-common.sh" --ensure-vm-ready

  log '显式执行 deploy.sh，确保 guest 资产最新'
  bash "${SCRIPT_DIR}/deploy.sh"

  run_playwright
  run_deliberate_failure_if_requested
  log 'UI E2E wrapper 执行完成'
}

main "$@"
