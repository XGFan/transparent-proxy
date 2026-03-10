#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

CURL_BIN="${CURL_BIN:-curl}"
SSH_BIN="${SSH_BIN:-ssh}"
SCP_BIN="${SCP_BIN:-scp}"
PYTHON_BIN="${PYTHON_BIN:-python3}"

VERSION="v0.0.0-test"
KEEP_DIST_OUTPUT="${KEEP_DIST_OUTPUT:-0}"

SERVICE_IPK=""
LUCI_IPK=""
BUILD_OUTPUT_ROOT=""
GUEST_STAGE=""
TMP_DIR=""
TUNNEL_PID=""
LUCI_TUNNEL_PORT=""

EVIDENCE_DIR="${OPENWRT_VM_REPO_ROOT}/.sisyphus/evidence"
TEXT_EVIDENCE_FILE="${EVIDENCE_DIR}/task-10-luci-entry.txt"
ROUTE_PATH="/cgi-bin/luci/admin/services/transparent-proxy"
FALLBACK_ENV_FILE="${OPENWRT_VM_WORK_DIR}/luci-probe.env"
FALLBACK_NOTICE='当前镜像未启用同源承载，已降级为独立管理页'
FALLBACK_TARGET='//${location.hostname}:1444/'

SSH_BASE=()
SCP_BASE=()

log() {
  printf '[test-luci-entry] %s\n' "$*" >&2
}

fail() {
  printf '[test-luci-entry][ERROR] %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: bash scripts/openwrt-vm/test-luci-entry.sh [--version <vX.Y.Z>]

Options:
  --version VERSION   Package version to build (default: v0.0.0-test)
  -h, --help          Show this help
EOF
}

cleanup() {
  local exit_code=$?

  if [[ -n "${TUNNEL_PID}" ]] && kill -0 "${TUNNEL_PID}" >/dev/null 2>&1; then
    kill "${TUNNEL_PID}" >/dev/null 2>&1 || true
    wait "${TUNNEL_PID}" >/dev/null 2>&1 || true
  fi

  if [[ -n "${TMP_DIR}" && -d "${TMP_DIR}" ]]; then
    rm -rf "${TMP_DIR}"
  fi

  if [[ "${KEEP_DIST_OUTPUT}" != "1" && -n "${BUILD_OUTPUT_ROOT}" && -d "${BUILD_OUTPUT_ROOT}" ]]; then
    rm -rf "${BUILD_OUTPUT_ROOT}"
  fi

  trap - EXIT
  exit "${exit_code}"
}
trap cleanup EXIT

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --version)
        [[ $# -ge 2 ]] || fail '--version 缺少值'
        VERSION="$2"
        shift 2
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
  done
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
  SCP_BIN="$(resolve_command "${SCP_BIN}")" || fail "缺少 scp 可执行文件: ${SCP_BIN}"
  PYTHON_BIN="$(resolve_command "${PYTHON_BIN}")" || fail "缺少 python3 可执行文件: ${PYTHON_BIN}"
  [[ -f "${OPENWRT_TEST_KEY_PATH}" ]] || fail "测试 SSH 私钥不存在: ${OPENWRT_TEST_KEY_PATH}"

  mkdir -p "${OPENWRT_VM_RUN_DIR}" "${OPENWRT_VM_LOG_DIR}" "${EVIDENCE_DIR}"
  TMP_DIR="$(mktemp -d "${OPENWRT_VM_RUN_DIR}/task10-luci-entry.XXXXXX")"

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
}

run_ssh() {
  "${SSH_BASE[@]}" "$@"
}

run_scp() {
  "${SCP_BASE[@]}" "$@"
}

assert_fallback_branch_locked() {
  [[ -f "${FALLBACK_ENV_FILE}" ]] || fail "缺少 fallback 真值文件: ${FALLBACK_ENV_FILE}"
  source "${FALLBACK_ENV_FILE}"
  [[ "${SAME_ORIGIN_SUPPORTED:-}" == '0' ]] || fail "当前分支并非 fallback-only：SAME_ORIGIN_SUPPORTED=${SAME_ORIGIN_SUPPORTED:-<empty>}"
}

build_ipks() {
  BUILD_OUTPUT_ROOT="${OPENWRT_VM_REPO_ROOT}/dist/ipk/${VERSION}"
  rm -rf "${BUILD_OUTPUT_ROOT}"

  log "构建 service + LuCI ipk（version=${VERSION}）"
  bash "${OPENWRT_VM_REPO_ROOT}/scripts/build-openwrt-ipk.sh" \
    --version "${VERSION}" \
    --package all \
    --skip-frontend

  shopt -s nullglob
  local service_matches=("${BUILD_OUTPUT_ROOT}/artifacts/transparent-proxy_"*.ipk)
  local luci_matches=("${BUILD_OUTPUT_ROOT}/artifacts/luci-app-transparent-proxy_"*.ipk)
  shopt -u nullglob

  [[ ${#service_matches[@]} -eq 1 ]] || fail "service ipk 数量异常，期望 1 个，实际 ${#service_matches[@]}"
  [[ ${#luci_matches[@]} -eq 1 ]] || fail "luci ipk 数量异常，期望 1 个，实际 ${#luci_matches[@]}"

  SERVICE_IPK="${service_matches[0]}"
  LUCI_IPK="${luci_matches[0]}"
}

wait_for_api_ready() {
  local url="${TP_API_BASE_URL}/api/status"
  local body_file="${TMP_DIR}/api-status.body"
  local status=''
  local attempt

  for attempt in $(seq 1 30); do
    status="$(${CURL_BIN} -sS -o "${body_file}" -w '%{http_code}' --connect-timeout 2 --max-time 5 "${url}" || true)"
    if [[ "${status}" == '200' ]]; then
      printf '%s\n' "${status}"
      return 0
    fi
    sleep 1
  done

  fail "service health check 失败: ${url} 最终状态 ${status:-<empty>}"
}

start_luci_tunnel() {
  LUCI_TUNNEL_PORT="$(${PYTHON_BIN} - <<'PY'
import socket

sock = socket.socket()
sock.bind(('127.0.0.1', 0))
print(sock.getsockname()[1])
sock.close()
PY
)"

  "${SSH_BASE[@]}" -N -L "127.0.0.1:${LUCI_TUNNEL_PORT}:127.0.0.1:80" >/dev/null 2>&1 &
  TUNNEL_PID="$!"

  local tunnel_url="http://127.0.0.1:${LUCI_TUNNEL_PORT}/cgi-bin/luci"
  local attempt
  for attempt in $(seq 1 20); do
    if ! kill -0 "${TUNNEL_PID}" >/dev/null 2>&1; then
      break
    fi
    if "${CURL_BIN}" -sS -o /dev/null --connect-timeout 1 --max-time 2 "${tunnel_url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  fail "无法建立 LuCI tunnel: ${tunnel_url}"
}

http_probe() {
  local url="$1"
  local body_file="$2"
  local header_file="$3"
  shift 3
  local code

  set +e
  code="$(${CURL_BIN} -sS -o "${body_file}" -D "${header_file}" -w '%{http_code}' --connect-timeout 5 --max-time 20 "$@" "${url}")"
  local curl_rc=$?
  set -e

  if (( curl_rc != 0 )); then
    printf 'curl-exit-%s' "${curl_rc}"
    return 0
  fi

  printf '%s' "${code}"
}

extract_header_value() {
  local header_file="$1"
  local header_name="$2"

  "${PYTHON_BIN}" - "${header_file}" "${header_name}" <<'PY'
import pathlib
import sys

header_path = pathlib.Path(sys.argv[1])
header_name = sys.argv[2].lower().strip()
value = ''

for line in header_path.read_text(encoding='utf-8', errors='ignore').splitlines():
    lower = line.lower()
    if lower.startswith(header_name + ':'):
        value = line.split(':', 1)[1].strip()

print(value)
PY
}

extract_auth_cookie() {
  local header_file="$1"

  "${PYTHON_BIN}" - "${header_file}" <<'PY'
import pathlib
import sys

header_path = pathlib.Path(sys.argv[1])
candidate = ''

for line in header_path.read_text(encoding='utf-8', errors='ignore').splitlines():
    if not line.lower().startswith('set-cookie:'):
        continue
    cookie = line.split(':', 1)[1].strip().split(';', 1)[0].strip()
    if cookie.startswith('sysauth_http='):
        print(cookie)
        raise SystemExit(0)
    if cookie.startswith('sysauth=') and not candidate:
        candidate = cookie

print(candidate)
PY
}

install_luci_packages() {
  GUEST_STAGE="/tmp/task10-luci-entry"
  run_ssh "rm -rf '${GUEST_STAGE}' && mkdir -p '${GUEST_STAGE}'"
  run_scp "${SERVICE_IPK}" "root@${QEMU_HOST}:${GUEST_STAGE}/service.ipk"
  run_scp "${LUCI_IPK}" "root@${QEMU_HOST}:${GUEST_STAGE}/luci.ipk"

  run_ssh sh -s -- "${GUEST_STAGE}" "${OPENWRT_GUEST_SERVER}" "${OPENWRT_GUEST_CONFIG}" "${OPENWRT_GUEST_INITD}" > "${TEXT_EVIDENCE_FILE}" <<'EOF'
set -eu

guest_stage="$1"
guest_server="$2"
guest_config="$3"
guest_initd="$4"
expected_service_command="/etc/transparent-proxy/server -c /etc/transparent-proxy/config.yaml"

remove_package() {
  package_name="$1"
  opkg remove "${package_name}" >/dev/null 2>&1 || true
  rm -f /usr/lib/opkg/info/${package_name}.*
}

reset_guest_layout() {
  if [ -x /etc/init.d/transparent-proxy ]; then
    /etc/init.d/transparent-proxy stop >/dev/null 2>&1 || true
    /etc/init.d/transparent-proxy disable >/dev/null 2>&1 || true
  fi

  killall server >/dev/null 2>&1 || true
  killall -9 server >/dev/null 2>&1 || true

  remove_package transparent-proxy
  remove_package luci-app-transparent-proxy

  if [ -f /usr/lib/opkg/status ]; then
    status_tmp="/usr/lib/opkg/status.task10.reset.$$"
    awk -v RS='' -v ORS='\n\n' '\
$0 !~ /^Package: transparent-proxy(\n|$)/ && \
$0 !~ /^Package: luci-app-transparent-proxy(\n|$)/\
' /usr/lib/opkg/status > "${status_tmp}"
    mv "${status_tmp}" /usr/lib/opkg/status
  fi

  for path in \
    /usr/share/luci/menu.d/transparent-proxy.json \
    /usr/share/rpcd/acl.d/luci-app-transparent-proxy.json \
    /www/luci-static/resources/view/transparent-proxy/index.js \
    /etc/init.d/transparent-proxy \
    /etc/hotplug.d/iface/80-ifup-wan \
    /etc/nftables.d/direct_dst.nft \
    /etc/nftables.d/direct_src.nft \
    /etc/nftables.d/proxy.nft \
    /etc/nftables.d/proxy_dst.nft \
    /etc/nftables.d/proxy_src.nft \
    /etc/nftables.d/reserved_ip.nft \
    /etc/nftables.d/v6block.nft \
    /etc/transparent-proxy/server \
    /etc/transparent-proxy/config.yaml \
    /etc/transparent-proxy/disable.sh \
    /etc/transparent-proxy/enable.sh \
    /etc/transparent-proxy/transparent.nft \
    /etc/transparent-proxy/transparent_full.nft \
    /etc/transparent-proxy/state/managed-records.json \
    /var/run/transparent-proxy.pid
  do
    rm -f "${path}"
  done

  rm -f /tmp/luci-indexcache.*
  rm -rf /tmp/luci-modulecache/
  rmdir /www/luci-static/resources/view/transparent-proxy >/dev/null 2>&1 || true
  rmdir /www/luci-static/resources/view >/dev/null 2>&1 || true
  rmdir /etc/transparent-proxy/state >/dev/null 2>&1 || true
  rmdir /etc/transparent-proxy >/dev/null 2>&1 || true
}

assert_install_result() {
  test -x /usr/bin/transparent-proxy
  test -x "${guest_server}"
  test -f "${guest_config}"
  test -f "${guest_initd}"
  grep -F -- "${expected_service_command}" "${guest_initd}" >/dev/null
  "${guest_initd}" enabled >/dev/null 2>&1
  "${guest_initd}" running >/dev/null 2>&1

  test -f /usr/share/luci/menu.d/transparent-proxy.json
  test -f /usr/share/rpcd/acl.d/luci-app-transparent-proxy.json
  test -f /www/luci-static/resources/view/transparent-proxy/index.js
}

reset_guest_layout
opkg install "${guest_stage}/service.ipk"
opkg install "${guest_stage}/luci.ipk"
assert_install_result
/etc/init.d/rpcd reload >/dev/null 2>&1 || true
/etc/init.d/uhttpd reload >/dev/null 2>&1 || true

printf '== install commands ==\n'
printf 'opkg install %s/service.ipk\n' "${guest_stage}"
printf 'opkg install %s/luci.ipk\n' "${guest_stage}"
printf '\n== opkg status transparent-proxy ==\n'
opkg status transparent-proxy
printf '\n== opkg status luci-app-transparent-proxy ==\n'
opkg status luci-app-transparent-proxy
printf '\n== luci files ==\n'
ls -l /usr/share/luci/menu.d/transparent-proxy.json /usr/share/rpcd/acl.d/luci-app-transparent-proxy.json /www/luci-static/resources/view/transparent-proxy/index.js
printf '\n== service files ==\n'
ls -l /usr/bin/transparent-proxy "${guest_server}" "${guest_config}" "${guest_initd}"
printf '\n== init command assertion ==\n'
grep -nF -- "${expected_service_command}" "${guest_initd}"
printf '\n== service lifecycle ==\n'
printf 'enabled=%s\n' "$("${guest_initd}" enabled >/dev/null 2>&1 && printf yes || printf no)"
printf 'running=%s\n' "$("${guest_initd}" running >/dev/null 2>&1 && printf yes || printf no)"
EOF
}

verify_route_and_fallback_ui() {
  local login_url="http://127.0.0.1:${LUCI_TUNNEL_PORT}/cgi-bin/luci"
  local route_url="http://127.0.0.1:${LUCI_TUNNEL_PORT}${ROUTE_PATH}"
  local js_url="http://127.0.0.1:${LUCI_TUNNEL_PORT}/luci-static/resources/view/transparent-proxy/index.js"
  local login_body="${TMP_DIR}/login.body"
  local login_header="${TMP_DIR}/login.headers"
  local route_body="${TMP_DIR}/route.body"
  local route_header="${TMP_DIR}/route.headers"
  local js_body="${TMP_DIR}/view.body"
  local js_header="${TMP_DIR}/view.headers"
  local login_status route_status js_status auth_cookie

  login_status="$(http_probe "${login_url}" "${login_body}" "${login_header}" \
    -X POST \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    --data-urlencode 'luci_username=root' \
    --data-urlencode 'luci_password=')"
  auth_cookie="$(extract_auth_cookie "${login_header}")"
  [[ "${login_status}" == '200' || "${login_status}" == '302' || "${login_status}" == '303' ]] || fail "LuCI 登录状态异常: ${login_status}"
  [[ -n "${auth_cookie}" ]] || fail 'LuCI 登录未返回 sysauth cookie'

  route_status="$(http_probe "${route_url}" "${route_body}" "${route_header}" -H "Cookie: ${auth_cookie}")"
  js_status="$(http_probe "${js_url}" "${js_body}" "${js_header}" -H "Cookie: ${auth_cookie}")"

  [[ "${route_status}" == '200' ]] || fail "LuCI route 状态异常: ${route_url} -> ${route_status}"
  [[ "${js_status}" == '200' ]] || fail "LuCI view JS 状态异常: ${js_url} -> ${js_status}"

  grep -F 'data-page="admin-services-transparent-proxy"' "${route_body}" >/dev/null || fail 'LuCI route HTML 缺少 data-page 标记'
  grep -F 'transparent-proxy\/index' "${route_body}" >/dev/null || fail 'LuCI route HTML 缺少 view path 标记'
  grep -F 'fallbackNotice' "${route_body}" >/dev/null || fail 'LuCI route HTML 缺少 fallbackNotice'
  grep -F "${FALLBACK_NOTICE}" "${route_body}" >/dev/null || fail 'LuCI route HTML 缺少 fallbackNotice 文案'
  grep -F 'fallbackTarget' "${route_body}" >/dev/null || fail 'LuCI route HTML 缺少 fallbackTarget'
  grep -F '${location.hostname}:1444' "${route_body}" >/dev/null || fail 'LuCI route HTML 缺少 fallback target 主机与端口标记'

  grep -F "${FALLBACK_NOTICE}" "${js_body}" >/dev/null || fail 'LuCI view JS 缺少 fallbackNotice 文案'
  grep -F 'data-testid' "${js_body}" >/dev/null || fail 'LuCI view JS 缺少 test id 标记'
  grep -F 'tp-fallback-link' "${js_body}" >/dev/null || fail 'LuCI view JS 缺少 fallback link test id'
  grep -F "var fallbackUrl = '//' + host + ':1444/';" "${js_body}" >/dev/null || fail 'LuCI view JS 缺少 fallbackUrl 构造逻辑'
  grep -F 'window.__TP_LUCI_SAME_ORIGIN_SUPPORTED__=0' "${js_body}" >/dev/null || fail 'LuCI view JS 未锁定 fallback 分支'
  grep -F "target: '_blank'" "${js_body}" >/dev/null || fail 'LuCI view JS 未暴露独立页面链接行为'

  {
    printf '\n== fallback branch lock ==\n'
    printf 'SAME_ORIGIN_SUPPORTED=0\n'
    printf '\n== service health ==\n'
    printf 'api-url=%s/api/status\n' "${TP_API_BASE_URL}"
    printf 'api-status=200\n'
    printf 'api-response=%s\n' "$(tr '\n' ' ' < "${TMP_DIR}/api-status.body")"
    printf '\n== luci login ==\n'
    printf 'login-url=%s\n' "${login_url}"
    printf 'login-status=%s\n' "${login_status}"
    printf 'login-location=%s\n' "$(extract_header_value "${login_header}" 'location')"
    printf '\n== luci route ==\n'
    printf 'route-url=%s\n' "${route_url}"
    printf 'route-status=%s\n' "${route_status}"
    printf 'route-marker=data-page="admin-services-transparent-proxy"\n'
    printf 'route-fallback-notice=%s\n' "${FALLBACK_NOTICE}"
    printf 'route-fallback-target=%s\n' "${FALLBACK_TARGET}"
    printf '\n== luci view js ==\n'
    printf 'js-url=%s\n' "${js_url}"
    printf 'js-status=%s\n' "${js_status}"
    printf 'js-marker=tp-fallback-link\n'
    printf 'js-same-origin-supported=0\n'
    printf 'js-link-behavior=_blank independent page\n'
    printf '\n== conclusion ==\n'
    printf 'fallback-route-ui=verified via authenticated route HTML + delivered LuCI view JS\n'
  } >> "${TEXT_EVIDENCE_FILE}"
}

main() {
  parse_args "$@"
  prepare_tools
  assert_fallback_branch_locked

  log '调用共享 readiness 真值入口'
  bash "${SCRIPT_DIR}/test-common.sh" --ensure-vm-ready

  build_ipks
  install_luci_packages
  wait_for_api_ready >/dev/null
  start_luci_tunnel
  verify_route_and_fallback_ui

  log "LuCI fallback 证据已写入: ${TEXT_EVIDENCE_FILE}"
}

main "$@"
