#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

SSH_BIN="${SSH_BIN:-ssh}"
CMP_BIN="${CMP_BIN:-cmp}"

LOG_FILE="${OPENWRT_VM_LOG_DIR}/hotplug-$(date +%Y%m%d-%H%M%S).log"
mkdir -p "${OPENWRT_VM_LOG_DIR}" "${OPENWRT_VM_RUN_DIR}"
exec > >(tee -a "${LOG_FILE}") 2>&1

declare -a TMP_FILES=()
SSH_BASE=()
CAPTURE_RC=0
CAPTURE_STDOUT_FILE=""
CAPTURE_STDERR_FILE=""
GUEST_READY=0

cleanup() {
  local exit_code=$?
  local f

  if (( GUEST_READY == 1 )) && (( ${#SSH_BASE[@]} > 0 )); then
    log '退出前执行 guest 清理，确保 target 文件与 fwmark 规则不残留'
    run_guest_cleanup_capture 'trap-cleanup'
    if (( CAPTURE_RC != 0 )); then
      log 'trap cleanup 未完全成功，保留 stdout/stderr 供诊断'
    fi
    log_capture 'trap cleanup'
  fi

  for f in "${TMP_FILES[@]}"; do
    [[ -n "${f}" ]] && rm -f "${f}" || true
  done

  trap - EXIT
  exit "${exit_code}"
}
trap cleanup EXIT

log() {
  printf '[hotplug] %s\n' "$*"
}

fail() {
  printf '[hotplug][ERROR] %s\n' "$*" >&2
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

prepare_tools() {
  SSH_BIN="$(resolve_command "${SSH_BIN}")" || fail "缺少 ssh 可执行文件: ${SSH_BIN}"
  CMP_BIN="$(resolve_command "${CMP_BIN}")" || fail "缺少 cmp 可执行文件: ${CMP_BIN}"

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

run_ssh_script() {
  "${SSH_BASE[@]}" sh -s -- "$@"
}

run_ssh_capture() {
  local label="$1"
  shift

  local stdout_file stderr_file
  stdout_file="$(mktemp "${OPENWRT_VM_RUN_DIR}/hotplug.${label}.stdout.XXXXXX")"
  stderr_file="$(mktemp "${OPENWRT_VM_RUN_DIR}/hotplug.${label}.stderr.XXXXXX")"
  TMP_FILES+=("${stdout_file}" "${stderr_file}")

  if "${SSH_BASE[@]}" "$@" >"${stdout_file}" 2>"${stderr_file}"; then
    CAPTURE_RC=0
  else
    CAPTURE_RC=$?
  fi

  CAPTURE_STDOUT_FILE="${stdout_file}"
  CAPTURE_STDERR_FILE="${stderr_file}"
}

run_ssh_capture_script() {
  local label="$1"
  shift

  local stdout_file stderr_file
  stdout_file="$(mktemp "${OPENWRT_VM_RUN_DIR}/hotplug.${label}.stdout.XXXXXX")"
  stderr_file="$(mktemp "${OPENWRT_VM_RUN_DIR}/hotplug.${label}.stderr.XXXXXX")"
  TMP_FILES+=("${stdout_file}" "${stderr_file}")

  if "${SSH_BASE[@]}" sh -s -- "$@" >"${stdout_file}" 2>"${stderr_file}"; then
    CAPTURE_RC=0
  else
    CAPTURE_RC=$?
  fi

  CAPTURE_STDOUT_FILE="${stdout_file}"
  CAPTURE_STDERR_FILE="${stderr_file}"
}

run_guest_cleanup_capture() {
  local label="$1"

  run_ssh_capture_script "${label}" <<'EOF'
set -eu

target='/usr/share/nftables.d/table-post/transparent.nft'

drain_fwmark_rules() {
  rounds=0
  while [ "$rounds" -lt 10 ]; do
    prefs="$(ip rule show | sed -n 's/^\([0-9][0-9]*\):.*fwmark 0x1 lookup 100.*/\1/p')"
    [ -n "$prefs" ] || return 0

    for pref in $prefs; do
      ip rule del priority "$pref" >/dev/null 2>&1 || true
    done

    rounds=$((rounds + 1))
  done

  return 1
}

drain_table_100_routes() {
  rounds=0
  while [ "$rounds" -lt 10 ]; do
    route_output="$(ip route show table 100 || true)"
    [ -z "$route_output" ] && return 0

    ip route flush table 100 >/dev/null 2>&1 || true
    while ip route del local 0.0.0.0/0 dev lo table 100 >/dev/null 2>&1; do :; done

    rounds=$((rounds + 1))
  done

  return 1
}

/etc/transparent-proxy/disable.sh >/dev/null 2>&1 || true
ACTION=ifdown INTERFACE=wan /etc/hotplug.d/iface/80-ifup-wan >/dev/null 2>&1 || true

drain_fwmark_rules
drain_table_100_routes

rm -f "$target"

rule_count="$(ip rule show | grep -c 'fwmark 0x1 lookup 100' || true)"
route_output="$(ip route show table 100 || true)"

printf 'cleanup-rule-count=%s\n' "$rule_count"
printf -- 'cleanup-routes=%s\n' "${route_output:-<empty>}"
printf 'cleanup-target=%s\n' "$([ -e "$target" ] && printf present || printf absent)"

[ ! -e "$target" ]
[ "$rule_count" = '0' ]
[ -z "$route_output" ]
EOF
}

ssh_ubus_ready() {
  run_ssh 'ubus call system board' >/dev/null 2>&1
}

ensure_vm_running_and_ready() {
  local need_boot=1

  if [[ -f "${OPENWRT_VM_PID_FILE}" ]]; then
    local pid
    pid="$(<"${OPENWRT_VM_PID_FILE}")"
    if [[ -n "${pid}" ]] && kill -0 "${pid}" >/dev/null 2>&1; then
      need_boot=0
      log "检测到运行中的 VM pid=${pid}"
    fi
  fi

  if (( need_boot == 1 )); then
    log '未检测到运行中的 VM，执行 boot-vm.sh'
    bash "${SCRIPT_DIR}/boot-vm.sh"
  fi

  if ssh_ubus_ready; then
    log 'VM SSH/ubus 已就绪，跳过 wait-ready 串口阶段'
  else
    log 'VM 尚未就绪，执行 wait-ready.sh'
    bash "${SCRIPT_DIR}/wait-ready.sh"
  fi

  ssh_ubus_ready || fail 'VM 未就绪：ubus call system board 失败'
  GUEST_READY=1
  log 'VM 运行与就绪检查通过'
}

ensure_canonical_deploy() {
  log '执行 deploy.sh，确保 canonical guest layout 已部署'
  bash "${SCRIPT_DIR}/deploy.sh"

  run_ssh 'test -x /etc/hotplug.d/iface/80-ifup-wan && test -x /etc/transparent-proxy/enable.sh && test -x /etc/transparent-proxy/disable.sh && test -f /etc/transparent-proxy/transparent.nft && test -f /etc/transparent-proxy/transparent_full.nft && test -d /usr/share/nftables.d/table-post' \
    || fail 'guest hotplug/enable/disable 布局断言失败'

  log 'canonical deploy 断言通过'
}

ensure_guest_clean_state() {
  log '清理 guest 状态：移除 table 100 规则/路由与 transparent.nft target 文件'
  run_guest_cleanup_capture 'ensure-clean-state'
  [[ ${CAPTURE_RC} -eq 0 ]] || fail "guest 清理失败: rc=${CAPTURE_RC}, stdout=$(<"${CAPTURE_STDOUT_FILE}"), stderr=$(<"${CAPTURE_STDERR_FILE}")"
  log_capture 'guest 清理'

  assert_guest_clean_state 'clean-baseline'
}

ensure_enable_prerequisite_chains() {
  log '准备 enable.sh 依赖的 fw4 jump target 链：transparent_proxy / transparent_proxy_mask（缺失则创建空链）'
  run_ssh_script <<'EOF'
set -eu

nft list table inet fw4 >/dev/null 2>&1

ensure_chain() {
  chain_name="$1"
  if nft list chain inet fw4 "$chain_name" >/dev/null 2>&1; then
    printf 'chain ready: %s\n' "$chain_name"
    return 0
  fi

  nft add chain inet fw4 "$chain_name"
  nft list chain inet fw4 "$chain_name" >/dev/null 2>&1
  printf 'chain created: %s\n' "$chain_name"
}

ensure_chain transparent_proxy
ensure_chain transparent_proxy_mask
EOF
}

assert_guest_clean_state() {
  local stage="$1"

  run_ssh_script <<'EOF'
set -eu

target='/usr/share/nftables.d/table-post/transparent.nft'
rule_count="$(ip rule show | grep -c 'fwmark 0x1 lookup 100' || true)"
route_output="$(ip route show table 100 || true)"

[ ! -e "$target" ]
[ "$rule_count" = '0' ]
[ -z "$route_output" ]
EOF

  log "清理态断言通过: ${stage}"
}

capture_enable_state_snapshot() {
  local label="$1"

  run_ssh_capture_script "${label}" <<'EOF'
set -eu

target='/usr/share/nftables.d/table-post/transparent.nft'

if [ -e "$target" ]; then
  printf 'target=present\n'
else
  printf 'target=absent\n'
fi

printf -- '--- target-content ---\n'
if [ -e "$target" ]; then
  cat "$target"
else
  printf '<missing>\n'
fi

printf -- '--- mangle_prerouting ---\n'
nft list chain inet fw4 mangle_prerouting

printf -- '--- mangle_output ---\n'
nft list chain inet fw4 mangle_output
EOF

  [[ ${CAPTURE_RC} -eq 0 ]] || fail "抓取 enable 状态快照失败: rc=${CAPTURE_RC}, stderr=$(<"${CAPTURE_STDERR_FILE}")"
}

capture_hotplug_state_snapshot() {
  local label="$1"

  run_ssh_capture_script "${label}" <<'EOF'
set -eu

rule_count="$(ip rule show | grep -c 'fwmark 0x1 lookup 100' || true)"
route_output="$(ip route show table 100 || true)"

printf 'rule_count=%s\n' "$rule_count"
printf -- '--- rules ---\n'
ip rule show | grep 'fwmark 0x1 lookup 100' || true
printf -- '--- route-table-100 ---\n'
printf '%s\n' "$route_output"
EOF

  [[ ${CAPTURE_RC} -eq 0 ]] || fail "抓取 hotplug 状态快照失败: rc=${CAPTURE_RC}, stderr=$(<"${CAPTURE_STDERR_FILE}")"
}

extract_snapshot_value() {
  local file="$1"
  local key="$2"

  grep -E "^${key}=" "${file}" | head -n 1 | cut -d= -f2-
}

snapshot_files_equal() {
  local left="$1"
  local right="$2"

  "${CMP_BIN}" -s "${left}" "${right}"
}

capture_contains_duplicate_signal() {
  local file="$1"

  grep -Eqi 'File exists|RTNETLINK answers|exists' "${file}"
}

combined_capture_has_duplicate_signal() {
  capture_contains_duplicate_signal "${CAPTURE_STDOUT_FILE}" || capture_contains_duplicate_signal "${CAPTURE_STDERR_FILE}"
}

log_capture() {
  local prefix="$1"

  if [[ -s "${CAPTURE_STDOUT_FILE}" ]]; then
    log "${prefix} stdout: $(<"${CAPTURE_STDOUT_FILE}")"
  fi
  if [[ -s "${CAPTURE_STDERR_FILE}" ]]; then
    log "${prefix} stderr: $(<"${CAPTURE_STDERR_FILE}")"
  fi
}

assert_enable_applied() {
  run_ssh_script <<'EOF'
set -eu

target='/usr/share/nftables.d/table-post/transparent.nft'
prerouting="$(nft list chain inet fw4 mangle_prerouting)"
output="$(nft list chain inet fw4 mangle_output)"

test -f "$target"
grep -F -- 'chain mangle_prerouting' "$target" >/dev/null
grep -F -- 'chain mangle_output' "$target" >/dev/null

case "$prerouting" in
  *'jump transparent_proxy'*) ;;
  *) exit 1 ;;
esac

case "$output" in
  *'jump transparent_proxy_mask'*) ;;
  *) exit 1 ;;
esac
EOF

  log 'enable 语义断言通过：target 文件存在，fw4 链可访问且规则已写入'
}

assert_disable_applied() {
  run_ssh_script <<'EOF'
set -eu

target='/usr/share/nftables.d/table-post/transparent.nft'
prerouting="$(nft list chain inet fw4 mangle_prerouting)"
output="$(nft list chain inet fw4 mangle_output)"

[ ! -e "$target" ]

case "$prerouting" in
  *'jump transparent_proxy'*) exit 1 ;;
  *) ;;
esac

case "$output" in
  *'jump transparent_proxy_mask'*) exit 1 ;;
  *) ;;
esac
EOF

  log 'disable 语义断言通过：target 文件已删除，fw4 链仍可访问且规则已清空'
}

assert_ifup_applied() {
  run_ssh_script <<'EOF'
set -eu

rule_count="$(ip rule show | grep -c 'fwmark 0x1 lookup 100' || true)"
route_output="$(ip route show table 100 || true)"

[ "$rule_count" = '1' ]
[ -n "$route_output" ]

case "$route_output" in
  *'dev lo'*) ;;
  *) exit 1 ;;
esac
EOF

  log 'ifup 语义断言通过：fwmark rule 与 table 100 路由均已出现'
}

assert_ifdown_applied() {
  run_ssh_script <<'EOF'
set -eu

rule_count="$(ip rule show | grep -c 'fwmark 0x1 lookup 100' || true)"
route_output="$(ip route show table 100 || true)"

[ "$rule_count" = '0' ]
[ -z "$route_output" ]
EOF

  log 'ifdown 语义断言通过：fwmark rule 与 table 100 路由均已清理'
}

classify_repeated_result() {
  local name="$1"
  local before_file="$2"
  local after_file="$3"
  local result_rc="$4"
  local result_stdout_file="$5"
  local result_stderr_file="$6"

  if ! snapshot_files_equal "${before_file}" "${after_file}"; then
    log "重复 ${name} 前快照: $(<"${before_file}")"
    log "重复 ${name} 后快照: $(<"${after_file}")"
    if [[ -s "${result_stdout_file}" ]]; then
      log "重复 ${name} 漂移时 stdout: $(<"${result_stdout_file}")"
    fi
    if [[ -s "${result_stderr_file}" ]]; then
      log "重复 ${name} 漂移时 stderr: $(<"${result_stderr_file}")"
    fi
    fail "重复 ${name} 后 guest 状态发生漂移；前后快照不一致"
  fi

  if (( result_rc == 0 )); then
    if capture_contains_duplicate_signal "${result_stdout_file}" || capture_contains_duplicate_signal "${result_stderr_file}"; then
      log "重复 ${name} 分类：状态未漂移，但输出显式暴露 duplicate/File exists 条件（脚本出口=0）"
    else
      log "重复 ${name} 分类：幂等成功，第二次执行返回 0 且状态不变"
    fi
  else
    log "重复 ${name} 分类：明确失败（rc=${result_rc}），且状态未漂移"
  fi

  if [[ -s "${result_stdout_file}" ]]; then
    log "重复 ${name} stdout: $(<"${result_stdout_file}")"
  fi
  if [[ -s "${result_stderr_file}" ]]; then
    log "重复 ${name} stderr: $(<"${result_stderr_file}")"
  fi
}

test_enable_disable_semantics() {
  log '步骤 4/5：执行 enable.sh / disable.sh，并验证真实语义'

  run_ssh_capture 'enable-first' '/etc/transparent-proxy/enable.sh'
  [[ ${CAPTURE_RC} -eq 0 ]] || fail "首次 enable.sh 失败: rc=${CAPTURE_RC}, stderr=$(<"${CAPTURE_STDERR_FILE}")"
  log_capture '首次 enable'
  assert_enable_applied

  run_ssh_capture 'disable-first' '/etc/transparent-proxy/disable.sh'
  [[ ${CAPTURE_RC} -eq 0 ]] || fail "首次 disable.sh 失败: rc=${CAPTURE_RC}, stderr=$(<"${CAPTURE_STDERR_FILE}")"
  log_capture '首次 disable'
  assert_disable_applied
}

test_hotplug_semantics() {
  log '步骤 6/7：执行 ifup / ifdown，并验证 table 100 与 fwmark 规则语义'

  run_ssh_capture 'ifup-first' 'env' 'ACTION=ifup' 'INTERFACE=wan' '/etc/hotplug.d/iface/80-ifup-wan'
  [[ ${CAPTURE_RC} -eq 0 ]] || fail "首次 ifup 失败: rc=${CAPTURE_RC}, stderr=$(<"${CAPTURE_STDERR_FILE}")"
  log_capture '首次 ifup'
  assert_ifup_applied

  run_ssh_capture 'ifdown-first' 'env' 'ACTION=ifdown' 'INTERFACE=wan' '/etc/hotplug.d/iface/80-ifup-wan'
  [[ ${CAPTURE_RC} -eq 0 ]] || fail "首次 ifdown 失败: rc=${CAPTURE_RC}, stderr=$(<"${CAPTURE_STDERR_FILE}")"
  log_capture '首次 ifdown'
  assert_ifdown_applied
}

test_repeated_enable_behavior() {
  local before_snapshot after_snapshot result_rc result_stdout result_stderr

  log '步骤 8a：验证 repeated enable 的确定性行为'
  ensure_guest_clean_state

  run_ssh_capture 'enable-repeat-prime' '/etc/transparent-proxy/enable.sh'
  [[ ${CAPTURE_RC} -eq 0 ]] || fail "重复 enable 前的基线 enable 失败: rc=${CAPTURE_RC}, stderr=$(<"${CAPTURE_STDERR_FILE}")"
  assert_enable_applied

  capture_enable_state_snapshot 'enable-repeat-before'
  before_snapshot="${CAPTURE_STDOUT_FILE}"

  run_ssh_capture 'enable-repeat-second' '/etc/transparent-proxy/enable.sh'
  result_rc="${CAPTURE_RC}"
  result_stdout="${CAPTURE_STDOUT_FILE}"
  result_stderr="${CAPTURE_STDERR_FILE}"
  capture_enable_state_snapshot 'enable-repeat-after'
  after_snapshot="${CAPTURE_STDOUT_FILE}"

  classify_repeated_result 'enable' "${before_snapshot}" "${after_snapshot}" "${result_rc}" "${result_stdout}" "${result_stderr}"
  assert_enable_applied

  run_ssh_capture 'enable-repeat-disable' '/etc/transparent-proxy/disable.sh'
  [[ ${CAPTURE_RC} -eq 0 ]] || fail "重复 enable 后 cleanup disable 失败: rc=${CAPTURE_RC}, stderr=$(<"${CAPTURE_STDERR_FILE}")"
  assert_disable_applied
}

test_repeated_ifup_behavior() {
  local before_snapshot after_snapshot rule_count result_rc result_stdout result_stderr

  log '步骤 8b：验证 repeated ifup 的确定性行为'
  ensure_guest_clean_state

  run_ssh_capture 'ifup-repeat-prime' 'env' 'ACTION=ifup' 'INTERFACE=wan' '/etc/hotplug.d/iface/80-ifup-wan'
  [[ ${CAPTURE_RC} -eq 0 ]] || fail "重复 ifup 前的首轮 ifup 失败: rc=${CAPTURE_RC}, stderr=$(<"${CAPTURE_STDERR_FILE}")"
  assert_ifup_applied

  capture_hotplug_state_snapshot 'ifup-repeat-before'
  before_snapshot="${CAPTURE_STDOUT_FILE}"
  rule_count="$(extract_snapshot_value "${before_snapshot}" 'rule_count')"
  [[ "${rule_count}" == '1' ]] || fail "重复 ifup 基线状态异常：rule_count=${rule_count}"

  run_ssh_capture 'ifup-repeat-second' 'env' 'ACTION=ifup' 'INTERFACE=wan' '/etc/hotplug.d/iface/80-ifup-wan'
  result_rc="${CAPTURE_RC}"
  result_stdout="${CAPTURE_STDOUT_FILE}"
  result_stderr="${CAPTURE_STDERR_FILE}"
  capture_hotplug_state_snapshot 'ifup-repeat-after'
  after_snapshot="${CAPTURE_STDOUT_FILE}"

  classify_repeated_result 'ifup' "${before_snapshot}" "${after_snapshot}" "${result_rc}" "${result_stdout}" "${result_stderr}"

  rule_count="$(extract_snapshot_value "${after_snapshot}" 'rule_count')"
  [[ "${rule_count}" == '1' ]] || fail "重复 ifup 后 rule_count 漂移：${rule_count}"

  run_ssh_capture 'ifup-repeat-ifdown' 'env' 'ACTION=ifdown' 'INTERFACE=wan' '/etc/hotplug.d/iface/80-ifup-wan'
  [[ ${CAPTURE_RC} -eq 0 ]] || fail "重复 ifup 后 ifdown 失败: rc=${CAPTURE_RC}, stderr=$(<"${CAPTURE_STDERR_FILE}")"
  assert_ifdown_applied
}

main() {
  log "开始执行 OpenWrt hotplug suite，日志文件: ${LOG_FILE}"
  prepare_tools

  ensure_vm_running_and_ready
  ensure_canonical_deploy
  ensure_guest_clean_state
  ensure_enable_prerequisite_chains

  test_enable_disable_semantics
  test_hotplug_semantics
  test_repeated_enable_behavior
  test_repeated_ifup_behavior

  ensure_guest_clean_state
  assert_guest_clean_state 'suite-final'

  log 'hotplug / enable-disable / repeated-behavior 全部断言通过'
}

main "$@"
