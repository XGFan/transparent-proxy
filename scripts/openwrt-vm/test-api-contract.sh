#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

CURL_BIN="${CURL_BIN:-curl}"
SSH_BIN="${SSH_BIN:-ssh}"
SCP_BIN="${SCP_BIN:-scp}"
PYTHON_BIN="${PYTHON_BIN:-python3}"

API_BASE_URL="${TP_API_BASE_URL}"
SUITE="${TP_TEST_SUITE_TIER:-blocking}"
GROUP=""

TEST_SET="proxy_dst"
TEST_IP="${API_CONTRACT_TEST_IP:-198.18.0.123}"
UNKNOWN_SET="unknown_contract_set"

REQUIRED_SETS=(
  direct_src
  direct_dst
  proxy_src
  proxy_dst
)

BLOCKING_GROUPS=(
  api-status
  api-ip
  api-add
  api-remove
  api-sync
  refresh-route
  invalid-json
  missing-ip
  missing-set
  invalid-ip
  unknown-set
)

REGRESSION_GROUPS=(
  duplicate-add-remove
  refresh-route-invalid-fixture
  refresh-route-missing-fixture
  sync-missing-set
)

LOG_FILE="${OPENWRT_VM_LOG_DIR}/api-contract-$(date +%Y%m%d-%H%M%S).log"
mkdir -p "${OPENWRT_VM_LOG_DIR}" "${OPENWRT_VM_RUN_DIR}"
exec > >(tee -a "${LOG_FILE}") 2>&1

declare -a TMP_FILES=()
SSH_BASE=()
SCP_BASE=()
API_LAST_CODE=""
API_LAST_BODY_FILE=""
TRANSPORT_READY=0

HOST_VALID_FIXTURE="${TP_CHNROUTE_FIXTURE_PATH:-${TP_REFRESH_ROUTE_FIXTURE}}"
HOST_INVALID_FIXTURE="${TP_CHNROUTE_INVALID_FIXTURE:-${SCRIPT_DIR}/fixtures/chnroute-invalid.txt}"
GUEST_FIXTURE_VALID="/tmp/tp-chnroute-valid.txt"
GUEST_FIXTURE_INVALID="/tmp/tp-chnroute-invalid.txt"
GUEST_FIXTURE_MISSING="/tmp/tp-chnroute-missing.txt"
GUEST_SERVER_PID_FILE="/tmp/transparent-proxy-api-contract.pid"
GUEST_SERVER_LOG_FILE="/tmp/transparent-proxy-api-contract.log"
CURRENT_FIXTURE_MODE=""

log() {
  printf '[api-contract] %s\n' "$*"
}

fail() {
  printf '[api-contract][ERROR] %s\n' "$*" >&2
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

usage() {
  cat <<'EOF'
Usage: bash scripts/openwrt-vm/test-api-contract.sh [--suite=blocking|regression] [--group=<group-name>]

Suites:
  blocking    API happy path + core negative contract checks
  regression  idempotence + fixture error paths + cleanup semantics

Examples:
  bash scripts/openwrt-vm/test-api-contract.sh --suite=blocking
  bash scripts/openwrt-vm/test-api-contract.sh --suite=blocking --group=api-ip
  bash scripts/openwrt-vm/test-api-contract.sh --suite=regression --group=sync-missing-set
EOF
}

cleanup() {
  local exit_code=$?
  local f

  set +e

  if (( TRANSPORT_READY == 1 )); then
    ensure_test_ip_absent >/dev/null 2>&1 || true

    run_ssh_script "${GUEST_SERVER_PID_FILE}" <<'EOF' >/dev/null 2>&1
set -eu
pid_file="$1"
if [ -f "$pid_file" ]; then
  pid="$(cat "$pid_file" 2>/dev/null || true)"
  if [ -n "$pid" ]; then
    kill "$pid" >/dev/null 2>&1 || true
  fi
  rm -f "$pid_file"
fi

pids="$(pgrep -f '/etc/transparent-proxy/server -c /etc/transparent-proxy/config.yaml' || true)"
for p in $pids; do
  kill "$p" >/dev/null 2>&1 || true
done

rm -f /tmp/tp-chnroute-valid.txt /tmp/tp-chnroute-invalid.txt /tmp/tp-chnroute-missing.txt
EOF

    run_ssh '/etc/init.d/transparent-proxy restart || /etc/init.d/transparent-proxy start || true' >/dev/null 2>&1 || true
  fi

  if (( exit_code != 0 )); then
    log '检测到失败，开始收集 VM artifacts'
    bash "${SCRIPT_DIR}/collect-artifacts.sh" >/dev/null 2>&1 || true
  fi

  for f in "${TMP_FILES[@]}"; do
    [[ -n "${f}" ]] && rm -f "${f}" || true
  done

  trap - EXIT
  exit "${exit_code}"
}
trap cleanup EXIT

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --suite=*)
        SUITE="${1#*=}"
        ;;
      --group=*)
        GROUP="${1#*=}"
        ;;
      --group)
        [[ $# -ge 2 ]] || fail "--group 需要参数"
        GROUP="$2"
        shift
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

  case "${SUITE}" in
    blocking|regression) ;;
    *) fail "--suite 仅支持 blocking 或 regression，当前: ${SUITE}" ;;
  esac
}

prepare_tools() {
  CURL_BIN="$(resolve_command "${CURL_BIN}")" || fail "缺少 curl 可执行文件: ${CURL_BIN}"
  SSH_BIN="$(resolve_command "${SSH_BIN}")" || fail "缺少 ssh 可执行文件: ${SSH_BIN}"
  SCP_BIN="$(resolve_command "${SCP_BIN}")" || fail "缺少 scp 可执行文件: ${SCP_BIN}"
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

  SCP_BASE=(
    "${SCP_BIN}"
    -O
    -o StrictHostKeyChecking=no
    -o UserKnownHostsFile=/dev/null
    -o ConnectTimeout=5
    -o BatchMode=yes
    -i "${OPENWRT_TEST_KEY_PATH}"
    -P "${SSH_PORT}"
  )

  TRANSPORT_READY=1
}

run_ssh() {
  "${SSH_BASE[@]}" "$@"
}

run_ssh_script() {
  "${SSH_BASE[@]}" sh -s -- "$@"
}

run_scp() {
  "${SCP_BASE[@]}" "$@"
}

register_tmp_file() {
  TMP_FILES+=("$1")
}

call_api() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  local content_type="${4:-application/json}"
  local body_file

  body_file="$(mktemp "${OPENWRT_VM_RUN_DIR}/api-contract.${method}.${path//\//_}.XXXXXX")"
  register_tmp_file "${body_file}"

  if [[ -n "${body}" ]]; then
    API_LAST_CODE="$("${CURL_BIN}" -sS -o "${body_file}" -w '%{http_code}' -X "${method}" -H "Content-Type: ${content_type}" --data "${body}" --connect-timeout 5 --max-time 30 "${API_BASE_URL}${path}")"
  else
    API_LAST_CODE="$("${CURL_BIN}" -sS -o "${body_file}" -w '%{http_code}' -X "${method}" --connect-timeout 5 --max-time 30 "${API_BASE_URL}${path}")"
  fi

  API_LAST_BODY_FILE="${body_file}"
}

assert_api_code() {
  local expected="$1"
  [[ "${API_LAST_CODE}" == "${expected}" ]] || fail "HTTP ${API_LAST_CODE}（预期 ${expected}），响应: $(<"${API_LAST_BODY_FILE}")"
}

assert_api_ok_envelope() {
  "${PYTHON_BIN}" - "${API_LAST_BODY_FILE}" <<'PY'
import json
import sys

with open(sys.argv[1], 'r', encoding='utf-8') as f:
    data = json.load(f)

if not isinstance(data, dict) or data.get('code') != 'ok' or data.get('message') != 'ok' or not isinstance(data.get('data'), dict):
    print(f"unexpected body: {data}", file=sys.stderr)
    raise SystemExit(1)
PY
}

assert_api_legacy_message_ok() {
  "${PYTHON_BIN}" - "${API_LAST_BODY_FILE}" <<'PY'
import json
import sys

with open(sys.argv[1], 'r', encoding='utf-8') as f:
    data = json.load(f)

if not isinstance(data, dict) or data.get('message') != 'ok':
    print(f"unexpected legacy body: {data}", file=sys.stderr)
    raise SystemExit(1)
PY
}

assert_api_error_contains() {
  local expected_substring="$1"
  local expected_code="${2:-}"
  "${PYTHON_BIN}" - "${API_LAST_BODY_FILE}" "${expected_substring}" "${expected_code}" <<'PY'
import json
import sys

with open(sys.argv[1], 'r', encoding='utf-8') as f:
    data = json.load(f)

if not isinstance(data, dict) or not isinstance(data.get('data'), dict) or 'error' not in data['data']:
    print(f"missing envelope error field: {data}", file=sys.stderr)
    raise SystemExit(1)

expected = sys.argv[2]
actual = str(data['data']['error'])
if expected not in actual:
    print(f"error mismatch: expected substring={expected}, actual={actual}", file=sys.stderr)
    raise SystemExit(1)

expected_code = sys.argv[3]
if expected_code and data.get('code') != expected_code:
    print(f"code mismatch: expected={expected_code}, actual={data.get('code')}", file=sys.stderr)
    raise SystemExit(1)
PY
}

assert_api_legacy_error_contains() {
  local expected_substring="$1"
  "${PYTHON_BIN}" - "${API_LAST_BODY_FILE}" "${expected_substring}" <<'PY'
import json
import sys

with open(sys.argv[1], 'r', encoding='utf-8') as f:
    data = json.load(f)

if not isinstance(data, dict) or 'error' not in data:
    print(f"missing legacy error field: {data}", file=sys.stderr)
    raise SystemExit(1)

expected = sys.argv[2]
actual = str(data['error'])
if expected not in actual:
    print(f"error mismatch: expected substring={expected}, actual={actual}", file=sys.stderr)
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

if data.get('set') != expected_set or data.get('ip') != expected_ip:
    print(f"set/ip mismatch: {data}", file=sys.stderr)
    raise SystemExit(1)

rule = data.get('rule')
if not isinstance(rule, dict) or rule.get('name') != expected_set:
    print(f"rule mismatch: {rule}", file=sys.stderr)
    raise SystemExit(1)

operation = data.get('operation')
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

assert_ip_string_non_empty() {
  "${PYTHON_BIN}" - "${API_LAST_BODY_FILE}" <<'PY'
import json
import ipaddress
import sys

with open(sys.argv[1], 'r', encoding='utf-8') as f:
    value = json.load(f)

if not isinstance(value, str) or not value.strip():
    print(f"/api/ip should return non-empty string, got: {value!r}", file=sys.stderr)
    raise SystemExit(1)

try:
    ipaddress.ip_address(value.strip())
except Exception as exc:
    print(f"/api/ip returned non-IP value: {value!r}, err={exc}", file=sys.stderr)
    raise SystemExit(1)
PY
}

wait_api_ready() {
  local i
  for i in $(seq 1 30); do
    if call_api 'GET' '/api/ip' && [[ "${API_LAST_CODE}" == '200' ]]; then
      return 0
    fi
    sleep 1
  done

  run_ssh "test -f '${GUEST_SERVER_LOG_FILE}' && tail -n 80 '${GUEST_SERVER_LOG_FILE}' || true"
  fail '等待 API 服务就绪超时（/api/ip 未返回 200）'
}

ensure_vm_ready_and_deployed() {
  log '调用共享 helper: test-common.sh --ensure-vm-ready'
  bash "${SCRIPT_DIR}/test-common.sh" --ensure-vm-ready
}

ensure_fixture_file() {
  local file_path="$1"
  [[ -f "${file_path}" ]] || fail "fixture 不存在: ${file_path}"
}

sync_fixtures_to_guest() {
  ensure_fixture_file "${HOST_VALID_FIXTURE}"
  ensure_fixture_file "${HOST_INVALID_FIXTURE}"

  run_scp "${HOST_VALID_FIXTURE}" "root@${QEMU_HOST}:${GUEST_FIXTURE_VALID}"
  run_scp "${HOST_INVALID_FIXTURE}" "root@${QEMU_HOST}:${GUEST_FIXTURE_INVALID}"

  run_ssh "rm -f '${GUEST_FIXTURE_MISSING}'"
}

restart_guest_server_with_fixture() {
  local fixture_path="$1"

  run_ssh_script "${fixture_path}" "${GUEST_SERVER_PID_FILE}" "${GUEST_SERVER_LOG_FILE}" <<'EOF'
set -eu

fixture="$1"
pid_file="$2"
log_file="$3"
cmd='/etc/transparent-proxy/server -c /etc/transparent-proxy/config.yaml'

/etc/init.d/transparent-proxy stop >/dev/null 2>&1 || true

if [ -f "$pid_file" ]; then
  old_pid="$(cat "$pid_file" 2>/dev/null || true)"
  if [ -n "$old_pid" ]; then
    kill "$old_pid" >/dev/null 2>&1 || true
  fi
  rm -f "$pid_file"
fi

pids="$(pgrep -f "$cmd" || true)"
for p in $pids; do
  kill "$p" >/dev/null 2>&1 || true
done

sleep 1
pids="$(pgrep -f "$cmd" || true)"
for p in $pids; do
  kill -9 "$p" >/dev/null 2>&1 || true
done

: > "$log_file"
if [ -n "$fixture" ]; then
  env TP_CHNROUTE_FIXTURE_PATH="$fixture" $cmd >>"$log_file" 2>&1 &
else
  $cmd >>"$log_file" 2>&1 &
fi
echo "$!" > "$pid_file"
EOF

  wait_api_ready
}

set_fixture_mode() {
  local mode="$1"
  local fixture_path=""

  if [[ "${CURRENT_FIXTURE_MODE}" == "${mode}" ]]; then
    return 0
  fi

  case "${mode}" in
    valid)
      fixture_path="${GUEST_FIXTURE_VALID}"
      ;;
    invalid)
      fixture_path="${GUEST_FIXTURE_INVALID}"
      ;;
    missing)
      fixture_path="${GUEST_FIXTURE_MISSING}"
      ;;
    none)
      fixture_path=""
      ;;
    *)
      fail "未知 fixture mode: ${mode}"
      ;;
  esac

  log "重启 guest 服务并切换 fixture mode: ${mode}"
  restart_guest_server_with_fixture "${fixture_path}"
  CURRENT_FIXTURE_MODE="${mode}"
}

prepare_required_sets() {
  run_ssh sh -s -- "${REQUIRED_SETS[@]}" <<'EOF'
set -eu

nft list table inet fw4 >/dev/null 2>&1

ensure_set() {
  set_name="$1"
  if nft list set inet fw4 "$set_name" >/dev/null 2>&1; then
    return 0
  fi
  nft "add set inet fw4 $set_name { type ipv4_addr; flags interval; auto-merge; }"
  nft list set inet fw4 "$set_name" >/dev/null 2>&1
}

for s in "$@"; do
  ensure_set "$s"
done
EOF
}

delete_set_if_exists() {
  local set_name="$1"
  run_ssh "nft delete set inet fw4 ${set_name} >/dev/null 2>&1 || true"
}

guest_set_contains_ip() {
  local set_name="$1"
  local ip="$2"
  run_ssh "nft list set inet fw4 ${set_name} | grep -F -- '${ip}' >/dev/null"
}

ensure_test_ip_absent() {
  run_ssh "nft delete element inet fw4 ${TEST_SET} { ${TEST_IP} } >/dev/null 2>&1 || true"
}

assert_test_ip_absent() {
  if guest_set_contains_ip "${TEST_SET}" "${TEST_IP}"; then
    fail "guest 脏状态：${TEST_SET} 仍包含 ${TEST_IP}"
  fi
}

capture_set_snapshot() {
  local set_name="$1"
  local output_file="$2"
  run_ssh "nft list set inet fw4 ${set_name}" >"${output_file}"
}

assert_refresh_route_file_contains_fixture_routes() {
  run_ssh_script <<'EOF'
set -eu

test -f /etc/nftables.d/chnroute.nft
grep -F -- '1.0.1.0/24' /etc/nftables.d/chnroute.nft >/dev/null
grep -F -- '1.0.2.0/23' /etc/nftables.d/chnroute.nft >/dev/null
EOF
}

assert_sync_file_has_set_content() {
  run_ssh_script "${TEST_SET}" <<'EOF'
set -eu

set_name="$1"
target="/etc/nftables.d/${set_name}.nft"
test -f "$target"
grep -F -- "set ${set_name}" "$target" >/dev/null
EOF
}

blocking_api_status() {
  log 'group=api-status'
  call_api 'GET' '/api/status'
  assert_api_code '200'
  assert_api_ok_envelope
  "${PYTHON_BIN}" - "${API_LAST_BODY_FILE}" <<'PY'
import json
import sys

required = {'direct_src', 'direct_dst', 'proxy_src', 'proxy_dst'}

with open(sys.argv[1], 'r', encoding='utf-8') as f:
    envelope = json.load(f)

if envelope.get('code') != 'ok' or envelope.get('message') != 'ok':
    print(f'unexpected envelope: {envelope}', file=sys.stderr)
    raise SystemExit(1)

data = envelope.get('data')
if not isinstance(data, dict):
    print(f'/api/status data should be object: {data}', file=sys.stderr)
    raise SystemExit(1)

if 'status' not in data or 'ip' not in data or 'sets' not in data:
    print(f'missing keys in /api/status envelope data: {data}', file=sys.stderr)
    raise SystemExit(1)

sets = {item.get('name') for item in data.get('sets', []) if isinstance(item, dict)}
missing = sorted(required - sets)
if missing:
    print(f'missing sets: {missing}', file=sys.stderr)
    raise SystemExit(1)
PY
}

blocking_api_ip() {
  log 'group=api-ip'
  call_api 'GET' '/api/ip'
  assert_api_code '200'
  assert_ip_string_non_empty
}

blocking_api_add() {
  local payload
  payload="{\"ip\":\"${TEST_IP}\",\"set\":\"${TEST_SET}\"}"
  log 'group=api-add'

  ensure_test_ip_absent
  assert_test_ip_absent

  call_api 'POST' '/api/rules/add' "${payload}"
  assert_api_code '200'
  assert_rules_add_remove_response 'add' "${TEST_SET}" "${TEST_IP}"

  guest_set_contains_ip "${TEST_SET}" "${TEST_IP}" || fail '/api/rules/add 后 guest 未出现目标 IP'

  ensure_test_ip_absent
  assert_test_ip_absent
}

blocking_api_remove() {
  local payload
  payload="{\"ip\":\"${TEST_IP}\",\"set\":\"${TEST_SET}\"}"
  log 'group=api-remove'

  ensure_test_ip_absent
  call_api 'POST' '/api/rules/add' "${payload}"
  assert_api_code '200'
  assert_rules_add_remove_response 'add' "${TEST_SET}" "${TEST_IP}"
  guest_set_contains_ip "${TEST_SET}" "${TEST_IP}" || fail 'remove 前准备失败：add 后未出现 IP'

  call_api 'POST' '/api/rules/remove' "${payload}"
  assert_api_code '200'
  assert_rules_add_remove_response 'remove' "${TEST_SET}" "${TEST_IP}"
  assert_test_ip_absent
}

blocking_api_sync() {
  local payload
  payload="{\"ip\":\"${TEST_IP}\",\"set\":\"${TEST_SET}\"}"
  log 'group=api-sync'

  ensure_test_ip_absent
  call_api 'POST' '/api/rules/add' "${payload}"
  assert_api_code '200'
  assert_rules_add_remove_response 'add' "${TEST_SET}" "${TEST_IP}"
  guest_set_contains_ip "${TEST_SET}" "${TEST_IP}" || fail 'sync 前准备失败：add 后未出现 IP'

  call_api 'POST' '/api/rules/sync'
  assert_api_code '200'
  assert_rules_sync_response
  assert_sync_file_has_set_content

  ensure_test_ip_absent
  call_api 'POST' '/api/rules/sync'
  assert_api_code '200'
  assert_rules_sync_response
}

blocking_refresh_route() {
  log 'group=refresh-route'
  set_fixture_mode valid

  call_api 'POST' '/api/refresh-route'
  assert_api_code '200'
  assert_api_legacy_message_ok
  assert_refresh_route_file_contains_fixture_routes
}

blocking_invalid_json() {
  local before_snapshot after_snapshot payload
  log 'group=invalid-json'

  before_snapshot="$(mktemp "${OPENWRT_VM_RUN_DIR}/api-contract.invalid-json.before.XXXXXX")"
  after_snapshot="$(mktemp "${OPENWRT_VM_RUN_DIR}/api-contract.invalid-json.after.XXXXXX")"
  register_tmp_file "${before_snapshot}"
  register_tmp_file "${after_snapshot}"

  ensure_test_ip_absent
  capture_set_snapshot "${TEST_SET}" "${before_snapshot}"

  payload='{"ip":"198.18.0.123","set":"proxy_dst"'
  call_api 'POST' '/api/rules/add' "${payload}" 'application/json'
  assert_api_code '400'
  assert_api_error_contains 'unexpected EOF' 'invalid_request'

  capture_set_snapshot "${TEST_SET}" "${after_snapshot}"
  cmp -s "${before_snapshot}" "${after_snapshot}" || fail 'invalid-json 后 guest set 快照发生变化'
  assert_test_ip_absent
}

blocking_missing_ip() {
  log 'group=missing-ip'

  ensure_test_ip_absent
  call_api 'POST' '/api/rules/add' '{"set":"proxy_dst"}'
  assert_api_code '400'
  assert_api_error_contains 'required' 'invalid_request'
  assert_test_ip_absent
}

blocking_missing_set() {
  log 'group=missing-set'

  ensure_test_ip_absent
  call_api 'POST' '/api/rules/add' "{\"ip\":\"${TEST_IP}\"}"
  assert_api_code '400'
  assert_api_error_contains 'required' 'invalid_request'
  assert_test_ip_absent
}

blocking_invalid_ip() {
  log 'group=invalid-ip'

  ensure_test_ip_absent
  call_api 'POST' '/api/rules/add' '{"ip":"999.1.1.1","set":"proxy_dst"}'
  assert_api_code '400'
  assert_api_error_contains 'invalid ip' 'invalid_request'
  assert_test_ip_absent
}

blocking_unknown_set() {
  local payload
  payload="{\"ip\":\"${TEST_IP}\",\"set\":\"${UNKNOWN_SET}\"}"
  log 'group=unknown-set'

  ensure_test_ip_absent
  call_api 'POST' '/api/rules/add' "${payload}"
  assert_api_code '400'
  assert_api_error_contains 'not managed' 'invalid_request'
  assert_test_ip_absent
}

regression_duplicate_add_remove() {
  local payload
  payload="{\"ip\":\"${TEST_IP}\",\"set\":\"${TEST_SET}\"}"
  log 'group=duplicate-add-remove'

  ensure_test_ip_absent

  call_api 'POST' '/api/rules/add' "${payload}"
  assert_api_code '200'
  assert_rules_add_remove_response 'add' "${TEST_SET}" "${TEST_IP}"
  call_api 'POST' '/api/rules/add' "${payload}"
  assert_api_code '200'
  assert_rules_add_remove_response 'add' "${TEST_SET}" "${TEST_IP}"
  guest_set_contains_ip "${TEST_SET}" "${TEST_IP}" || fail 'duplicate add 后 guest 缺少目标 IP'

  call_api 'POST' '/api/rules/remove' "${payload}"
  assert_api_code '200'
  assert_rules_add_remove_response 'remove' "${TEST_SET}" "${TEST_IP}"
  call_api 'POST' '/api/rules/remove' "${payload}"
  assert_api_code '500'
  assert_api_error_contains 'element does not exist' 'internal_error'
  assert_test_ip_absent
}

regression_refresh_route_invalid_fixture() {
  log 'group=refresh-route-invalid-fixture'
  set_fixture_mode invalid

  call_api 'POST' '/api/refresh-route'
  assert_api_code '500'
  assert_api_legacy_error_contains 'parse line'
}

regression_refresh_route_missing_fixture() {
  log 'group=refresh-route-missing-fixture'
  set_fixture_mode missing

  call_api 'POST' '/api/refresh-route'
  assert_api_code '500'
  assert_api_legacy_error_contains 'read chnroute fixture fail'
}

regression_sync_missing_set() {
  local missing_set='direct_src'
  log 'group=sync-missing-set'

  prepare_required_sets
  delete_set_if_exists "${missing_set}"

  call_api 'POST' '/api/rules/sync'
  assert_api_code '500'
  assert_api_error_contains "${missing_set}" 'internal_error'

  prepare_required_sets
  call_api 'POST' '/api/rules/sync'
  assert_api_code '200'
  assert_rules_sync_response
}

run_blocking_group() {
  case "$1" in
    api-status) blocking_api_status ;;
    api-ip) blocking_api_ip ;;
    api-add) blocking_api_add ;;
    api-remove) blocking_api_remove ;;
    api-sync) blocking_api_sync ;;
    refresh-route) blocking_refresh_route ;;
    invalid-json) blocking_invalid_json ;;
    missing-ip) blocking_missing_ip ;;
    missing-set) blocking_missing_set ;;
    invalid-ip) blocking_invalid_ip ;;
    unknown-set) blocking_unknown_set ;;
    *) fail "blocking suite 不支持 group: $1" ;;
  esac
}

run_regression_group() {
  case "$1" in
    duplicate-add-remove) regression_duplicate_add_remove ;;
    refresh-route-invalid-fixture|refresh-route-error) regression_refresh_route_invalid_fixture ;;
    refresh-route-missing-fixture) regression_refresh_route_missing_fixture ;;
    sync-missing-set) regression_sync_missing_set ;;
    *) fail "regression suite 不支持 group: $1" ;;
  esac
}

run_groups_for_suite() {
  local selected_group="$1"
  local group_name

  if [[ "${SUITE}" == 'blocking' ]]; then
    if [[ -n "${selected_group}" ]]; then
      run_blocking_group "${selected_group}"
    else
      for group_name in "${BLOCKING_GROUPS[@]}"; do
        run_blocking_group "${group_name}"
      done
    fi
    return
  fi

  if [[ -n "${selected_group}" ]]; then
    run_regression_group "${selected_group}"
  else
    for group_name in "${REGRESSION_GROUPS[@]}"; do
      run_regression_group "${group_name}"
    done
  fi
}

main() {
  parse_args "$@"
  log "开始执行 suite=${SUITE} group=${GROUP:-<all>}，日志文件: ${LOG_FILE}"

  prepare_tools
  ensure_vm_ready_and_deployed
  sync_fixtures_to_guest
  prepare_required_sets
  ensure_test_ip_absent

  set_fixture_mode valid

  run_groups_for_suite "${GROUP}"

  prepare_required_sets
  ensure_test_ip_absent
  set_fixture_mode valid

  log "suite=${SUITE} group=${GROUP:-<all>} 全部断言通过"
}

main "$@"
