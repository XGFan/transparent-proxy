#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CALLER_OPENWRT_IMAGE_RAW="${OPENWRT_IMAGE:-}"
source "${SCRIPT_DIR}/env.sh"

QEMU_BIN_CANDIDATE="${QEMU_BIN:-qemu-system-aarch64}"
QEMU_IMG_BIN="${QEMU_IMG_BIN:-qemu-img}"
BOOT_TIMEOUT_SECONDS="${BOOT_TIMEOUT_SECONDS:-20}"

SERIAL_LOG_PATH="${OPENWRT_VM_LOG_DIR}/serial-$(date +%Y%m%d-%H%M%S).log"

resolve_abs_path() {
  local path="$1"
  if [[ "${path}" == /* ]]; then
    printf '%s\n' "${path}"
  else
    printf '%s\n' "${PWD}/${path}"
  fi
}

create_overlay_from_base() {
  local qemu_img_bin="$1"
  local base_image="$2"

  local overlay_dir
  overlay_dir="${OPENWRT_VM_OVERLAY_DIR:-${OPENWRT_VM_RUN_DIR}/overlays}"
  mkdir -p "${overlay_dir}"

  local base_abs
  base_abs="$(cd -P "$(dirname "${base_image}")" && pwd -P)/$(basename "${base_image}")"

  local ts rand overlay_path
  ts="$(date +%Y%m%d-%H%M%S)"
  rand="$(python3 - <<'PY'
import secrets
print(secrets.token_hex(4))
PY
)"
  overlay_path="${overlay_dir}/overlay-${ts}-${rand}.qcow2"

  "${qemu_img_bin}" create -f qcow2 -F raw -b "${base_abs}" "${overlay_path}" >/dev/null

  printf 'overlay 创建完成: %s\n' "${overlay_path}"
  printf 'backing file: %s\n' "${base_abs}"
  printf '可验证命令: qemu-img info --backing-chain "%s"\n' "${overlay_path}"
}

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

require_port_free() {
  local port="$1"

  if ! python3 - "${port}" <<'PY'
import socket
import sys

port = int(sys.argv[1])
sock = socket.socket()
try:
    sock.bind(("127.0.0.1", port))
except OSError:
    raise SystemExit(1)
finally:
    sock.close()
PY
  then
    printf '端口被占用: 127.0.0.1:%s\n' "${port}" >&2
    exit 3
  fi
}

resolve_efi_code() {
  local candidate
  local candidates=(
    "${QEMU_EFI_CODE:-}"
    "/opt/homebrew/share/qemu/edk2-aarch64-code.fd"
    "/opt/homebrew/share/qemu/edk2-arm-code.fd"
    "/usr/local/share/qemu/edk2-aarch64-code.fd"
    "/usr/local/share/qemu/edk2-arm-code.fd"
    "/usr/share/qemu-efi-aarch64/QEMU_EFI.fd"
  )

  for candidate in "${candidates[@]}"; do
    if [[ -n "${candidate}" && -f "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done

  return 1
}

main() {
  local qemu_bin
  qemu_bin="$(resolve_command "${QEMU_BIN_CANDIDATE}")" || {
    printf '缺少 QEMU 可执行文件: %s\n' "${QEMU_BIN_CANDIDATE}" >&2
    exit 2
  }

  resolve_command "${QEMU_IMG_BIN}" >/dev/null || {
    printf '缺少 qemu-img，可执行文件: %s\n' "${QEMU_IMG_BIN}" >&2
    exit 2
  }
  QEMU_IMG_BIN="$(resolve_command "${QEMU_IMG_BIN}")"

  if [[ "${QEMU_NET}" != "user" ]]; then
    printf '仅支持 QEMU user networking（当前 QEMU_NET=%s）\n' "${QEMU_NET}" >&2
    exit 2
  fi

  if ! "${qemu_bin}" -accel help 2>/dev/null | grep -qw 'hvf'; then
    printf '当前 qemu-system-aarch64 不支持 hvf，请安装支持 hvf 的构建。\n' >&2
    exit 2
  fi

  local efi_code
  efi_code="$(resolve_efi_code)" || {
    printf '未找到 AArch64 UEFI 固件，请设置 QEMU_EFI_CODE 或安装 edk2。\n' >&2
    exit 2
  }

  mkdir -p "${OPENWRT_VM_BASE_DIR}" "${OPENWRT_VM_LOG_DIR}" "${OPENWRT_VM_STATE_DIR}"

  local boot_image_path
  local caller_openwrt_image
  boot_image_path="${OPENWRT_IMAGE_PATH}"
  caller_openwrt_image=""
  if [[ -n "${CALLER_OPENWRT_IMAGE_RAW}" && "${CALLER_OPENWRT_IMAGE_RAW}" != "${OPENWRT_IMAGE}" ]]; then
    caller_openwrt_image="${CALLER_OPENWRT_IMAGE_RAW}"
    boot_image_path="$(resolve_abs_path "${caller_openwrt_image}")"
  fi

  if [[ ! -f "${boot_image_path}" ]]; then
    if [[ -n "${caller_openwrt_image}" ]]; then
      printf 'OPENWRT_IMAGE 指定镜像不存在: %s\n' "${boot_image_path}" >&2
    else
      printf '基础镜像不存在，请先执行: bash scripts/openwrt-vm/fetch-image.sh\n' >&2
    fi
    exit 2
  fi

  if [[ -f "${OPENWRT_VM_PID_FILE}" ]]; then
    local existing_pid
    existing_pid="$(<"${OPENWRT_VM_PID_FILE}")"
    if [[ -n "${existing_pid}" ]] && kill -0 "${existing_pid}" >/dev/null 2>&1; then
      printf '检测到已有运行中的 VM（pid=%s），请先执行 teardown-vm.sh\n' "${existing_pid}" >&2
      exit 3
    fi
    rm -f "${OPENWRT_VM_PID_FILE}"
  fi

  require_port_free "${SSH_PORT}"
  require_port_free "${API_PORT}"

  local prepare_output overlay_path line
  prepare_output="$(create_overlay_from_base "${QEMU_IMG_BIN}" "${boot_image_path}")"
  printf '%s\n' "${prepare_output}"

  overlay_path=""
  while IFS= read -r line; do
    case "${line}" in
      overlay\ 创建完成:\ *)
        overlay_path="${line#overlay 创建完成: }"
        ;;
    esac
  done <<< "${prepare_output}"

  if [[ -z "${overlay_path}" || ! -f "${overlay_path}" ]]; then
    printf 'overlay 创建失败，未得到有效路径。\n' >&2
    exit 4
  fi

  rm -f "${OPENWRT_VM_MONITOR_SOCKET}" "${OPENWRT_VM_SERIAL_SOCKET}"
  rm -f "${OPENWRT_VM_BOOTSTRAP_STATE_FILE}" "${OPENWRT_VM_READY_STATE_FILE}"

  if ! "${qemu_bin}" \
    -daemonize \
    -pidfile "${OPENWRT_VM_PID_FILE}" \
    -name openwrt-vm \
    -machine virt,accel=hvf \
    -cpu host \
    -smp 2 \
    -m 1024 \
    -bios "${efi_code}" \
    -drive if=virtio,format=qcow2,file="${overlay_path}" \
    -netdev "user,id=net0,hostfwd=tcp:${QEMU_HOST}:${SSH_PORT}-:22,hostfwd=tcp:${QEMU_HOST}:${API_PORT}-:1444" \
    -device virtio-net-pci,netdev=net0 \
    -display none \
    -chardev "socket,id=serial0,path=${OPENWRT_VM_SERIAL_SOCKET},server=on,wait=off,logfile=${SERIAL_LOG_PATH},logappend=on" \
    -serial chardev:serial0 \
    -monitor "unix:${OPENWRT_VM_MONITOR_SOCKET},server=on,wait=off"; then
    printf 'QEMU 启动失败。\n' >&2
    exit 5
  fi

  if [[ ! -f "${OPENWRT_VM_PID_FILE}" ]]; then
    printf 'QEMU 未写入 pid 文件: %s\n' "${OPENWRT_VM_PID_FILE}" >&2
    exit 5
  fi

  local qemu_pid
  qemu_pid="$(<"${OPENWRT_VM_PID_FILE}")"

  local waited=0
  while (( waited < BOOT_TIMEOUT_SECONDS )); do
    if kill -0 "${qemu_pid}" >/dev/null 2>&1 && [[ -S "${OPENWRT_VM_SERIAL_SOCKET}" && -S "${OPENWRT_VM_MONITOR_SOCKET}" ]]; then
      break
    fi
    sleep 1
    waited=$((waited + 1))
  done

  if ! kill -0 "${qemu_pid}" >/dev/null 2>&1; then
    printf 'QEMU 进程未存活（pid=%s）。\n' "${qemu_pid}" >&2
    exit 5
  fi

  if [[ ! -S "${OPENWRT_VM_SERIAL_SOCKET}" || ! -S "${OPENWRT_VM_MONITOR_SOCKET}" ]]; then
    printf 'QEMU socket 未就绪（serial=%s monitor=%s）。\n' "${OPENWRT_VM_SERIAL_SOCKET}" "${OPENWRT_VM_MONITOR_SOCKET}" >&2
    exit 5
  fi

  printf '%s\n' "${overlay_path}" > "${OPENWRT_VM_OVERLAY_FILE}"
  printf '%s\n' "${SERIAL_LOG_PATH}" > "${OPENWRT_VM_SERIAL_LOG_FILE}"
  printf '%s\n' "${OPENWRT_VM_MONITOR_SOCKET}" > "${OPENWRT_VM_MONITOR_SOCKET_FILE}"
  printf '%s\n' "${OPENWRT_VM_SERIAL_SOCKET}" > "${OPENWRT_VM_SERIAL_SOCKET_FILE}"

  printf 'QEMU 已启动\n'
  printf 'pid: %s\n' "${qemu_pid}"
  printf 'overlay: %s\n' "${overlay_path}"
  printf 'serial log: %s\n' "${SERIAL_LOG_PATH}"
  printf 'serial socket: %s\n' "${OPENWRT_VM_SERIAL_SOCKET}"
  printf 'monitor socket: %s\n' "${OPENWRT_VM_MONITOR_SOCKET}"
}

main "$@"
