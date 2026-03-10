#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

SSH_BIN="${SSH_BIN:-ssh}"
SCP_BIN="${SCP_BIN:-scp}"

INSTALL_IPK=""
UPGRADE_IPK=""
INSTALL_EVIDENCE="${OPENWRT_VM_REPO_ROOT}/.sisyphus/evidence/task-7-service-lifecycle-install.txt"
UPGRADE_EVIDENCE="${OPENWRT_VM_REPO_ROOT}/.sisyphus/evidence/task-7-service-lifecycle-upgrade.txt"

SSH_BASE=()
SCP_BASE=()
TMP_DIRS=()

log() {
  printf '[service-ipk-lifecycle] %s\n' "$*" >&2
}

fail() {
  printf '[service-ipk-lifecycle][ERROR] %s\n' "$*" >&2
  exit 1
}

cleanup() {
  local dir
  for dir in "${TMP_DIRS[@]}"; do
    [[ -n "${dir}" ]] && rm -rf "${dir}" || true
  done
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage: bash scripts/openwrt-vm/test-service-ipk-lifecycle.sh \
  --install-ipk <path> \
  --upgrade-ipk <path> \
  [--install-evidence <path>] \
  [--upgrade-evidence <path>]
EOF
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

prepare_ssh() {
  SSH_BIN="$(resolve_command "${SSH_BIN}")" || fail "缺少 ssh 可执行文件: ${SSH_BIN}"
  SCP_BIN="$(resolve_command "${SCP_BIN}")" || fail "缺少 scp 可执行文件: ${SCP_BIN}"
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
}

run_ssh() {
  "${SSH_BASE[@]}" "$@"
}

run_scp() {
  "${SCP_BASE[@]}" "$@"
}

ensure_parent_dir() {
  local path="$1"
  mkdir -p "$(dirname "${path}")"
}

build_feed() {
  local ipk_path="$1"
  local output_dir="$2"
  local filename packages_path control_dir size checksum

  filename="$(basename "${ipk_path}")"
  packages_path="${output_dir}/Packages"
  control_dir="$(mktemp -d "${output_dir}/control.XXXXXX")"
  TMP_DIRS+=("${control_dir}")

  cp "${ipk_path}" "${output_dir}/${filename}"
  python3 - "${ipk_path}" "${control_dir}/control.tar.gz" <<'PY'
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
  } > "${packages_path}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --install-ipk)
      INSTALL_IPK="$(resolve_abs_path "$2")"
      shift 2
      ;;
    --upgrade-ipk)
      UPGRADE_IPK="$(resolve_abs_path "$2")"
      shift 2
      ;;
    --install-evidence)
      INSTALL_EVIDENCE="$(resolve_abs_path "$2")"
      shift 2
      ;;
    --upgrade-evidence)
      UPGRADE_EVIDENCE="$(resolve_abs_path "$2")"
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

[[ -n "${INSTALL_IPK}" ]] || fail '缺少 --install-ipk'
[[ -n "${UPGRADE_IPK}" ]] || fail '缺少 --upgrade-ipk'
[[ -f "${INSTALL_IPK}" ]] || fail "install ipk 不存在: ${INSTALL_IPK}"
[[ -f "${UPGRADE_IPK}" ]] || fail "upgrade ipk 不存在: ${UPGRADE_IPK}"

ensure_parent_dir "${INSTALL_EVIDENCE}"
ensure_parent_dir "${UPGRADE_EVIDENCE}"
prepare_ssh

log '执行 VM readiness 真值入口'
bash "${SCRIPT_DIR}/test-common.sh" --ensure-vm-ready

host_feed_dir="$(mktemp -d "${OPENWRT_VM_RUN_DIR}/service-ipk-feed.XXXXXX")"
TMP_DIRS+=("${host_feed_dir}")
build_feed "${UPGRADE_IPK}" "${host_feed_dir}"

guest_stage="/tmp/transparent-proxy-ipk-lifecycle"
run_ssh "rm -rf '${guest_stage}' && mkdir -p '${guest_stage}/feed'"
run_scp "${INSTALL_IPK}" "root@${QEMU_HOST}:${guest_stage}/install.ipk"
run_scp "${host_feed_dir}/$(basename "${UPGRADE_IPK}")" "root@${QEMU_HOST}:${guest_stage}/feed/"
run_scp "${host_feed_dir}/Packages" "root@${QEMU_HOST}:${guest_stage}/feed/Packages"

log '执行 install 场景并写证据'
run_ssh sh -s -- "${guest_stage}" "${OPENWRT_GUEST_SERVER}" "${OPENWRT_GUEST_CONFIG}" "${OPENWRT_GUEST_INITD}" > "${INSTALL_EVIDENCE}" <<'EOF'
set -eu

guest_stage="$1"
guest_server="$2"
guest_config="$3"
guest_initd="$4"
expected_service_command="/etc/transparent-proxy/server -c /etc/transparent-proxy/config.yaml"

reset_guest_layout() {
  if [ -x /etc/init.d/transparent-proxy ]; then
    /etc/init.d/transparent-proxy stop >/dev/null 2>&1 || true
    /etc/init.d/transparent-proxy disable >/dev/null 2>&1 || true
  fi

  opkg remove transparent-proxy >/dev/null 2>&1 || true
  rm -f /usr/lib/opkg/info/transparent-proxy.*
  if [ -f /usr/lib/opkg/status ]; then
    status_tmp="/usr/lib/opkg/status.transparent-proxy-reset.$$"
    awk -v RS='' -v ORS='\n\n' '$0 !~ /^Package: transparent-proxy(\n|$)/' /usr/lib/opkg/status > "${status_tmp}"
    mv "${status_tmp}" /usr/lib/opkg/status
  fi
  rm -f /usr/bin/transparent-proxy /var/run/transparent-proxy.pid

  for path in \
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
    /etc/transparent-proxy/state/managed-records.json
  do
    rm -f "${path}"
  done

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

  for path in \
    /etc/hotplug.d/iface/80-ifup-wan \
    /etc/nftables.d/direct_dst.nft \
    /etc/nftables.d/proxy.nft \
    /etc/transparent-proxy/enable.sh \
    /etc/transparent-proxy/transparent.nft
  do
    test -f "${path}"
  done
}

reset_guest_layout
opkg install "${guest_stage}/install.ipk"
assert_install_result

printf '== install command ==\n'
printf 'opkg install %s/install.ipk\n' "${guest_stage}"
printf '\n== opkg status ==\n'
opkg status transparent-proxy
printf '\n== canonical files ==\n'
ls -l /usr/bin/transparent-proxy "${guest_server}" "${guest_config}" "${guest_initd}"
printf '\n== managed assets ==\n'
ls -l /etc/hotplug.d/iface/80-ifup-wan /etc/nftables.d/direct_dst.nft /etc/nftables.d/proxy.nft /etc/transparent-proxy/enable.sh /etc/transparent-proxy/transparent.nft
printf '\n== init command assertion ==\n'
grep -nF -- "${expected_service_command}" "${guest_initd}"
printf '\n== service lifecycle ==\n'
printf 'enabled=%s\n' "$("${guest_initd}" enabled >/dev/null 2>&1 && printf yes || printf no)"
printf 'running=%s\n' "$("${guest_initd}" running >/dev/null 2>&1 && printf yes || printf no)"
printf '\n== config content ==\n'
cat "${guest_config}"
printf '\n== config sha256 ==\n'
sha256sum "${guest_config}"
EOF

log '执行 upgrade 场景并写证据'
run_ssh sh -s -- "${guest_stage}" "${OPENWRT_GUEST_SERVER}" "${OPENWRT_GUEST_CONFIG}" "${OPENWRT_GUEST_INITD}" > "${UPGRADE_EVIDENCE}" <<'EOF'
set -eu

guest_stage="$1"
guest_server="$2"
guest_config="$3"
guest_initd="$4"
expected_service_command="/etc/transparent-proxy/server -c /etc/transparent-proxy/config.yaml"
feed_conf="${guest_stage}/opkg-lifecycle.conf"
before_copy="${guest_stage}/config-before-upgrade.yaml"
distfeeds_conf="/etc/opkg/distfeeds.conf"
customfeeds_conf="/etc/opkg/customfeeds.conf"

restore_feeds() {
  for conf in "${distfeeds_conf}" "${customfeeds_conf}"; do
    if [ -f "${conf}.task7.bak" ]; then
      mv "${conf}.task7.bak" "${conf}"
    fi
  done
}

disable_default_feeds() {
  for conf in "${distfeeds_conf}" "${customfeeds_conf}"; do
    if [ -f "${conf}" ]; then
      cp "${conf}" "${conf}.task7.bak"
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

cat >> "${guest_config}" <<'CFG'
server:
  listenAddress: ":1555"
# task-7-upgrade-marker
CFG
cp "${guest_config}" "${before_copy}"
before_sha="$(sha256sum "${before_copy}" | awk '{print $1}')"

disable_default_feeds
opkg -f "${feed_conf}" update
opkg -f "${feed_conf}" upgrade transparent-proxy

test -x /usr/bin/transparent-proxy
test -x "${guest_server}"
test -f "${guest_config}"
test -f "${guest_initd}"
grep -F -- "${expected_service_command}" "${guest_initd}" >/dev/null
cmp -s "${before_copy}" "${guest_config}"
grep -F -- '# task-7-upgrade-marker' "${guest_config}" >/dev/null
grep -F -- 'listenAddress: ":1555"' "${guest_config}" >/dev/null

printf '== upgrade command ==\n'
printf 'opkg -f %s update\n' "${feed_conf}"
printf 'opkg -f %s upgrade transparent-proxy\n' "${feed_conf}"
printf '\n== opkg status ==\n'
opkg status transparent-proxy
printf '\n== config checksum compare ==\n'
printf 'before=%s\n' "${before_sha}"
sha256sum "${guest_config}"
printf '\n== config preserved ==\n'
cmp -s "${before_copy}" "${guest_config}"
printf 'cmp=identical\n'
printf '\n== config content ==\n'
cat "${guest_config}"
printf '\n== canonical files ==\n'
ls -l /usr/bin/transparent-proxy "${guest_server}" "${guest_config}" "${guest_initd}"
printf '\n== init command assertion ==\n'
grep -nF -- "${expected_service_command}" "${guest_initd}"
EOF

log 'service ipk lifecycle install/upgrade 证据已生成'
