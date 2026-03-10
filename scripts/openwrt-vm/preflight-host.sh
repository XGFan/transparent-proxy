#!/usr/bin/env bash

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

ROOT_DIR="${OPENWRT_VM_REPO_ROOT}"
WORK_DIR="${OPENWRT_VM_WORK_DIR}"
IMAGE_URL="${OPENWRT_IMAGE_URL}"

BLOCKERS=()
WARNINGS=()
INFOS=()

add_blocker() {
  BLOCKERS+=("$1")
}

add_warning() {
  WARNINGS+=("$1")
}

add_info() {
  INFOS+=("$1")
}

print_kv() {
  printf '%-28s %s\n' "$1" "$2"
}

check_cmd() {
  local cmd="$1"
  local hint="$2"
  if command -v "$cmd" >/dev/null 2>&1; then
    add_info "✅ 命令可用: ${cmd} -> $(command -v "$cmd")"
  else
    add_blocker "缺少命令 ${cmd}。${hint}"
  fi
}

check_port_free() {
  local port="$1"
  if python3 - <<PY
import socket
port=${port}
s=socket.socket()
try:
    s.bind(('127.0.0.1', port))
except OSError:
    raise SystemExit(1)
finally:
    s.close()
PY
  then
    add_info "✅ 端口可用: 127.0.0.1:${port}"
  else
    add_blocker "端口被占用: 127.0.0.1:${port}。请先释放该端口（或统一改基线端口并全链路同步）。"
  fi
}

check_efi_firmware() {
  local candidates=(
    "${QEMU_EFI_CODE:-}"
    "/opt/homebrew/share/qemu/edk2-aarch64-code.fd"
    "/opt/homebrew/share/qemu/edk2-arm-code.fd"
    "/usr/local/share/qemu/edk2-aarch64-code.fd"
    "/usr/local/share/qemu/edk2-arm-code.fd"
    "/usr/share/qemu-efi-aarch64/QEMU_EFI.fd"
  )

  local found=""
  local path
  for path in "${candidates[@]}"; do
    if [[ -n "$path" && -f "$path" ]]; then
      found="$path"
      break
    fi
  done

  if [[ -n "$found" ]]; then
    add_info "✅ 找到 AArch64 UEFI 固件: ${found}"
  else
    add_blocker "未找到 AArch64 UEFI 固件（edk2）。请安装 qemu/edk2，或设置 QEMU_EFI_CODE 指向有效 firmware 文件。"
  fi
}

check_hvf_support() {
  if ! command -v qemu-system-aarch64 >/dev/null 2>&1; then
    return
  fi

  if qemu-system-aarch64 -accel help 2>/dev/null | grep -q 'hvf'; then
    add_info "✅ QEMU 支持 hvf 加速"
  else
    add_blocker "qemu-system-aarch64 未显示 hvf 加速支持。请确认在 macOS 上安装了支持 hvf 的 QEMU 构建。"
  fi
}

check_download_access() {
  if curl -fsSLI --max-time 12 "$IMAGE_URL" >/dev/null 2>&1; then
    add_info "✅ 可访问 OpenWrt 镜像地址"
  else
    add_blocker "无法访问 OpenWrt 镜像地址：${IMAGE_URL}。请提前处理网络/VPN/代理/防火墙策略。"
  fi
}

check_disk_space() {
  local available_kb
  available_kb="$(df -Pk "$ROOT_DIR" | awk 'NR==2 {print $4}')"
  if [[ -z "$available_kb" ]]; then
    add_warning "无法读取磁盘可用空间，建议手动确认 >= 8GB。"
    return
  fi

  local min_kb=$((8 * 1024 * 1024))
  if (( available_kb < min_kb )); then
    add_blocker "磁盘空间不足（当前 ${available_kb} KB，可用要求 >= ${min_kb} KB）。请先清理空间。"
  else
    add_info "✅ 磁盘空间充足（可用 ${available_kb} KB）"
  fi
}

check_workspace_writable() {
  mkdir -p "$WORK_DIR" 2>/dev/null || true
  local probe="${WORK_DIR}/.preflight-write-test"
  if : > "$probe" 2>/dev/null; then
    rm -f "$probe"
    add_info "✅ 工作目录可写: ${WORK_DIR}"
  else
    add_blocker "工作目录不可写: ${WORK_DIR}。请调整权限。"
  fi
}

check_os_arch() {
  local os arch
  os="$(uname -s)"
  arch="$(uname -m)"
  print_kv "OS" "$os"
  print_kv "ARCH" "$arch"

  if [[ "$os" != "Darwin" ]]; then
    add_warning "当前不是 macOS（Darwin）。该 preflight 以 macOS + Apple Silicon 基线设计。"
  fi
  if [[ "$arch" != "arm64" ]]; then
    add_warning "当前不是 arm64。可运行但不属于本计划默认基线。"
  fi
}

check_ssh_key_material() {
  local priv="${OPENWRT_TEST_KEY_PATH}"
  local pub="${priv}.pub"

  if [[ -f "$priv" && -f "$pub" ]]; then
    add_info "✅ 测试 SSH key 已存在: ${priv}"
  else
    add_warning "未检测到测试 SSH key（${priv} / ${pub}）。后续可由自动化脚本生成；若你想先手动准备：ssh-keygen -t ed25519 -N '' -f '${priv}'"
  fi
}

print_section() {
  local title="$1"
  printf '\n=== %s ===\n' "$title"
}

main() {
  print_section "OpenWrt VM Preflight (Host)"
  print_kv "Repo" "$ROOT_DIR"
  print_kv "Release" "$OPENWRT_RELEASE"
  print_kv "Target" "$OPENWRT_TARGET"
  print_kv "Image" "$OPENWRT_IMAGE"
  print_kv "Image URL" "$IMAGE_URL"
  print_kv "QEMU Net" "$QEMU_NET"
  print_kv "SSH Port" "$SSH_PORT"
  print_kv "API Port" "$API_PORT"
  print_kv "Work Dir" "$WORK_DIR"

  check_os_arch
  check_cmd "bash" "macOS 默认自带；若不可用请先修复 shell 环境。"
  check_cmd "python3" "建议安装 Python 3（例如 brew install python）。"
  check_cmd "curl" "建议安装 curl。"
  check_cmd "gzip" "建议安装 gzip。"
  check_cmd "ssh" "建议安装 openssh。"
  check_cmd "ssh-keygen" "建议安装 openssh。"
  check_cmd "scp" "建议安装 openssh。"
  check_cmd "jq" "建议安装 jq（brew install jq）。"
  check_cmd "gpg" "建议安装 gnupg（brew install gnupg），用于签名校验。"
  check_cmd "qemu-system-aarch64" "建议安装 qemu（brew install qemu）。"
  check_cmd "qemu-img" "建议安装 qemu（brew install qemu）。"

  check_hvf_support
  check_efi_firmware
  check_workspace_writable
  check_disk_space
  check_port_free "$SSH_PORT"
  check_port_free "$API_PORT"
  check_download_access
  check_ssh_key_material

  print_section "检查结果"
  local item
  if (( ${#INFOS[@]} > 0 )); then
    for item in "${INFOS[@]}"; do
      printf '%s\n' "$item"
    done
  fi

  if (( ${#WARNINGS[@]} > 0 )); then
    print_section "警告（建议提前处理）"
    for item in "${WARNINGS[@]}"; do
      printf -- '- %s\n' "$item"
    done
  fi

  if (( ${#BLOCKERS[@]} > 0 )); then
    print_section "阻塞项（必须先处理）"
    for item in "${BLOCKERS[@]}"; do
      printf -- '- %s\n' "$item"
    done
    print_section "结论"
    printf '❌ PRECHECK FAILED：存在 %d 个阻塞项。请先处理后再开始执行自动化任务。\n' "${#BLOCKERS[@]}"
    exit 2
  fi

  print_section "结论"
  printf '✅ PRECHECK PASSED：无阻塞项，可进入自动化实施阶段。\n'
}

main "$@"
