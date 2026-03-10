#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

CURL_BIN="${CURL_BIN:-curl}"
SSH_BIN="${SSH_BIN:-ssh}"
PYTHON_BIN="${PYTHON_BIN:-python3}"

TEST_SET="proxy_dst"
TEST_IP="${SMOKE_TEST_IP:-198.18.0.123}"
API_BASE="http://${QEMU_HOST}:${API_PORT}"

REQUIRED_SETS=(
  direct_src
  direct_dst
  proxy_src
  proxy_dst
)

LOG_FILE="${OPENWRT_VM_LOG_DIR}/smoke-$(date +%Y%m%d-%H%M%S).log"
mkdir -p "${OPENWRT_VM_LOG_DIR}" "${OPENWRT_VM_RUN_DIR}"
exec > >(tee -a "${LOG_FILE}") 2>&1

declare -a TMP_FILES=()
SSH_BASE=()
API_LAST_CODE=""
API_LAST_BODY_FILE=""

cleanup() {
  local f
  for f in "${TMP_FILES[@]}"; do
    [[ -n "${f}" ]] && rm -f "${f}" || true
  done
}
trap cleanup EXIT

log() {
  printf '[smoke] %s\n' "$*"
}

fail() {
  printf '[smoke][ERROR] %s\n' "$*" >&2
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
  CURL_BIN="$(resolve_command "${CURL_BIN}")" || fail "缺少 curl 可执行文件: ${CURL_BIN}"
  SSH_BIN="$(resolve_command "${SSH_BIN}")" || fail "缺少 ssh 可执行文件: ${SSH_BIN}"
  PYTHON_BIN="$(resolve_command "${PYTHON_BIN}")" || fail "缺少 python3 可执行文件: ${PYTHON_BIN}"

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
    log '未检测到运行中的 VM，启动 boot-vm.sh'
    bash "${SCRIPT_DIR}/boot-vm.sh"
  fi

  if ssh_ubus_ready; then
    log 'VM SSH/ubus 已就绪，跳过 wait-ready 串口阶段'
  else
    log 'VM 尚未就绪，执行 wait-ready.sh'
    bash "${SCRIPT_DIR}/wait-ready.sh"
  fi

  ssh_ubus_ready || fail 'VM 未就绪：ubus call system board 失败'
  log 'VM 运行与就绪检查通过'
}

ensure_canonical_deploy() {
  log '执行 deploy.sh，确保 canonical guest layout 已部署'
  bash "${SCRIPT_DIR}/deploy.sh"

  run_ssh 'test -x /etc/init.d/transparent-proxy && test -x /etc/transparent-proxy/server && test -f /etc/transparent-proxy/config.yaml' \
    || fail 'guest canonical 布局断言失败'
  log 'canonical guest layout 断言通过'
}

restart_and_assert_service() {
  local pattern='/etc/transparent-proxy/server -c /etc/transparent-proxy/config.yaml'
  local ready=0
  local i

  log '重启 transparent-proxy 服务（restart || start）'
  run_ssh '/etc/init.d/transparent-proxy restart || /etc/init.d/transparent-proxy start'

  log '校验服务进程（pgrep -af）'
  for i in $(seq 1 20); do
    if run_ssh "pgrep -af '${pattern}'" >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done

  if (( ready == 0 )); then
    run_ssh '/etc/init.d/transparent-proxy status || true'
    run_ssh 'logread | tail -n 80 || true'
    fail 'transparent-proxy 进程不存在'
  fi

  run_ssh "pgrep -af '${pattern}'"

  if run_ssh 'test -f /var/run/transparent-proxy.pid'; then
    local pid
    pid="$(run_ssh 'cat /var/run/transparent-proxy.pid')"
    log "pidfile 存在: /var/run/transparent-proxy.pid -> ${pid}"
  else
    log 'pidfile 缺失: /var/run/transparent-proxy.pid（记录 procd 状态用于说明）'
    run_ssh "ubus call service list '{\"name\":\"transparent-proxy\"}' 2>/dev/null || true"
  fi
}

prepare_required_sets() {
  log '准备 direct_src/direct_dst/proxy_src/proxy_dst 四个 set（缺失则自愈创建）'
  run_ssh sh -s -- "${REQUIRED_SETS[@]}" <<'EOF'
set -eu

nft list table inet fw4 >/dev/null 2>&1

ensure_set() {
  set_name="$1"
  if nft list set inet fw4 "$set_name" >/dev/null 2>&1; then
    printf 'set ready: %s\n' "$set_name"
    return 0
  fi

  nft "add set inet fw4 $set_name { type ipv4_addr; flags interval; auto-merge; }"
  nft list set inet fw4 "$set_name" >/dev/null 2>&1
  printf 'set created: %s\n' "$set_name"
}

for s in "$@"; do
  ensure_set "$s"
  nft list set inet fw4 "$s" >/dev/null 2>&1
done
EOF
}

call_api() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  local body_file
  body_file="$(mktemp "${OPENWRT_VM_RUN_DIR}/smoke-api.${method}.${path//\//_}.XXXXXX")"
  TMP_FILES+=("${body_file}")

  if [[ -n "${body}" ]]; then
    API_LAST_CODE="$("${CURL_BIN}" -sS -o "${body_file}" -w '%{http_code}' -X "${method}" -H 'Content-Type: application/json' --data "${body}" --connect-timeout 5 --max-time 30 "${API_BASE}${path}")"
  else
    API_LAST_CODE="$("${CURL_BIN}" -sS -o "${body_file}" -w '%{http_code}' -X "${method}" --connect-timeout 5 --max-time 30 "${API_BASE}${path}")"
  fi

  API_LAST_BODY_FILE="${body_file}"
}

assert_api_ok_envelope() {
  "${PYTHON_BIN}" - "${API_LAST_BODY_FILE}" <<'PY'
import json
import sys

with open(sys.argv[1], 'r', encoding='utf-8') as f:
    envelope = json.load(f)

if envelope.get('code') != 'ok' or envelope.get('message') != 'ok' or not isinstance(envelope.get('data'), dict):
    print(f"unexpected envelope: {envelope}", file=sys.stderr)
    raise SystemExit(1)
PY
}

assert_rules_add_remove_response() {
  local expected_action="$1"
  local expected_set="$2"
  local expected_ip="$3"
  "${PYTHON_BIN}" - "${API_LAST_BODY_FILE}" "${expected_action}" "${expected_set}" "${expected_ip}" <<'PY'
import json
import sys

with open(sys.argv[1], 'r', encoding='utf-8') as f:
    envelope = json.load(f)

expected_action = sys.argv[2]
expected_set = sys.argv[3]
expected_ip = sys.argv[4]

if envelope.get('code') != 'ok' or envelope.get('message') != 'ok':
    print(f"unexpected envelope: {envelope}", file=sys.stderr)
    raise SystemExit(1)

data = envelope.get('data')
if not isinstance(data, dict):
    print(f"unexpected data payload: {data}", file=sys.stderr)
    raise SystemExit(1)

rule = data.get('rule')
operation = data.get('operation')
if data.get('set') != expected_set or data.get('ip') != expected_ip:
    print(f"set/ip mismatch: {data}", file=sys.stderr)
    raise SystemExit(1)
if not isinstance(rule, dict) or rule.get('name') != expected_set:
    print(f"rule mismatch: {rule}", file=sys.stderr)
    raise SystemExit(1)
if not isinstance(operation, dict) or operation.get('action') != expected_action or operation.get('result') != 'applied':
    print(f"operation mismatch: {operation}", file=sys.stderr)
    raise SystemExit(1)
PY
}

assert_rules_sync_response() {
  "${PYTHON_BIN}" - "${API_LAST_BODY_FILE}" "${TEST_SET}" <<'PY'
import json
import sys

with open(sys.argv[1], 'r', encoding='utf-8') as f:
    envelope = json.load(f)

target_set = sys.argv[2]

if envelope.get('code') != 'ok' or envelope.get('message') != 'ok':
    print(f"unexpected envelope: {envelope}", file=sys.stderr)
    raise SystemExit(1)

data = envelope.get('data')
if not isinstance(data, dict):
    print(f"unexpected data payload: {data}", file=sys.stderr)
    raise SystemExit(1)

synced = data.get('synced')
results = data.get('results')
if not isinstance(synced, list) or target_set not in synced:
    print(f"synced mismatch: {data}", file=sys.stderr)
    raise SystemExit(1)
if not isinstance(results, list) or not results:
    print(f"results mismatch: {data}", file=sys.stderr)
    raise SystemExit(1)

for item in results:
    if not isinstance(item, dict):
        print(f"invalid result item: {item}", file=sys.stderr)
        raise SystemExit(1)
    operation = item.get('operation')
    rule = item.get('rule')
    if not isinstance(operation, dict) or operation.get('action') != 'sync' or operation.get('result') != 'applied':
        print(f"operation mismatch: {item}", file=sys.stderr)
        raise SystemExit(1)
    if not isinstance(rule, dict) or not rule.get('name'):
        print(f"rule mismatch: {item}", file=sys.stderr)
        raise SystemExit(1)
    expected_output = f"/etc/nftables.d/{rule['name']}.nft"
    if operation.get('output') != expected_output:
        print(f"output mismatch: expected={expected_output}, actual={operation.get('output')}", file=sys.stderr)
        raise SystemExit(1)
PY
}

assert_status_api() {
  log '断言 GET /api/status'
  call_api 'GET' '/api/status'
  [[ "${API_LAST_CODE}" == '200' ]] || fail "/api/status HTTP ${API_LAST_CODE}，响应: $(<"${API_LAST_BODY_FILE}")"
  assert_api_ok_envelope

  "${PYTHON_BIN}" - "${API_LAST_BODY_FILE}" <<'PY'
import json
import sys

body_file = sys.argv[1]
required = {"direct_src", "direct_dst", "proxy_src", "proxy_dst"}

with open(body_file, "r", encoding="utf-8") as f:
    envelope = json.load(f)

if envelope.get("code") != "ok" or envelope.get("message") != "ok":
    print(f"/api/status envelope 异常: {envelope}", file=sys.stderr)
    raise SystemExit(1)

payload = envelope.get("data")
if not isinstance(payload, dict):
    print(f"/api/status data 不是对象: {payload}", file=sys.stderr)
    raise SystemExit(1)

if "status" not in payload or "ip" not in payload or "sets" not in payload:
    print(f"/api/status data 缺少 status/ip/sets: {payload}", file=sys.stderr)
    raise SystemExit(1)

sets = {item.get("name") for item in payload.get("sets", []) if isinstance(item, dict)}
missing = sorted(required - sets)
if missing:
    print(f"/api/status 缺少 sets: {', '.join(missing)}", file=sys.stderr)
    raise SystemExit(1)
PY

  log "/api/status 响应体: $(<"${API_LAST_BODY_FILE}")"
}

guest_set_contains_ip() {
  local set_name="$1"
  local ip="$2"

  run_ssh "nft list set inet fw4 ${set_name} | grep -F -- '${ip}' >/dev/null"
}

assert_add_remove_semantics() {
  local payload
  payload="{\"ip\":\"${TEST_IP}\",\"set\":\"${TEST_SET}\"}"

  log "预清理测试元素: ${TEST_SET} <- ${TEST_IP}"
  run_ssh "nft delete element inet fw4 ${TEST_SET} { ${TEST_IP} } >/dev/null 2>&1 || true"

  if guest_set_contains_ip "${TEST_SET}" "${TEST_IP}"; then
    fail "预期 ${TEST_SET} 不含 ${TEST_IP}，但实际仍存在"
  fi
  log "add 前断言通过：${TEST_SET} 不含 ${TEST_IP}"
  run_ssh "nft list set inet fw4 ${TEST_SET}"

  log '断言 POST /api/rules/add'
  call_api 'POST' '/api/rules/add' "${payload}"
  [[ "${API_LAST_CODE}" == '200' ]] || fail "/api/rules/add HTTP ${API_LAST_CODE}，响应: $(<"${API_LAST_BODY_FILE}")"
  assert_rules_add_remove_response 'add' "${TEST_SET}" "${TEST_IP}"

  guest_set_contains_ip "${TEST_SET}" "${TEST_IP}" || fail "/api/rules/add 后 guest set 未出现 ${TEST_IP}"
  log "/api/rules/add 后断言通过：${TEST_SET} 已包含 ${TEST_IP}"
  run_ssh "nft list set inet fw4 ${TEST_SET}"

  log '断言 POST /api/rules/remove'
  call_api 'POST' '/api/rules/remove' "${payload}"
  [[ "${API_LAST_CODE}" == '200' ]] || fail "/api/rules/remove HTTP ${API_LAST_CODE}，响应: $(<"${API_LAST_BODY_FILE}")"
  assert_rules_add_remove_response 'remove' "${TEST_SET}" "${TEST_IP}"

  if guest_set_contains_ip "${TEST_SET}" "${TEST_IP}"; then
    fail "/api/rules/remove 后 guest set 仍包含 ${TEST_IP}"
  fi
  log "/api/rules/remove 后断言通过：${TEST_SET} 已不含 ${TEST_IP}"
  run_ssh "nft list set inet fw4 ${TEST_SET}"
}

assert_sync_and_file() {
  log '断言 POST /api/rules/sync'
  call_api 'POST' '/api/rules/sync'
  [[ "${API_LAST_CODE}" == '200' ]] || fail "/api/rules/sync HTTP ${API_LAST_CODE}，响应: $(<"${API_LAST_BODY_FILE}")"
  assert_rules_sync_response

  run_ssh 'test -f /etc/nftables.d/proxy_dst.nft' || fail 'sync 后 /etc/nftables.d/proxy_dst.nft 不存在'
  run_ssh 'ls -l /etc/nftables.d/proxy_dst.nft && wc -c /etc/nftables.d/proxy_dst.nft'
  log 'sync 后文件断言通过：/etc/nftables.d/proxy_dst.nft 存在'
}

main() {
  log "开始执行 OpenWrt smoke，日志文件: ${LOG_FILE}"
  prepare_tools

  ensure_vm_running_and_ready
  ensure_canonical_deploy
  restart_and_assert_service
  prepare_required_sets

  assert_status_api
  assert_add_remove_semantics
  assert_sync_and_file

  log 'smoke 全部断言通过（未执行 /api/refresh-route，保持非阻塞）'
}

main "$@"
