#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

SSH_BIN="${SSH_BIN:-ssh}"
ARTIFACTS_ROOT="${OPENWRT_VM_WORK_DIR}/artifacts"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"

mkdir -p "${ARTIFACTS_ROOT}"

ARTIFACT_DIR="${ARTIFACTS_ROOT}/${TIMESTAMP}"
if [[ -e "${ARTIFACT_DIR}" ]]; then
  suffix=1
  while [[ -e "${ARTIFACTS_ROOT}/${TIMESTAMP}-${suffix}" ]]; do
    suffix=$((suffix + 1))
  done
  ARTIFACT_DIR="${ARTIFACTS_ROOT}/${TIMESTAMP}-${suffix}"
fi

mkdir -p "${ARTIFACT_DIR}" "${ARTIFACT_DIR}/host" "${ARTIFACT_DIR}/guest" "${ARTIFACT_DIR}/guest/snapshots"

MANIFEST_FILE="${ARTIFACT_DIR}/manifest.txt"
FAIL_FILE="${ARTIFACT_DIR}/guest/failed-snapshots.txt"
touch "${MANIFEST_FILE}" "${FAIL_FILE}"

log() {
  printf '[collect-artifacts] %s\n' "$*"
}

record_manifest() {
  printf '%s\n' "$*" >> "${MANIFEST_FILE}"
}

collect_host_files() {
  log '收集 host logs/state'

  local host_logs_dir="${ARTIFACT_DIR}/host/logs"
  local host_state_dir="${ARTIFACT_DIR}/host/state"
  mkdir -p "${host_logs_dir}" "${host_state_dir}"

  if [[ -d "${OPENWRT_VM_LOG_DIR}" ]]; then
    cp -a "${OPENWRT_VM_LOG_DIR}"/. "${host_logs_dir}/"
    record_manifest "copied-dir: ${OPENWRT_VM_LOG_DIR} -> ${host_logs_dir}"
  else
    record_manifest "missing-dir: ${OPENWRT_VM_LOG_DIR}"
  fi

  if [[ -d "${OPENWRT_VM_STATE_DIR}" ]]; then
    local item
    for item in "${OPENWRT_VM_STATE_DIR}"/*; do
      [[ -e "${item}" ]] || continue
      if [[ -S "${item}" ]]; then
        record_manifest "skip-socket: ${item}"
        continue
      fi
      cp -a "${item}" "${host_state_dir}/"
      record_manifest "copied-state-item: ${item}"
    done
  else
    record_manifest "missing-dir: ${OPENWRT_VM_STATE_DIR}"
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

SSH_BIN="$(resolve_command "${SSH_BIN}")"

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

FAILED_COUNT=0

collect_guest_snapshot() {
  local name="$1"
  local cmd="$2"
  local out_file="${ARTIFACT_DIR}/guest/snapshots/${name}.txt"
  local err_file="${ARTIFACT_DIR}/guest/snapshots/${name}.stderr.txt"

  if "${SSH_BASE[@]}" "${cmd}" >"${out_file}" 2>"${err_file}"; then
    record_manifest "guest-snapshot-ok: ${name}"
    rm -f "${err_file}"
  else
    FAILED_COUNT=$((FAILED_COUNT + 1))
    record_manifest "guest-snapshot-failed: ${name}"
    printf '%s\n' "${name}" >> "${FAIL_FILE}"
  fi
}

collect_guest_snapshots() {
  log '收集 guest snapshots'

  collect_guest_snapshot 'ubus-system-board' 'ubus call system board'
  collect_guest_snapshot 'ip-rule-show' 'ip rule show'
  collect_guest_snapshot 'ip-route-table-100' 'ip route show table 100'
  collect_guest_snapshot 'transparent-proxy-config' 'cat /etc/transparent-proxy/config.yaml'
  collect_guest_snapshot 'transparent-proxy-status' '(/etc/init.d/transparent-proxy status || true)'
}

main() {
  record_manifest "artifact-dir: ${ARTIFACT_DIR}"
  record_manifest "created-at: $(date -u +%Y-%m-%dT%H:%M:%SZ)"

  collect_host_files
  collect_guest_snapshots

  if (( FAILED_COUNT > 0 )); then
    log "guest snapshots 存在失败项（${FAILED_COUNT}），详见 ${FAIL_FILE}"
    log "artifact 输出目录: ${ARTIFACT_DIR}"
    exit 1
  fi

  log "收集完成: ${ARTIFACT_DIR}"
}

main "$@"
