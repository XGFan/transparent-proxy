#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

GO_BIN="${GO_BIN:-go}"
SSH_BIN="${SSH_BIN:-ssh}"
SCP_BIN="${SCP_BIN:-scp}"
REMOTE_USER="${OPENWRT_VM_SSH_USER:-root}"
REMOTE_HOST="${QEMU_HOST}"
REMOTE_STAGE_BASE="${REMOTE_STAGE_BASE:-/tmp}"
GUEST_ROOT="${OPENWRT_GUEST_ROOT}"
GUEST_SERVER="${OPENWRT_GUEST_SERVER}"
GUEST_CONFIG="${OPENWRT_GUEST_CONFIG}"
GUEST_INITD="${OPENWRT_GUEST_INITD}"
EXPECTED_SERVICE_COMMAND="/usr/bin/transparent-proxy -c ${GUEST_CONFIG}"
REMOTE_STAGE=""
HOST_STAGE_DIR=""

SSH_BASE=()
SCP_BASE=()

cleanup() {
  local exit_code=$?

  if [[ -n "${HOST_STAGE_DIR}" && -d "${HOST_STAGE_DIR}" ]]; then
    rm -rf "${HOST_STAGE_DIR}"
  fi

  if [[ -n "${REMOTE_STAGE}" && ${#SSH_BASE[@]} -gt 0 ]]; then
    "${SSH_BASE[@]}" "rm -rf '${REMOTE_STAGE}'" >/dev/null 2>&1 || true
  fi

  trap - EXIT
  exit "${exit_code}"
}

trap cleanup EXIT

resolve_command() {
  local cmd="$1"

  if [[ "${cmd}" == */* ]]; then
    if [[ -x "${cmd}" ]]; then
      printf '%s\n' "${cmd}"
      return 0
    fi
    return 1
  fi

  command -v "${cmd}"
}

run_ssh() {
  "${SSH_BASE[@]}" "$@"
}

run_scp() {
  "${SCP_BASE[@]}" "$@"
}

prepare_transport() {
  GO_BIN="$(resolve_command "${GO_BIN}")" || {
    printf '缺少 Go 可执行文件: %s\n' "${GO_BIN}" >&2
    exit 2
  }

  SSH_BIN="$(resolve_command "${SSH_BIN}")" || {
    printf '缺少 SSH 可执行文件: %s\n' "${SSH_BIN}" >&2
    exit 2
  }

  SCP_BIN="$(resolve_command "${SCP_BIN}")" || {
    printf '缺少 SCP 可执行文件: %s\n' "${SCP_BIN}" >&2
    exit 2
  }

  if [[ ! -f "${OPENWRT_TEST_KEY_PATH}" ]]; then
    printf '测试 SSH 私钥不存在: %s\n' "${OPENWRT_TEST_KEY_PATH}" >&2
    exit 2
  fi

  SSH_BASE=(
    "${SSH_BIN}"
    -o StrictHostKeyChecking=no
    -o UserKnownHostsFile=/dev/null
    -o ConnectTimeout=5
    -o BatchMode=yes
    -i "${OPENWRT_TEST_KEY_PATH}"
    -p "${SSH_PORT}"
    "${REMOTE_USER}@${REMOTE_HOST}"
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

prepare_host_stage() {
  mkdir -p "${OPENWRT_VM_RUN_DIR}"
  HOST_STAGE_DIR="$(mktemp -d "${OPENWRT_VM_RUN_DIR}/deploy-host.XXXXXX")"

  (
    cd "${OPENWRT_VM_REPO_ROOT}/server"
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 "${GO_BIN}" build -o "${HOST_STAGE_DIR}/server" .
  )

  chmod 755 "${HOST_STAGE_DIR}/server"

  # Stage supporting files (init.d, hotplug, static nft rules)
  local files_root="${OPENWRT_VM_REPO_ROOT}/files"
  cp "${files_root}/etc/init.d/transparent-proxy" "${HOST_STAGE_DIR}/initd"
  cp "${files_root}/etc/hotplug.d/iface/80-ifup-wan" "${HOST_STAGE_DIR}/hotplug"
  cp "${files_root}/etc/nftables.d/reserved_ip.nft" "${HOST_STAGE_DIR}/reserved_ip.nft"
  cp "${files_root}/etc/nftables.d/v6block.nft" "${HOST_STAGE_DIR}/v6block.nft"
}

upload_stage() {
  REMOTE_STAGE="${REMOTE_STAGE_BASE%/}/transparent-proxy-deploy.$$.$(date +%s)"

  run_ssh "mkdir -p '${REMOTE_STAGE}'"
  run_scp "${HOST_STAGE_DIR}/server" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_STAGE}/server"
  run_scp "${HOST_STAGE_DIR}/initd" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_STAGE}/initd"
  run_scp "${HOST_STAGE_DIR}/hotplug" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_STAGE}/hotplug"
  run_scp "${HOST_STAGE_DIR}/reserved_ip.nft" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_STAGE}/reserved_ip.nft"
  run_scp "${HOST_STAGE_DIR}/v6block.nft" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_STAGE}/v6block.nft"
}

install_on_guest() {
  run_ssh sh -s -- \
    "${REMOTE_STAGE}" \
    "${GUEST_ROOT}" \
    "${GUEST_SERVER}" \
    "${GUEST_CONFIG}" \
    "${GUEST_INITD}" \
    "${EXPECTED_SERVICE_COMMAND}" <<'EOF'
set -eu

stage="$1"
guest_root="$2"
guest_server="$3"
guest_config="$4"
guest_initd="$5"
expected_service_command="$6"

install_file() {
  src="$1"
  dst="$2"
  mode="$3"
  dir="${dst%/*}"
  base="${dst##*/}"
  tmp="${dir}/.${base}.deploy.$$"

  test -f "${src}"
  mkdir -p "${dir}"
  rm -f "${tmp}"
  cp "${src}" "${tmp}"
  chmod "${mode}" "${tmp}"
  mv "${tmp}" "${dst}"
}

run_bootstrap_once() {
  bootstrap_log="/tmp/transparent-proxy-bootstrap.log"
  rm -f "${bootstrap_log}"

  "${guest_server}" -c "${guest_config}" >"${bootstrap_log}" 2>&1 &
  bootstrap_pid="$!"

  ready=0
  tries=0
  while [ "${tries}" -lt 30 ]; do
    tries=$((tries + 1))

    if [ -f "${guest_config}" ] && [ -f "${guest_initd}" ]; then
      ready=1
      break
    fi

    if ! kill -0 "${bootstrap_pid}" >/dev/null 2>&1; then
      wait "${bootstrap_pid}" || true
      if [ -f "${guest_config}" ] && [ -f "${guest_initd}" ]; then
        ready=1
      fi
      break
    fi

    sleep 1
  done

  if kill -0 "${bootstrap_pid}" >/dev/null 2>&1; then
    kill "${bootstrap_pid}" >/dev/null 2>&1 || true
    wait "${bootstrap_pid}" >/dev/null 2>&1 || true
  fi

  if [ "${ready}" -ne 1 ]; then
    printf 'bootstrap 失败：未在时限内生成 canonical config/init 脚本\n' >&2
    if [ -f "${bootstrap_log}" ]; then
      cat "${bootstrap_log}" >&2
    fi
    return 1
  fi
}

test -f "${stage}/server"
mkdir -p "${guest_root}"

# Install supporting files before bootstrap
install_file "${stage}/initd" "${guest_initd}" 755
mkdir -p /etc/hotplug.d/iface
install_file "${stage}/hotplug" /etc/hotplug.d/iface/80-ifup-wan 755
mkdir -p /etc/nftables.d
mkdir -p /usr/share/nftables.d/table-post
install_file "${stage}/reserved_ip.nft" /etc/nftables.d/reserved_ip.nft 644
install_file "${stage}/v6block.nft" /etc/nftables.d/v6block.nft 644

# Load static nft rules so sets exist before bootstrap
nft -f /etc/nftables.d/reserved_ip.nft 2>/dev/null || true
nft -f /etc/nftables.d/v6block.nft 2>/dev/null || true

install_file "${stage}/server" "${guest_server}" 755
install_file "${stage}/server" /usr/bin/transparent-proxy 755
run_bootstrap_once

test -x "${guest_server}"
test -f "${guest_config}"
test -f "${guest_initd}"
grep -F -- "${expected_service_command}" "${guest_initd}" >/dev/null

rm -rf "${stage}"
EOF

  REMOTE_STAGE=""
}

main() {
  prepare_transport
  prepare_host_stage

  printf '验证 guest SSH 可用...\n'
  run_ssh 'test -d /tmp'

  printf '构建 host 侧 linux/arm64 后端（仅单二进制）并准备 staging...\n'
  upload_stage

  printf '将单二进制落盘到 guest 并执行真实首启自举...\n'
  install_on_guest

  printf 'deploy 完成。\n'
}

main "$@"
