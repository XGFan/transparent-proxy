#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

SERIAL_BOOTSTRAP_TIMEOUT="${SERIAL_BOOTSTRAP_TIMEOUT:-240}"
SSH_READY_TIMEOUT="${SSH_READY_TIMEOUT:-180}"
SSH_PRECHECK_TIMEOUT="${SSH_PRECHECK_TIMEOUT:-90}"
SERIAL_REPAIR_INTERVAL="${SERIAL_REPAIR_INTERVAL:-20}"

serial_bootstrap() {
  local serial_socket="$1"

  python3 - "${serial_socket}" "${OPENWRT_TEST_KEY_PATH}.pub" "${SERIAL_BOOTSTRAP_TIMEOUT}" "${OPENWRT_VM_GUEST_IP}" <<'PY'
import re
import shlex
import socket
import sys
import time
from pathlib import Path

serial_socket = sys.argv[1]
pubkey_path = Path(sys.argv[2])
timeout = int(sys.argv[3])
hostfwd_guest_ip = sys.argv[4]

if not pubkey_path.is_file():
    print(f"测试公钥不存在: {pubkey_path}", file=sys.stderr)
    raise SystemExit(2)

pubkey = pubkey_path.read_text(encoding="utf-8").strip()
if not pubkey:
    print(f"测试公钥为空: {pubkey_path}", file=sys.stderr)
    raise SystemExit(2)

deadline = time.time() + timeout
sock = None
buffer = ""
next_nudge_at = 0.0
step_counter = 0

def connect_serial() -> socket.socket:
    while True:
        if time.time() >= deadline:
            print(f"无法连接串口 socket: {serial_socket}", file=sys.stderr)
            raise SystemExit(3)
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.settimeout(0.3)
        try:
            s.connect(serial_socket)
            return s
        except (FileNotFoundError, ConnectionRefusedError, OSError):
            s.close()
            time.sleep(0.5)

def reconnect() -> None:
    global sock
    if sock is not None:
        try:
            sock.close()
        except OSError:
            pass
    sock = connect_serial()

def send_line(text: str) -> None:
    payload = text.replace("\n", "\r").encode("utf-8", errors="ignore")
    try:
        sock.sendall(payload)
    except OSError:
        reconnect()
        sock.sendall(payload)

def trim_buffer() -> None:
    global buffer
    if len(buffer) > 200000:
        buffer = buffer[-200000:]

def pump_once() -> None:
    global buffer
    global next_nudge_at

    now = time.time()
    if now >= next_nudge_at:
        send_line("\r")
        next_nudge_at = now + 2.0

    try:
        chunk = sock.recv(4096)
    except socket.timeout:
        chunk = b""
    except OSError:
        reconnect()
        chunk = b""

    if chunk:
        text = chunk.decode("utf-8", errors="ignore")
        buffer += text
        trim_buffer()

    lower = buffer.lower()
    if "login:" in lower:
        send_line("root\n")
    if "password:" in lower:
        send_line("\n")

def wait_for_openwrt_prompt() -> None:
    while time.time() < deadline:
        pump_once()
        lower = buffer.lower()
        if "root@openwrt" in lower and "#" in lower:
            return
    print("串口 bootstrap 超时：未拿到稳定 shell 提示符 root@OpenWrt", file=sys.stderr)
    raise SystemExit(4)

def run_step(cmd: str, name: str) -> None:
    global step_counter
    global buffer

    step_counter += 1
    marker = f"__SERIAL_STEP_{name}_{step_counter}__"
    escaped_marker = re.escape(marker)
    send_line(f"{cmd}; rc=$?; printf '{marker}:%s\\n' \"$rc\"\n")

    while time.time() < deadline:
        pump_once()
        match = re.search(escaped_marker + r":([0-9]+)", buffer)
        if not match:
            continue
        rc = int(match.group(1))
        if rc != 0:
            print(f"串口 bootstrap 步骤失败: {name}, rc={rc}", file=sys.stderr)
            raise SystemExit(4)
        buffer = buffer[match.end():]
        wait_for_openwrt_prompt()
        return

    print(f"串口 bootstrap 超时：步骤未返回 marker: {name}", file=sys.stderr)
    raise SystemExit(4)

reconnect()
wait_for_openwrt_prompt()

pubkey_q = shlex.quote(pubkey)
run_step("mkdir -p /etc/dropbear", "mkdir")
run_step("chmod 700 /etc/dropbear", "chmod_dir")
run_step("ip link set br-lan up >/dev/null 2>&1 || true", "bringup_lan")
run_step(f"ip -4 addr show dev br-lan | grep -q '{hostfwd_guest_ip}/' || ip -4 addr add {hostfwd_guest_ip}/24 dev br-lan", "ensure_hostfwd_ip")
run_step(f"PUBKEY={pubkey_q}; printf '%s\\n' \"$PUBKEY\" > /etc/dropbear/authorized_keys", "write_key")
run_step("chmod 600 /etc/dropbear/authorized_keys", "chmod_key")
run_step("/etc/init.d/dropbear enable >/dev/null 2>&1 || true", "enable_dropbear")
run_step("/etc/init.d/dropbear restart >/dev/null 2>&1 || /etc/init.d/dropbear start >/dev/null 2>&1", "restart_dropbear")
run_step(f"PUBKEY={pubkey_q}; test -s /etc/dropbear/authorized_keys && grep -F -- \"$PUBKEY\" /etc/dropbear/authorized_keys >/dev/null", "validate_key")
run_step("ok=1; for i in $(seq 1 30); do if pidof dropbear >/dev/null 2>&1; then ok=0; break; fi; /etc/init.d/dropbear restart >/dev/null 2>&1 || /etc/init.d/dropbear start >/dev/null 2>&1 || true; sleep 1; done; [ \"$ok\" -eq 0 ]", "validate_dropbear_pid")

print("serial bootstrap finished")
PY
}

serial_repair_dropbear() {
  local serial_socket="$1"
  local repair_timeout="${2:-45}"

  python3 - "${serial_socket}" "${repair_timeout}" "${OPENWRT_VM_GUEST_IP}" <<'PY'
import re
import socket
import sys
import time

serial_socket = sys.argv[1]
timeout = int(sys.argv[2])
hostfwd_guest_ip = sys.argv[3]
deadline = time.time() + timeout
sock = None
buffer = ""
next_nudge_at = 0.0

def connect_serial() -> socket.socket:
    while True:
        if time.time() >= deadline:
            raise SystemExit(1)
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.settimeout(0.3)
        try:
            s.connect(serial_socket)
            return s
        except (FileNotFoundError, ConnectionRefusedError, OSError):
            s.close()
            time.sleep(0.2)

def reconnect() -> None:
    global sock
    if sock is not None:
        try:
            sock.close()
        except OSError:
            pass
    sock = connect_serial()

def send_line(text: str) -> None:
    payload = text.replace("\n", "\r").encode("utf-8", errors="ignore")
    try:
        sock.sendall(payload)
    except OSError:
        reconnect()
        sock.sendall(payload)

def pump_once() -> None:
    global buffer
    global next_nudge_at

    now = time.time()
    if now >= next_nudge_at:
        send_line("\r")
        next_nudge_at = now + 2.0

    try:
        chunk = sock.recv(4096)
    except socket.timeout:
        chunk = b""
    except OSError:
        reconnect()
        chunk = b""

    if chunk:
        text = chunk.decode("utf-8", errors="ignore")
        buffer += text
        if len(buffer) > 120000:
            buffer = buffer[-120000:]

    lower = buffer.lower()
    if "login:" in lower:
        send_line("root\n")
    if "password:" in lower:
        send_line("\n")

def wait_prompt() -> None:
    while time.time() < deadline:
        pump_once()
        lower = buffer.lower()
        if "root@openwrt" in lower and "#" in lower:
            return
    raise SystemExit(1)

def run_step(cmd: str, name: str) -> None:
    global buffer
    marker = f"__SERIAL_REPAIR_{name}__"
    send_line(f"{cmd}; rc=$?; printf '{marker}:%s\\n' \"$rc\"\n")
    while time.time() < deadline:
        pump_once()
        match = re.search(re.escape(marker) + r":([0-9]+)", buffer)
        if not match:
            continue
        if int(match.group(1)) != 0:
            raise SystemExit(1)
        buffer = buffer[match.end():]
        wait_prompt()
        return
    raise SystemExit(1)

reconnect()
wait_prompt()
run_step(f"ip -4 addr show dev br-lan | grep -q '{hostfwd_guest_ip}/' || ip -4 addr add {hostfwd_guest_ip}/24 dev br-lan", "hostfwd_ip")
run_step("/etc/init.d/dropbear restart >/dev/null 2>&1 || /etc/init.d/dropbear start >/dev/null 2>&1", "restart")
run_step("pidof dropbear >/dev/null 2>&1", "pid")
PY
}

ssh_wait_ready() {
  local timeout="$1"
  local serial_socket="${2:-}"
  local allow_serial_repair="${3:-1}"
  local deadline=$((SECONDS + timeout))
  local last_err=""
  local last_repair_at=0

  local ssh_cmd=(
    ssh
    -o StrictHostKeyChecking=no
    -o UserKnownHostsFile=/dev/null
    -o ConnectTimeout=5
    -o BatchMode=yes
    -i "${OPENWRT_TEST_KEY_PATH}"
    -p "${SSH_PORT}"
    "root@${QEMU_HOST}"
    "ubus call system board"
  )

  local ssh_err_file
  ssh_err_file="$(mktemp)"

  while (( SECONDS < deadline )); do
    local out
    : > "${ssh_err_file}"
    if out="$(${ssh_cmd[@]} 2>"${ssh_err_file}")"; then
      if [[ "${out}" == *"OpenWrt"* ]]; then
        rm -f "${ssh_err_file}"
        printf '%s\n' "${out}"
        return 0
      fi
    else
      last_err="$(<"${ssh_err_file}")"

      if (( allow_serial_repair == 1 )) && [[ -n "${serial_socket}" ]] && [[ "${last_err}" == *"banner exchange"* || "${last_err}" == *"Connection timed out"* || "${last_err}" == *"Connection refused"* ]]; then
        if (( SECONDS - last_repair_at >= SERIAL_REPAIR_INTERVAL )); then
          printf 'SSH 未就绪，尝试串口重启 dropbear。\n' >&2
          serial_repair_dropbear "${serial_socket}" 45 || true
          last_repair_at=${SECONDS}
        fi
      fi
    fi
    sleep 1
  done

  if [[ -n "${last_err}" ]]; then
    printf '最后一次 SSH 错误: %s\n' "${last_err}" >&2
  fi
  rm -f "${ssh_err_file}"
  return 1
}

main() {
  if [[ "${SERIAL_BOOTSTRAP_FORCE_FAIL:-0}" == "1" ]]; then
    printf 'SERIAL_BOOTSTRAP_FORCE_FAIL=1，按预期强制失败。\n' >&2
    exit 9
  fi

  if [[ ! -f "${OPENWRT_VM_PID_FILE}" ]]; then
    printf '未找到 VM pid 文件，请先执行 boot-vm.sh\n' >&2
    exit 2
  fi

  local qemu_pid
  qemu_pid="$(<"${OPENWRT_VM_PID_FILE}")"
  if [[ -z "${qemu_pid}" ]] || ! kill -0 "${qemu_pid}" >/dev/null 2>&1; then
    printf 'QEMU 进程不存在（pid=%s），请先重新启动 VM。\n' "${qemu_pid:-unknown}" >&2
    exit 2
  fi

  if [[ ! -f "${OPENWRT_TEST_KEY_PATH}" || ! -f "${OPENWRT_TEST_KEY_PATH}.pub" ]]; then
    printf '测试 SSH key 不完整：%s(.pub)\n' "${OPENWRT_TEST_KEY_PATH}" >&2
    exit 2
  fi

  printf '优先尝试 SSH/ubus 直连（timeout=%ss）...\n' "${SSH_PRECHECK_TIMEOUT}"
  if ssh_wait_ready "${SSH_PRECHECK_TIMEOUT}" "" 0; then
    printf 'SSH 直连已就绪，跳过串口 bootstrap。\n'
    printf 'serial-bootstrap=skipped\n' > "${OPENWRT_VM_BOOTSTRAP_STATE_FILE}"
    printf 'ssh-ready=ok\n' > "${OPENWRT_VM_READY_STATE_FILE}"
    printf 'VM 已就绪。\n'
    return 0
  fi
  printf 'SSH 直连未就绪，回退串口 bootstrap。\n'

  local serial_socket
  if [[ -f "${OPENWRT_VM_SERIAL_SOCKET_FILE}" ]]; then
    serial_socket="$(<"${OPENWRT_VM_SERIAL_SOCKET_FILE}")"
  else
    serial_socket="${OPENWRT_VM_SERIAL_SOCKET}"
  fi

  if [[ -z "${serial_socket}" ]]; then
    printf '串口 socket 路径为空，无法执行 bootstrap。\n' >&2
    exit 2
  fi

  printf '开始串口 bootstrap: %s\n' "${serial_socket}"
  serial_bootstrap "${serial_socket}"
  printf 'serial bootstrap 成功。\n'

  printf '等待 SSH/ubus 就绪（timeout=%ss）...\n' "${SSH_READY_TIMEOUT}"
  if ! ssh_wait_ready "${SSH_READY_TIMEOUT}" "${serial_socket}" 1; then
    printf 'SSH readiness 失败：ubus call system board 未在超时内成功。\n' >&2
    exit 5
  fi

  printf 'serial-bootstrap=ok\n' > "${OPENWRT_VM_BOOTSTRAP_STATE_FILE}"
  printf 'ssh-ready=ok\n' > "${OPENWRT_VM_READY_STATE_FILE}"
  printf 'VM 已就绪。\n'
}

main "$@"
