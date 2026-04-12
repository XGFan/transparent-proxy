#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

SSH_BIN="${SSH_BIN:-ssh}"
EVIDENCE_DIR="${OPENWRT_VM_REPO_ROOT}/.sisyphus/evidence"
GUEST_SERVER="${OPENWRT_GUEST_SERVER}"
GUEST_CONFIG="${OPENWRT_GUEST_CONFIG}"
GUEST_INITD="${OPENWRT_GUEST_INITD}"
EXPECTED_SERVICE_COMMAND="/usr/bin/transparent-proxy -c ${GUEST_CONFIG}"

SSH_BASE=()
ARTIFACT_HOOK_ENABLED=0

cleanup() {
  local exit_code=$?

  if (( exit_code != 0 )) && (( ARTIFACT_HOOK_ENABLED == 1 )); then
    set +e
    log "检测到失败，开始收集 VM artifacts"
    bash "${SCRIPT_DIR}/collect-artifacts.sh"
    local collect_rc=$?
    if (( collect_rc != 0 )); then
      log "collect-artifacts.sh 返回非 0（rc=${collect_rc}），已保留可用证据"
    fi
    set -e
  fi

  trap - EXIT
  exit "${exit_code}"
}
trap cleanup EXIT

log() {
  printf '[test-common] %s\n' "$*" >&2
}

fail() {
  printf '[test-common][ERROR] %s\n' "$*" >&2
  exit 1
}

resolve_command() {
  local cmd="$1"

  if [[ "${cmd}" == */* ]]; then
    [[ -x "${cmd}" ]] || return 1
    printf '%s\n' "${cmd}"
    return 0
  fi

  command -v "${cmd}"
}

prepare_directories() {
  mkdir -p \
    "${OPENWRT_VM_WORK_DIR}" \
    "${OPENWRT_VM_RUN_DIR}" \
    "${OPENWRT_VM_STATE_DIR}" \
    "${OPENWRT_VM_LOG_DIR}" \
    "${EVIDENCE_DIR}" \
    "${TP_PLAYWRIGHT_ARTIFACT_DIR}"
}

prepare_ssh() {
  SSH_BIN="$(resolve_command "${SSH_BIN}")" || fail "缺少 ssh 可执行文件: ${SSH_BIN}"
  [[ -f "${OPENWRT_TEST_KEY_PATH}" ]] || fail "测试 SSH 私钥不存在: ${OPENWRT_TEST_KEY_PATH}"

  SSH_BASE=(
    "${SSH_BIN}"
    -o StrictHostKeyChecking=no
    -o UserKnownHostsFile=/dev/null
    -o ConnectTimeout=5
    -o BatchMode=yes
    -i "${OPENWRT_TEST_KEY_PATH}"
    -p "${SSH_PORT}"
    "root@${QEMU_HOST}"
  )
}

run_ssh() {
  "${SSH_BASE[@]}" "$@"
}

vm_pid_running() {
  if [[ ! -f "${OPENWRT_VM_PID_FILE}" ]]; then
    return 1
  fi

  local pid
  pid="$(<"${OPENWRT_VM_PID_FILE}")"
  [[ -n "${pid}" ]] || return 1

  kill -0 "${pid}" >/dev/null 2>&1
}

vm_ubus_ready() {
  run_ssh 'ubus call system board' >/dev/null 2>&1
}

ensure_vm_ready() {
  ARTIFACT_HOOK_ENABLED=1
  prepare_directories
  prepare_ssh

  if vm_pid_running; then
    log "检测到运行中的 VM（pid=$(<"${OPENWRT_VM_PID_FILE}")）"
  else
    log '未检测到运行中的 VM，执行 boot-vm.sh'
    bash "${SCRIPT_DIR}/boot-vm.sh"
  fi

  if vm_ubus_ready; then
    log 'VM SSH/ubus 已就绪，继续执行 wait-ready.sh（幂等确认）'
  else
    log 'VM 尚未就绪，执行 wait-ready.sh'
  fi
  bash "${SCRIPT_DIR}/wait-ready.sh"

  vm_ubus_ready || fail 'VM 未就绪：ubus call system board 失败'

  log '执行 deploy.sh，确保 canonical guest layout 已部署'
  bash "${SCRIPT_DIR}/deploy.sh"

  run_ssh sh -s -- "${GUEST_SERVER}" "${GUEST_CONFIG}" "${GUEST_INITD}" "${EXPECTED_SERVICE_COMMAND}" <<'EOF' \
    || fail 'guest canonical 布局或服务 wiring 断言失败'
set -eu

guest_server="$1"
guest_config="$2"
guest_initd="$3"
expected_service_command="$4"

test -x "${guest_server}"
test -f "${guest_config}"
test -f "${guest_initd}"
grep -F -- "${expected_service_command}" "${guest_initd}" >/dev/null
EOF

  log 'VM ready + deploy 断言通过'
}

print_contract() {
  local key
  local value
  local keys=(
    TP_API_BASE_URL
    TP_UI_BASE_URL
    TP_REFRESH_ROUTE_FIXTURE
    TP_TEST_SUITE_TIER
    TP_PLAYWRIGHT_ARTIFACT_DIR
  )

  for key in "${keys[@]}"; do
    value="${!key:-}"
    [[ -n "${value}" ]] || fail "契约变量为空: ${key}"
    printf '%s=%s\n' "${key}" "${value}"
  done
}

usage() {
  cat <<'EOF'
Usage: bash scripts/openwrt-vm/test-common.sh [--print-contract|--ensure-vm-ready]

Commands:
  --print-contract   Print unified shared test contract values
  --ensure-vm-ready  Ensure VM boot/wait-ready/deploy flow succeeds
EOF
}

main() {
  local command="${1:-}"
  if [[ $# -gt 1 ]]; then
    fail "参数过多，当前仅支持一个命令参数"
  fi

  case "${command}" in
    --print-contract)
      prepare_directories
      print_contract
      ;;
    --ensure-vm-ready)
      ensure_vm_ready
      ;;
    -h|--help)
      usage
      ;;
    *)
      usage
      fail "未知参数: ${command:-<empty>}"
      ;;
  esac
}

main "$@"
