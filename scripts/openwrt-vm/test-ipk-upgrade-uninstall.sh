#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

CURL_BIN="${CURL_BIN:-curl}"
SSH_BIN="${SSH_BIN:-ssh}"
SCP_BIN="${SCP_BIN:-scp}"
NODE_BIN="${NODE_BIN:-node}"
PYTHON_BIN="${PYTHON_BIN:-python3}"

OLD_VERSION="v0.11.0"
NEW_VERSION="v0.11.1"
KEEP_DIST_OUTPUT="${KEEP_DIST_OUTPUT:-0}"

OLD_SERVICE_IPK=""
OLD_LUCI_IPK=""
NEW_SERVICE_IPK=""
NEW_LUCI_IPK=""
OLD_BUILD_OUTPUT_ROOT=""
NEW_BUILD_OUTPUT_ROOT=""
GUEST_STAGE=""
TMP_DIR=""
PROFILE_DIR=""
FEED_DIR=""
TUNNEL_PID=""
LUCI_TUNNEL_PORT=""

EVIDENCE_DIR="${OPENWRT_VM_REPO_ROOT}/.sisyphus/evidence"
UPGRADE_EVIDENCE_FILE="${EVIDENCE_DIR}/task-11-upgrade-cache.txt"
UNINSTALL_EVIDENCE_FILE="${EVIDENCE_DIR}/task-11-uninstall.txt"
UPGRADE_SCREENSHOT_FILE="${EVIDENCE_DIR}/task-11-upgrade-cache.png"
FALLBACK_ENV_FILE="${OPENWRT_VM_WORK_DIR}/luci-probe.env"
ROUTE_PATH="/cgi-bin/luci/admin/services/transparent-proxy"
VIEW_JS_PATH="/luci-static/resources/view/transparent-proxy/index.js"
FALLBACK_NOTICE='当前镜像未启用同源承载，已降级为独立管理页'
PLAYWRIGHT_HELPER="${OPENWRT_VM_REPO_ROOT}/portal/e2e/helpers/luci-cache-probe.mjs"

SSH_BASE=()
SCP_BASE=()
TMP_DIRS=()

log() {
  printf '[task11-ipk-upgrade-uninstall] %s\n' "$*" >&2
}

fail() {
  printf '[task11-ipk-upgrade-uninstall][ERROR] %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: bash scripts/openwrt-vm/test-ipk-upgrade-uninstall.sh [options]

Options:
  --old-version VERSION   Old package version to install first (default: v0.11.0)
  --new-version VERSION   New package version to upgrade to (default: v0.11.1)
  -h, --help              Show this help
EOF
}

cleanup() {
  local exit_code=$?
  local dir

  if [[ -n "${TUNNEL_PID}" ]] && kill -0 "${TUNNEL_PID}" >/dev/null 2>&1; then
    kill "${TUNNEL_PID}" >/dev/null 2>&1 || true
    wait "${TUNNEL_PID}" >/dev/null 2>&1 || true
  fi

  for dir in "${TMP_DIRS[@]}"; do
    [[ -n "${dir}" && -d "${dir}" ]] && rm -rf "${dir}" || true
  done

  if [[ -n "${TMP_DIR}" && -d "${TMP_DIR}" ]]; then
    rm -rf "${TMP_DIR}"
  fi

  if [[ -n "${PROFILE_DIR}" && -d "${PROFILE_DIR}" ]]; then
    rm -rf "${PROFILE_DIR}"
  fi

  if [[ "${KEEP_DIST_OUTPUT}" != '1' && -n "${OLD_BUILD_OUTPUT_ROOT}" && -d "${OLD_BUILD_OUTPUT_ROOT}" ]]; then
    rm -rf "${OLD_BUILD_OUTPUT_ROOT}"
  fi

  if [[ "${KEEP_DIST_OUTPUT}" != '1' && -n "${NEW_BUILD_OUTPUT_ROOT}" && -d "${NEW_BUILD_OUTPUT_ROOT}" ]]; then
    rm -rf "${NEW_BUILD_OUTPUT_ROOT}"
  fi

  trap - EXIT
  exit "${exit_code}"
}
trap cleanup EXIT

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --old-version)
        [[ $# -ge 2 ]] || fail '--old-version 缺少值'
        OLD_VERSION="$2"
        shift 2
        ;;
      --new-version)
        [[ $# -ge 2 ]] || fail '--new-version 缺少值'
        NEW_VERSION="$2"
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

  [[ "${OLD_VERSION}" != "${NEW_VERSION}" ]] || fail 'old/new version 不能相同'
}

resolve_abs_path() {
  local path="$1"
  if [[ "${path}" == /* ]]; then
    printf '%s\n' "${path}"
  else
    printf '%s\n' "${PWD}/${path}"
  fi
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
  NODE_BIN="$(resolve_command "${NODE_BIN}")" || fail "缺少 node 可执行文件: ${NODE_BIN}"
  PYTHON_BIN="$(resolve_command "${PYTHON_BIN}")" || fail "缺少 python3 可执行文件: ${PYTHON_BIN}"
  [[ -f "${OPENWRT_TEST_KEY_PATH}" ]] || fail "测试 SSH 私钥不存在: ${OPENWRT_TEST_KEY_PATH}"
  [[ -f "${PLAYWRIGHT_HELPER}" ]] || fail "缺少 Playwright helper: ${PLAYWRIGHT_HELPER}"

  mkdir -p "${OPENWRT_VM_RUN_DIR}" "${OPENWRT_VM_LOG_DIR}" "${EVIDENCE_DIR}"
  TMP_DIR="$(mktemp -d "${OPENWRT_VM_RUN_DIR}/task11-ipk-upgrade-uninstall.XXXXXX")"
  PROFILE_DIR="${TMP_DIR}/chromium-profile"

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

build_packages_for_version() {
  local version="$1"
  local output_var_prefix="$2"
  local output_root="${OPENWRT_VM_REPO_ROOT}/dist/ipk/${version}"
  local service_var="${output_var_prefix}_SERVICE_IPK"
  local luci_var="${output_var_prefix}_LUCI_IPK"
  local root_var="${output_var_prefix}_BUILD_OUTPUT_ROOT"

  rm -rf "${output_root}"
  log "构建 OpenWrt ipk（version=${version}）"
  bash "${OPENWRT_VM_REPO_ROOT}/scripts/build-openwrt-ipk.sh" \
    --version "${version}" \
    --package all \
    --skip-frontend

  shopt -s nullglob
  local service_matches=("${output_root}/artifacts/transparent-proxy_"*.ipk)
  local luci_matches=("${output_root}/artifacts/luci-app-transparent-proxy_"*.ipk)
  shopt -u nullglob

  [[ ${#service_matches[@]} -eq 1 ]] || fail "service ipk 数量异常（${version}），期望 1 个，实际 ${#service_matches[@]}"
  [[ ${#luci_matches[@]} -eq 1 ]] || fail "luci ipk 数量异常（${version}），期望 1 个，实际 ${#luci_matches[@]}"

  printf -v "${service_var}" '%s' "${service_matches[0]}"
  printf -v "${luci_var}" '%s' "${luci_matches[0]}"
  printf -v "${root_var}" '%s' "${output_root}"
}

build_feed() {
  local output_dir="$1"
  shift
  local ipk_path filename packages_path control_dir size checksum

  mkdir -p "${output_dir}"
  packages_path="${output_dir}/Packages"
  : > "${packages_path}"

  for ipk_path in "$@"; do
    filename="$(basename "${ipk_path}")"
    control_dir="$(mktemp -d "${output_dir}/control.XXXXXX")"
    TMP_DIRS+=("${control_dir}")

    cp "${ipk_path}" "${output_dir}/${filename}"
    "${PYTHON_BIN}" - "${ipk_path}" "${control_dir}/control.tar.gz" <<'PY'
import pathlib
import sys
import tarfile

ipk_path = pathlib.Path(sys.argv[1])
output_path = pathlib.Path(sys.argv[2])

with tarfile.open(ipk_path, 'r:gz') as outer:
    member = outer.extractfile('./control.tar.gz') or outer.extractfile('control.tar.gz')
    if member is None:
        raise SystemExit('control.tar.gz not found in ipk')
    output_path.write_bytes(member.read())
PY
    tar -xzf "${control_dir}/control.tar.gz" -C "${control_dir}"

    size="$(wc -c < "${ipk_path}" | tr -d ' ')"
    if command -v sha256sum >/dev/null 2>&1; then
      checksum="$(sha256sum "${ipk_path}" | awk '{print $1}')"
    else
      checksum="$(shasum -a 256 "${ipk_path}" | awk '{print $1}')"
    fi

    {
      cat "${control_dir}/control"
      printf 'Filename: %s\n' "${filename}"
      printf 'Size: %s\n' "${size}"
      printf 'SHA256sum: %s\n\n' "${checksum}"
    } >> "${packages_path}"
  done
}

stage_guest_assets() {
  GUEST_STAGE="/tmp/task11-ipk-upgrade-uninstall"
  FEED_DIR="${TMP_DIR}/feed"
  build_feed "${FEED_DIR}" "${NEW_SERVICE_IPK}" "${NEW_LUCI_IPK}"

  run_ssh "rm -rf '${GUEST_STAGE}' && mkdir -p '${GUEST_STAGE}/feed'"
  run_scp "${OLD_SERVICE_IPK}" "root@${QEMU_HOST}:${GUEST_STAGE}/old-service.ipk"
  run_scp "${OLD_LUCI_IPK}" "root@${QEMU_HOST}:${GUEST_STAGE}/old-luci.ipk"
  run_scp "${NEW_SERVICE_IPK}" "root@${QEMU_HOST}:${GUEST_STAGE}/new-service.ipk"
  run_scp "${NEW_LUCI_IPK}" "root@${QEMU_HOST}:${GUEST_STAGE}/new-luci.ipk"
  run_scp "${FEED_DIR}/Packages" "root@${QEMU_HOST}:${GUEST_STAGE}/feed/Packages"
  run_scp "${FEED_DIR}/$(basename "${NEW_SERVICE_IPK}")" "root@${QEMU_HOST}:${GUEST_STAGE}/feed/"
  run_scp "${FEED_DIR}/$(basename "${NEW_LUCI_IPK}")" "root@${QEMU_HOST}:${GUEST_STAGE}/feed/"
}

write_evidence_prelude() {
  cat > "${UPGRADE_EVIDENCE_FILE}" <<EOF
== task 11 upgrade/cache regression ==
old-version=${OLD_VERSION}
new-version=${NEW_VERSION}
same-origin-supported=0
helper=bash scripts/openwrt-vm/test-ipk-upgrade-uninstall.sh --old-version ${OLD_VERSION} --new-version ${NEW_VERSION}
playwright-helper=portal/e2e/helpers/luci-cache-probe.mjs
EOF

  cat > "${UNINSTALL_EVIDENCE_FILE}" <<EOF
== task 11 uninstall regression ==
service-version=${NEW_VERSION}
luci-version=${NEW_VERSION}
same-origin-supported=0
helper=bash scripts/openwrt-vm/test-ipk-upgrade-uninstall.sh --old-version ${OLD_VERSION} --new-version ${NEW_VERSION}
EOF
}

wait_for_api_ready() {
  local url="${TP_API_BASE_URL}/api/status"
  local body_file="$1"
  local status=''
  local attempt

  for attempt in $(seq 1 30); do
    status="$("${CURL_BIN}" -sS -o "${body_file}" -w '%{http_code}' --connect-timeout 2 --max-time 5 "${url}" || true)"
    if [[ "${status}" == '200' ]]; then
      printf '%s\n' "${status}"
      return 0
    fi
    sleep 1
  done

  fail "service health check 失败: ${url} 最终状态 ${status:-<empty>}"
}

wait_for_api_down() {
  local url="${TP_API_BASE_URL}/api/status"
  local body_file="$1"
  local status=''
  local attempt

  for attempt in $(seq 1 20); do
    status="$("${CURL_BIN}" -sS -o "${body_file}" -w '%{http_code}' --connect-timeout 2 --max-time 5 "${url}" || true)"
    if [[ "${status}" != '200' ]]; then
      printf '%s\n' "${status:-curl-failed}"
      return 0
    fi
    sleep 1
  done

  fail "service 在卸载后仍返回 200: ${url}"
}

start_luci_tunnel() {
  if [[ -n "${TUNNEL_PID}" ]] && kill -0 "${TUNNEL_PID}" >/dev/null 2>&1; then
    return 0
  fi

  LUCI_TUNNEL_PORT="$("${PYTHON_BIN}" - <<'PY'
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
  code="$("${CURL_BIN}" -sS -o "${body_file}" -D "${header_file}" -w '%{http_code}' --connect-timeout 5 --max-time 20 "$@" "${url}")"
  local curl_rc=$?
  set -e

  if (( curl_rc != 0 )); then
    printf 'curl-exit-%s' "${curl_rc}"
    return 0
  fi

  printf '%s' "${code}"
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

login_luci() {
  local login_body="$1"
  local login_header="$2"
  local login_url="http://127.0.0.1:${LUCI_TUNNEL_PORT}/cgi-bin/luci"
  local login_status auth_cookie

  login_status="$(http_probe "${login_url}" "${login_body}" "${login_header}" \
    -X POST \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    --data-urlencode 'luci_username=root' \
    --data-urlencode 'luci_password=')"
  auth_cookie="$(extract_auth_cookie "${login_header}")"

  [[ "${login_status}" == '200' || "${login_status}" == '302' || "${login_status}" == '303' ]] || fail "LuCI 登录状态异常: ${login_status}"
  [[ -n "${auth_cookie}" ]] || fail 'LuCI 登录未返回 sysauth cookie'
  printf '%s\n' "${auth_cookie}"
}

run_playwright_probe() {
  local label="$1"
  local output_json="$2"
  local screenshot_path="${3:-}"
  local -a args=(
    "${PLAYWRIGHT_HELPER}"
    --base-url "http://127.0.0.1:${LUCI_TUNNEL_PORT}"
    --route-path "${ROUTE_PATH}"
    --profile-dir "${PROFILE_DIR}"
    --output-json "${output_json}"
    --label "${label}"
  )

  if [[ -n "${screenshot_path}" ]]; then
    args+=(--screenshot "${screenshot_path}")
  fi

  "${NODE_BIN}" "${args[@]}"
}

append_file_to_evidence() {
  local title="$1"
  local input_path="$2"
  local output_path="$3"

  {
    printf '\n== %s ==\n' "${title}"
    cat "${input_path}"
  } >> "${output_path}"
}

prepare_upgrade_scenario() {
  log '准备 upgrade/cache 场景'
  run_ssh sh -s -- "${GUEST_STAGE}" "${OPENWRT_GUEST_SERVER}" "${OPENWRT_GUEST_CONFIG}" "${OPENWRT_GUEST_INITD}" >> "${UPGRADE_EVIDENCE_FILE}" <<'EOF'
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
    status_tmp="/usr/lib/opkg/status.task11.reset.$$"
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
opkg install "${guest_stage}/old-service.ipk"
opkg install "${guest_stage}/old-luci.ipk"
assert_install_result
/etc/init.d/rpcd reload >/dev/null 2>&1 || true
/etc/init.d/uhttpd reload >/dev/null 2>&1 || true

cat >> "${guest_config}" <<'CFG'
# task-11-service-upgrade-marker
CFG
cp "${guest_config}" "${guest_stage}/config-before-service-upgrade.yaml"
sha256sum "${guest_stage}/config-before-service-upgrade.yaml" > "${guest_stage}/config-before-service-upgrade.sha256"

printf '\n== install old packages ==\n'
printf 'opkg install %s/old-service.ipk\n' "${guest_stage}"
printf 'opkg install %s/old-luci.ipk\n' "${guest_stage}"
printf '\n== opkg status transparent-proxy ==\n'
opkg status transparent-proxy
printf '\n== opkg status luci-app-transparent-proxy ==\n'
opkg status luci-app-transparent-proxy
printf '\n== service lifecycle ==\n'
printf 'enabled=%s\n' "$("${guest_initd}" enabled >/dev/null 2>&1 && printf yes || printf no)"
printf 'running=%s\n' "$("${guest_initd}" running >/dev/null 2>&1 && printf yes || printf no)"
printf '\n== config before service upgrade ==\n'
cat "${guest_config}"
printf '\n== config before service upgrade sha256 ==\n'
cat "${guest_stage}/config-before-service-upgrade.sha256"
EOF
}

upgrade_service_package() {
  log '执行 service package upgrade'
  run_ssh sh -s -- "${GUEST_STAGE}" "${OPENWRT_GUEST_SERVER}" "${OPENWRT_GUEST_CONFIG}" "${OPENWRT_GUEST_INITD}" >> "${UPGRADE_EVIDENCE_FILE}" <<'EOF'
set -eu

guest_stage="$1"
guest_server="$2"
guest_config="$3"
guest_initd="$4"
expected_service_command="/etc/transparent-proxy/server -c /etc/transparent-proxy/config.yaml"
feed_conf="${guest_stage}/opkg-task11.conf"
distfeeds_conf="/etc/opkg/distfeeds.conf"
customfeeds_conf="/etc/opkg/customfeeds.conf"

restore_feeds() {
  for conf in "${distfeeds_conf}" "${customfeeds_conf}"; do
    if [ -f "${conf}.task11.bak" ]; then
      mv "${conf}.task11.bak" "${conf}"
    fi
  done
}

disable_default_feeds() {
  for conf in "${distfeeds_conf}" "${customfeeds_conf}"; do
    if [ -f "${conf}" ]; then
      cp "${conf}" "${conf}.task11.bak"
      : > "${conf}"
    fi
  done
}

trap restore_feeds EXIT

cat > "${feed_conf}" <<CFG
dest root /
dest ram /tmp
lists_dir ext /var/opkg-lists
option overlay_root /overlay
arch all 100
arch noarch 200
arch aarch64_generic 300
src local file://${guest_stage}/feed
CFG

disable_default_feeds
opkg -f "${feed_conf}" update
opkg -f "${feed_conf}" upgrade transparent-proxy

test -x /usr/bin/transparent-proxy
test -x "${guest_server}"
test -f "${guest_config}"
test -f "${guest_initd}"
grep -F -- "${expected_service_command}" "${guest_initd}" >/dev/null
cmp -s "${guest_stage}/config-before-service-upgrade.yaml" "${guest_config}"
grep -F -- '# task-11-service-upgrade-marker' "${guest_config}" >/dev/null

/etc/init.d/transparent-proxy restart >/dev/null 2>&1

printf '\n== upgrade transparent-proxy ==\n'
printf 'opkg -f %s update\n' "${feed_conf}"
printf 'opkg -f %s upgrade transparent-proxy\n' "${feed_conf}"
printf '\n== opkg status transparent-proxy ==\n'
opkg status transparent-proxy
printf '\n== config preserved compare ==\n'
cmp -s "${guest_stage}/config-before-service-upgrade.yaml" "${guest_config}"
printf 'cmp=identical\n'
printf '\n== config after service upgrade ==\n'
cat "${guest_config}"
printf '\n== config after service upgrade sha256 ==\n'
sha256sum "${guest_config}"
EOF
}

upgrade_luci_package() {
  log '执行 luci-app package upgrade + cache invalidation'
  run_ssh sh -s -- "${GUEST_STAGE}" >> "${UPGRADE_EVIDENCE_FILE}" <<'EOF'
set -eu

guest_stage="$1"
feed_conf="${guest_stage}/opkg-task11.conf"
distfeeds_conf="/etc/opkg/distfeeds.conf"
customfeeds_conf="/etc/opkg/customfeeds.conf"

restore_feeds() {
  for conf in "${distfeeds_conf}" "${customfeeds_conf}"; do
    if [ -f "${conf}.task11.bak" ]; then
      mv "${conf}.task11.bak" "${conf}"
    fi
  done
}

disable_default_feeds() {
  for conf in "${distfeeds_conf}" "${customfeeds_conf}"; do
    if [ -f "${conf}" ]; then
      cp "${conf}" "${conf}.task11.bak"
      : > "${conf}"
    fi
  done
}

trap restore_feeds EXIT

mkdir -p /tmp/luci-modulecache
printf 'stale-cache\n' > /tmp/luci-indexcache.task11-old
printf 'stale-module\n' > /tmp/luci-modulecache/task11-stale.js

disable_default_feeds
opkg -f "${feed_conf}" update
opkg -f "${feed_conf}" upgrade luci-app-transparent-proxy

test -f /usr/share/luci/menu.d/transparent-proxy.json
test -f /usr/share/rpcd/acl.d/luci-app-transparent-proxy.json
test -f /www/luci-static/resources/view/transparent-proxy/index.js
test ! -e /tmp/luci-indexcache.task11-old
test ! -e /tmp/luci-modulecache/task11-stale.js

/etc/init.d/rpcd reload >/dev/null 2>&1 || true
/etc/init.d/uhttpd reload >/dev/null 2>&1 || true

printf '\n== upgrade luci-app-transparent-proxy ==\n'
printf 'opkg -f %s update\n' "${feed_conf}"
printf 'opkg -f %s upgrade luci-app-transparent-proxy\n' "${feed_conf}"
printf '\n== opkg status luci-app-transparent-proxy ==\n'
opkg status luci-app-transparent-proxy
printf '\n== luci cache invalidation ==\n'
printf 'stale-index-cache=%s\n' "$(test -e /tmp/luci-indexcache.task11-old && printf present || printf removed)"
printf 'stale-module-cache=%s\n' "$(test -e /tmp/luci-modulecache/task11-stale.js && printf present || printf removed)"
printf '\n== luci files after upgrade ==\n'
ls -l /usr/share/luci/menu.d/transparent-proxy.json /usr/share/rpcd/acl.d/luci-app-transparent-proxy.json /www/luci-static/resources/view/transparent-proxy/index.js
printf '\n== luci file sha256 ==\n'
sha256sum /www/luci-static/resources/view/transparent-proxy/index.js /usr/share/luci/menu.d/transparent-proxy.json
EOF
}

verify_upgrade_and_cache() {
  local api_body="${TMP_DIR}/upgrade-api-status.body"
  local pre_json="${TMP_DIR}/upgrade-cache-pre.json"
  local post_json="${TMP_DIR}/upgrade-cache-post.json"

  wait_for_api_ready "${api_body}" >/dev/null
  start_luci_tunnel
  run_playwright_probe 'pre-upgrade' "${pre_json}"

  upgrade_service_package
  wait_for_api_ready "${api_body}" >/dev/null

  {
    printf '\n== host api after service upgrade ==\n'
    printf 'url=%s/api/status\n' "${TP_API_BASE_URL}"
    printf 'status=200\n'
    printf 'body=%s\n' "$(tr '\n' ' ' < "${api_body}")"
  } >> "${UPGRADE_EVIDENCE_FILE}"

  upgrade_luci_package
  run_playwright_probe 'post-upgrade' "${post_json}" "${UPGRADE_SCREENSHOT_FILE}"

  append_file_to_evidence 'playwright pre-upgrade probe' "${pre_json}" "${UPGRADE_EVIDENCE_FILE}"
  append_file_to_evidence 'playwright post-upgrade probe' "${post_json}" "${UPGRADE_EVIDENCE_FILE}"

  {
    printf '\n== screenshot ==\n'
    printf '%s\n' "${UPGRADE_SCREENSHOT_FILE}"
    printf '\n== conclusion ==\n'
    printf 'service-upgrade-config-preserved=pass\n'
    printf 'service-upgrade-health-ok=pass\n'
    printf 'luci-upgrade-cache-invalidated=pass\n'
    printf 'luci-upgrade-no-white-screen=pass\n'
  } >> "${UPGRADE_EVIDENCE_FILE}"
}

prepare_uninstall_branch() {
  local title="$1"
  log "准备 uninstall 场景: ${title}"
  {
    printf '\n== %s ==\n' "${title}"
  } >> "${UNINSTALL_EVIDENCE_FILE}"

  run_ssh sh -s -- "${GUEST_STAGE}" "${OPENWRT_GUEST_SERVER}" "${OPENWRT_GUEST_CONFIG}" "${OPENWRT_GUEST_INITD}" >> "${UNINSTALL_EVIDENCE_FILE}" <<'EOF'
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
    status_tmp="/usr/lib/opkg/status.task11.reset.$$"
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

reset_guest_layout
opkg install "${guest_stage}/new-service.ipk"
opkg install "${guest_stage}/new-luci.ipk"
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

/etc/init.d/rpcd reload >/dev/null 2>&1 || true
/etc/init.d/uhttpd reload >/dev/null 2>&1 || true

printf 'opkg install %s/new-service.ipk\n' "${guest_stage}"
printf 'opkg install %s/new-luci.ipk\n' "${guest_stage}"
printf 'service-enabled=%s\n' "$("${guest_initd}" enabled >/dev/null 2>&1 && printf yes || printf no)"
printf 'service-running=%s\n' "$("${guest_initd}" running >/dev/null 2>&1 && printf yes || printf no)"
printf 'baseline=service+luci installed\n'
EOF
}

remove_luci_only_branch() {
  local api_body="${TMP_DIR}/uninstall-luci-api-status.body"
  local login_body="${TMP_DIR}/uninstall-luci-login.body"
  local login_header="${TMP_DIR}/uninstall-luci-login.headers"
  local route_body="${TMP_DIR}/uninstall-luci-route.body"
  local route_header="${TMP_DIR}/uninstall-luci-route.headers"
  local auth_cookie route_status

  prepare_uninstall_branch 'branch A: uninstall luci-app only'

  run_ssh sh -s -- >> "${UNINSTALL_EVIDENCE_FILE}" <<'EOF'
set -eu

opkg remove luci-app-transparent-proxy
/etc/init.d/rpcd reload >/dev/null 2>&1 || true
/etc/init.d/uhttpd reload >/dev/null 2>&1 || true

printf '\npost-remove-luci-opkg-status=\n'
opkg status luci-app-transparent-proxy >/tmp/task11-luci-status.$$ 2>&1 || true
cat /tmp/task11-luci-status.$$
rm -f /tmp/task11-luci-status.$$
printf '\nservice-still-present=%s\n' "$(test -x /etc/transparent-proxy/server && printf yes || printf no)"
printf 'menu-json=%s\n' "$(test -e /usr/share/luci/menu.d/transparent-proxy.json && printf present || printf absent)"
printf 'view-js=%s\n' "$(test -e /www/luci-static/resources/view/transparent-proxy/index.js && printf present || printf absent)"
EOF

wait_for_api_ready "${api_body}" >/dev/null
start_luci_tunnel
auth_cookie="$(login_luci "${login_body}" "${login_header}")"
route_status="$(http_probe "http://127.0.0.1:${LUCI_TUNNEL_PORT}${ROUTE_PATH}" "${route_body}" "${route_header}" -H "Cookie: ${auth_cookie}")"
[[ "${route_status}" == '404' ]] || fail "卸载 luci-app 后路由状态异常: ${route_status}（期望 404）"

  {
    printf '\n== host verify after luci uninstall ==\n'
    printf 'api-url=%s/api/status\n' "${TP_API_BASE_URL}"
    printf 'api-status=200\n'
    printf 'api-body=%s\n' "$(tr '\n' ' ' < "${api_body}")"
    printf 'route-url=http://127.0.0.1:%s%s\n' "${LUCI_TUNNEL_PORT}" "${ROUTE_PATH}"
    printf 'route-status=%s\n' "${route_status}"
    printf '\n== conclusion ==\n'
    printf 'uninstall-luci-service-health=pass\n'
    printf 'uninstall-luci-route-cleared=pass\n'
  } >> "${UNINSTALL_EVIDENCE_FILE}"
}

remove_service_after_shell_branch() {
  local api_body="${TMP_DIR}/uninstall-service-api-status.body"
  local login_body="${TMP_DIR}/uninstall-service-login.body"
  local login_header="${TMP_DIR}/uninstall-service-login.headers"
  local route_body="${TMP_DIR}/uninstall-service-route.body"
  local route_header="${TMP_DIR}/uninstall-service-route.headers"
  local js_body="${TMP_DIR}/uninstall-service-view.body"
  local js_header="${TMP_DIR}/uninstall-service-view.headers"
  local auth_cookie route_status js_status api_status

  prepare_uninstall_branch 'branch B: uninstall transparent-proxy after shell participation'

  run_ssh sh -s -- >> "${UNINSTALL_EVIDENCE_FILE}" <<'EOF'
set -eu

opkg remove transparent-proxy
/etc/init.d/rpcd reload >/dev/null 2>&1 || true
/etc/init.d/uhttpd reload >/dev/null 2>&1 || true

printf '\npost-remove-service-opkg-status-transparent-proxy=\n'
opkg status transparent-proxy >/tmp/task11-service-status.$$ 2>&1 || true
cat /tmp/task11-service-status.$$
rm -f /tmp/task11-service-status.$$

printf '\npost-remove-service-opkg-status-luci=\n'
opkg status luci-app-transparent-proxy >/tmp/task11-luci-status.$$ 2>&1 || true
cat /tmp/task11-luci-status.$$
rm -f /tmp/task11-luci-status.$$

printf '\nservice-binary=%s\n' "$(test -e /etc/transparent-proxy/server && printf present || printf absent)"
printf 'service-initd=%s\n' "$(test -e /etc/init.d/transparent-proxy && printf present || printf absent)"
printf 'menu-json=%s\n' "$(test -e /usr/share/luci/menu.d/transparent-proxy.json && printf present || printf absent)"
printf 'view-js=%s\n' "$(test -e /www/luci-static/resources/view/transparent-proxy/index.js && printf present || printf absent)"
EOF

api_status="$(wait_for_api_down "${api_body}")"
start_luci_tunnel
auth_cookie="$(login_luci "${login_body}" "${login_header}")"
route_status="$(http_probe "http://127.0.0.1:${LUCI_TUNNEL_PORT}${ROUTE_PATH}" "${route_body}" "${route_header}" -H "Cookie: ${auth_cookie}")"

  case "${route_status}" in
    404)
      js_status='skipped-because-route-404'
      ;;
    200)
      js_status="$(http_probe "http://127.0.0.1:${LUCI_TUNNEL_PORT}${VIEW_JS_PATH}" "${js_body}" "${js_header}" -H "Cookie: ${auth_cookie}")"
      [[ "${js_status}" == '200' ]] || fail "service 卸载后 LuCI view JS 状态异常: ${js_status}"
      grep -F 'data-page="admin-services-transparent-proxy"' "${route_body}" >/dev/null || fail 'service 卸载后 route HTML 缺少 data-page 标记'
      grep -F "${FALLBACK_NOTICE}" "${route_body}" >/dev/null || fail 'service 卸载后 route HTML 缺少 fallbackNotice'
      grep -F 'tp-fallback-link' "${js_body}" >/dev/null || fail 'service 卸载后 LuCI view JS 缺少 fallback link'
      ;;
    *)
      fail "service 卸载后 LuCI route 返回异常状态: ${route_status}"
      ;;
  esac

  {
    printf '\n== host verify after service uninstall ==\n'
    printf 'api-url=%s/api/status\n' "${TP_API_BASE_URL}"
    printf 'api-status=%s\n' "${api_status}"
    printf 'route-url=http://127.0.0.1:%s%s\n' "${LUCI_TUNNEL_PORT}" "${ROUTE_PATH}"
    printf 'route-status=%s\n' "${route_status}"
    printf 'view-js-status=%s\n' "${js_status}"
    printf '\n== conclusion ==\n'
    printf 'uninstall-service-api-removed=pass\n'
    printf 'uninstall-service-luci-state-non-broken=pass\n'
    printf 'task-11-overall=pass\n'
  } >> "${UNINSTALL_EVIDENCE_FILE}"
}

verify_no_dist_residue() {
  if [[ -d "${OPENWRT_VM_REPO_ROOT}/dist/ipk/${OLD_VERSION}" ]]; then
    fail "dist 残留未清理: dist/ipk/${OLD_VERSION}"
  fi

  if [[ -d "${OPENWRT_VM_REPO_ROOT}/dist/ipk/${NEW_VERSION}" ]]; then
    fail "dist 残留未清理: dist/ipk/${NEW_VERSION}"
  fi
}

main() {
  parse_args "$@"
  prepare_tools
  assert_fallback_branch_locked

  log '调用共享 readiness 真值入口'
  bash "${SCRIPT_DIR}/test-common.sh" --ensure-vm-ready

  write_evidence_prelude
  build_packages_for_version "${OLD_VERSION}" OLD
  build_packages_for_version "${NEW_VERSION}" NEW
  stage_guest_assets

  prepare_upgrade_scenario
  verify_upgrade_and_cache
  remove_luci_only_branch
  remove_service_after_shell_branch

  log "upgrade/cache 证据已写入: ${UPGRADE_EVIDENCE_FILE}"
  log "uninstall 证据已写入: ${UNINSTALL_EVIDENCE_FILE}"
}

main "$@"
