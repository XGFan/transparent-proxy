#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

SHUTDOWN_TIMEOUT="${SHUTDOWN_TIMEOUT:-20}"

declare -a TARGET_PIDS=()

monitor_cmd() {
  local socket_path="$1"
  local cmd="$2"

  python3 - "${socket_path}" "${cmd}" <<'PY'
import socket
import sys

socket_path = sys.argv[1]
cmd = sys.argv[2]

sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
sock.settimeout(2)
try:
    sock.connect(socket_path)
    sock.sendall((cmd + "\n").encode("utf-8", errors="ignore"))
except OSError:
    raise SystemExit(1)
finally:
    sock.close()
PY
}

wait_process_exit() {
  local pid="$1"
  local timeout="$2"
  local deadline=$((SECONDS + timeout))

  while (( SECONDS < deadline )); do
    if ! kill -0 "${pid}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  return 1
}

collect_target_pids() {
  local monitor_socket="$1"
  local serial_socket="$2"
  local pid_file_pid="$3"
  local output

  output="$(python3 - "${monitor_socket}" "${serial_socket}" "${QEMU_HOST}" "${SSH_PORT}" "${API_PORT}" "${OPENWRT_VM_GUEST_IP}" "${pid_file_pid}" <<'PY'
import subprocess
import sys

monitor_socket = sys.argv[1]
serial_socket = sys.argv[2]
host = sys.argv[3]
ssh_port = sys.argv[4]
api_port = sys.argv[5]
guest_ip = sys.argv[6]
pid_file_pid = sys.argv[7]

matched = []
seen = set()

def add_pid(value: str) -> None:
    value = value.strip()
    if not value:
        return
    if not value.isdigit():
        return
    if value in seen:
        return
    seen.add(value)
    matched.append(value)

add_pid(pid_file_pid)

try:
    ps_out = subprocess.check_output(["ps", "-ax", "-o", "pid=", "-o", "command="], text=True)
except subprocess.CalledProcessError:
    ps_out = ""

for raw in ps_out.splitlines():
    row = raw.strip()
    if not row:
        continue
    parts = row.split(None, 1)
    if len(parts) != 2:
        continue

    pid, cmd = parts
    if "qemu-system-aarch64" not in cmd:
        continue

    has_name = "-name openwrt-vm" in cmd
    has_monitor = f"unix:{monitor_socket}" in cmd if monitor_socket else False
    has_serial = f"path={serial_socket}" in cmd if serial_socket else False
    has_hostfwd_default = (
        f"hostfwd=tcp:{host}:{ssh_port}-:22" in cmd
        and f"hostfwd=tcp:{host}:{api_port}-:1444" in cmd
    )
    has_hostfwd_guest_ip = (
        f"hostfwd=tcp:{host}:{ssh_port}-{guest_ip}:22" in cmd
        and f"hostfwd=tcp:{host}:{api_port}-{guest_ip}:1444" in cmd
    )
    has_hostfwd = has_hostfwd_default or has_hostfwd_guest_ip

    if has_name or has_monitor or has_serial or has_hostfwd:
        add_pid(pid)

print("\n".join(matched))
PY
)"

  TARGET_PIDS=()
  while IFS= read -r pid; do
    if [[ -n "${pid}" ]] && kill -0 "${pid}" >/dev/null 2>&1; then
      TARGET_PIDS+=("${pid}")
    fi
  done <<< "${output}"
}

remaining_target_pids() {
  local pid
  local still=()
  for pid in "${TARGET_PIDS[@]}"; do
    if kill -0 "${pid}" >/dev/null 2>&1; then
      still+=("${pid}")
    fi
  done
  TARGET_PIDS=("${still[@]}")
}

wait_all_targets_exit() {
  local timeout="$1"
  local deadline=$((SECONDS + timeout))

  while (( SECONDS < deadline )); do
    remaining_target_pids
    if (( ${#TARGET_PIDS[@]} == 0 )); then
      return 0
    fi
    sleep 1
  done

  remaining_target_pids
  (( ${#TARGET_PIDS[@]} == 0 ))
}

cleanup_state() {
  local overlay_path=""
  if [[ -f "${OPENWRT_VM_OVERLAY_FILE}" ]]; then
    overlay_path="$(<"${OPENWRT_VM_OVERLAY_FILE}")"
  fi

  rm -f "${OPENWRT_VM_PID_FILE}" \
    "${OPENWRT_VM_MONITOR_SOCKET}" \
    "${OPENWRT_VM_SERIAL_SOCKET}" \
    "${OPENWRT_VM_MONITOR_SOCKET_FILE}" \
    "${OPENWRT_VM_SERIAL_SOCKET_FILE}" \
    "${OPENWRT_VM_BOOTSTRAP_STATE_FILE}" \
    "${OPENWRT_VM_READY_STATE_FILE}" \
    "${OPENWRT_VM_OVERLAY_FILE}" \
    "${OPENWRT_VM_SERIAL_LOG_FILE}"

  if [[ -n "${overlay_path}" ]]; then
    rm -f "${overlay_path}.lck" "${overlay_path}.lock"
  fi
}

main() {
  local pid=""
  if [[ -f "${OPENWRT_VM_PID_FILE}" ]]; then
    pid="$(<"${OPENWRT_VM_PID_FILE}")"
  fi

  local monitor_socket="${OPENWRT_VM_MONITOR_SOCKET}"
  if [[ -f "${OPENWRT_VM_MONITOR_SOCKET_FILE}" ]]; then
    monitor_socket="$(<"${OPENWRT_VM_MONITOR_SOCKET_FILE}")"
  fi

  local serial_socket="${OPENWRT_VM_SERIAL_SOCKET}"
  if [[ -f "${OPENWRT_VM_SERIAL_SOCKET_FILE}" ]]; then
    serial_socket="$(<"${OPENWRT_VM_SERIAL_SOCKET_FILE}")"
  fi

  collect_target_pids "${monitor_socket}" "${serial_socket}" "${pid}"

  if (( ${#TARGET_PIDS[@]} > 0 )); then
    printf '检测到运行中的 canonical QEMU pid: %s\n' "${TARGET_PIDS[*]}"

    if [[ -S "${monitor_socket}" ]]; then
      printf '尝试优雅关机（system_powerdown）。\n'
      monitor_cmd "${monitor_socket}" "system_powerdown" || true
    else
      printf 'monitor socket 不可用，跳过 system_powerdown。\n'
    fi

    if ! wait_all_targets_exit "${SHUTDOWN_TIMEOUT}"; then
      if [[ -S "${monitor_socket}" ]]; then
        printf '优雅关机超时，尝试 monitor quit。\n'
        monitor_cmd "${monitor_socket}" "quit" || true
      fi
    fi

    if ! wait_all_targets_exit 5; then
      printf 'monitor 阶段超时，发送 SIGTERM: %s\n' "${TARGET_PIDS[*]}"
      local pid_item
      for pid_item in "${TARGET_PIDS[@]}"; do
        kill "${pid_item}" >/dev/null 2>&1 || true
      done
    fi

    if ! wait_all_targets_exit 5; then
      printf 'SIGTERM 超时，发送 SIGKILL: %s\n' "${TARGET_PIDS[*]}"
      local pid_item
      for pid_item in "${TARGET_PIDS[@]}"; do
        kill -9 "${pid_item}" >/dev/null 2>&1 || true
      done
    fi

    if ! wait_all_targets_exit 2; then
      printf 'QEMU 仍未退出（pid=%s）。\n' "${TARGET_PIDS[*]}" >&2
      exit 5
    fi
  else
    printf '未检测到 canonical QEMU 进程，执行状态清理。\n'
  fi

  cleanup_state
  printf 'teardown 完成：VM 已停止，状态文件已清理。\n'
}

main "$@"
