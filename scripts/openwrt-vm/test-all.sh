#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
PORTAL_DIR="${REPO_ROOT}/portal"

TIER="blocking"

usage() {
  cat <<'EOF'
Usage: bash scripts/openwrt-vm/test-all.sh --tier blocking|regression

Tiers:
  blocking    Go tests -> component -> API contract blocking -> UI blocking
  regression  Go tests -> component -> API contract regression -> UI regression
EOF
}

log() {
  printf '[test-all] %s\n' "$*"
}

fail() {
  printf '[test-all][ERROR] %s\n' "$*" >&2
  exit 1
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --tier=*)
        TIER="${1#*=}"
        ;;
      --tier)
        shift
        [[ $# -gt 0 ]] || fail '--tier 缺少参数'
        TIER="$1"
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        usage
        fail "未知参数: $1"
        ;;
    esac
    shift
  done

  case "${TIER}" in
    blocking|regression) ;;
    *) fail "--tier 仅支持 blocking 或 regression，当前: ${TIER}" ;;
  esac
}

run_component_tests() {
  log '运行组件快速层: npm run test:component'
  (
    cd "${PORTAL_DIR}"
    npm run test:component
  )
}

run_go_tests() {
  log '运行后端快速层: (cd server && go test ./...)'
  (
    cd "${REPO_ROOT}/server"
    go test ./...
  )
}

run_api_contract_tests() {
  log "运行 API 契约层: suite=${TIER}"
  bash "${SCRIPT_DIR}/test-api-contract.sh" --suite="${TIER}"
}

run_ui_e2e_tests() {
  log "运行浏览器 E2E 层: suite=${TIER}"
  bash "${SCRIPT_DIR}/test-ui-e2e.sh" --suite="${TIER}"
}

main() {
  parse_args "$@"
  log "开始执行 tier=${TIER} (Go -> component -> API contract -> UI E2E)"

  run_go_tests
  run_component_tests
  run_api_contract_tests
  run_ui_e2e_tests

  log "tier=${TIER} 全部通过"
}

main "$@"
