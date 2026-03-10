#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

BASE_IMAGE="${OPENWRT_IMAGE_PATH}"
OVERLAY_DIR="${OPENWRT_VM_OVERLAY_DIR:-${OPENWRT_VM_RUN_DIR}/overlays}"

main() {
  if [[ ! -f "${BASE_IMAGE}" ]]; then
    printf '基础镜像不存在，请先执行 fetch-image.sh: %s\n' "${BASE_IMAGE}" >&2
    exit 2
  fi

  mkdir -p "${OVERLAY_DIR}"

  local base_abs
  base_abs="$(cd -P "$(dirname "${BASE_IMAGE}")" && pwd -P)/$(basename "${BASE_IMAGE}")"

  local ts rand overlay
  ts="$(date +%Y%m%d-%H%M%S)"
  rand="$(python3 - <<'PY'
import secrets
print(secrets.token_hex(4))
PY
)"
  overlay="${OVERLAY_DIR}/overlay-${ts}-${rand}.qcow2"

  qemu-img create -f qcow2 -F raw -b "${base_abs}" "${overlay}" >/dev/null

  printf 'overlay 创建完成: %s\n' "${overlay}"
  printf 'backing file: %s\n' "${base_abs}"
  printf '可验证命令: qemu-img info --backing-chain "%s"\n' "${overlay}"
}

main "$@"
