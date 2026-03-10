#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

CALLER_OPENWRT_SHA256_FILE="${OPENWRT_SHA256_FILE:-}"
CALLER_OPENWRT_SHA256_ASC_FILE="${OPENWRT_SHA256_ASC_FILE:-}"
CALLER_OPENWRT_SHA256_SIG_FILE="${OPENWRT_SHA256_SIG_FILE:-}"

source "${SCRIPT_DIR}/env.sh"

IMAGE_URL="${OPENWRT_IMAGE_URL}"
SHA256_URL="${OPENWRT_SHA256_URL}"
SHA256_ASC_URL="${OPENWRT_SHA256_ASC_URL}"
SHA256_SIG_URL="${OPENWRT_SHA256_SIG_URL:-}"

IMAGE_ARCHIVE_PATH="${OPENWRT_IMAGE_ARCHIVE_PATH}"
IMAGE_PATH="${OPENWRT_IMAGE_PATH}"
SHA256_FILE="${CALLER_OPENWRT_SHA256_FILE:-${OPENWRT_SHA256_FILE}}"
SHA256_ASC_FILE="${CALLER_OPENWRT_SHA256_ASC_FILE:-${OPENWRT_SHA256_ASC_FILE}}"
SHA256_SIG_FILE="${CALLER_OPENWRT_SHA256_SIG_FILE:-${OPENWRT_SHA256_SIG_FILE:-}}"
GPG_VERIFY_LOG="${OPENWRT_GPG_VERIFY_LOG}"
FETCH_VERIFY_LOG="${OPENWRT_FETCH_VERIFY_LOG}"

IMAGE_BASENAME="$(basename "${IMAGE_ARCHIVE_PATH}")"
RAW_IMAGE_BASENAME="$(basename "${IMAGE_PATH}")"

download_file() {
  local url="$1"
  local dst="$2"
  local optional="${3:-0}"

  if curl -fL --retry 3 --retry-delay 1 --connect-timeout 10 --max-time 300 -o "${dst}.tmp" "${url}"; then
    mv -f "${dst}.tmp" "${dst}"
    return 0
  fi

  rm -f "${dst}.tmp"
  if [[ "${optional}" == "1" ]]; then
    return 1
  fi
  printf '下载失败: %s\n' "${url}" >&2
  exit 2
}

verify_gpg_signature() {
  local target_sums="$1"
  local target_asc="$2"
  local log_file="$3"

  : > "${log_file}"
  if ! gpg --verify "${target_asc}" "${target_sums}" >"${log_file}" 2>&1; then
    printf 'GPG 签名校验失败，请检查日志: %s\n' "${log_file}" >&2
    cat "${log_file}" >&2
    exit 3
  fi
}

verify_sha256() {
  local sums_file="$1"
  local archive_file="$2"
  local raw_file="$3"
  local out_log="$4"

  : > "${out_log}"
  {
    printf 'sha256 verify file: %s\n' "${sums_file}"
    printf 'target archive: %s\n' "${archive_file}"
    printf 'target raw image: %s\n' "${raw_file}"
  } >>"${out_log}"

  local line_count
  line_count="$(awk -v name="${IMAGE_BASENAME}" '{ f=$NF; sub(/^\*/, "", f); if (f==name) c++ } END { print c+0 }' "${sums_file}")"
  if [[ "${line_count}" != "1" ]]; then
    printf 'checksum 文件中 %s 记录数量异常（期望 1，实际 %s）\n' "${IMAGE_BASENAME}" "${line_count}" >&2
    exit 4
  fi

  local actual expected
  actual="$(shasum -a 256 "${archive_file}" | awk '{print $1}')"
  expected="$(awk -v name="${IMAGE_BASENAME}" '{ f=$NF; sub(/^\*/, "", f); if (f==name) print $1 }' "${sums_file}")"
  if [[ -z "${expected}" ]]; then
    printf 'checksum 文件缺少镜像条目: %s\n' "${IMAGE_BASENAME}" >&2
    exit 4
  fi

  {
    printf 'expected sha256: %s\n' "${expected}"
    printf 'actual   sha256: %s\n' "${actual}"
  } >>"${out_log}"

  if [[ "${actual}" != "${expected}" ]]; then
    printf 'SHA256 校验失败: %s\n' "${IMAGE_BASENAME}" >&2
    exit 4
  fi

  if [[ ! -s "${raw_file}" ]]; then
    printf 'raw 镜像不存在或为空: %s\n' "${raw_file}" >&2
    exit 5
  fi
}

main() {
  mkdir -p "${OPENWRT_VM_BASE_DIR}"

  if [[ ! -s "${IMAGE_ARCHIVE_PATH}" ]]; then
    download_file "${IMAGE_URL}" "${IMAGE_ARCHIVE_PATH}"
  fi

  if [[ "${SHA256_FILE}" == "${OPENWRT_SHA256_FILE}" ]]; then
    download_file "${SHA256_URL}" "${SHA256_FILE}"
  elif [[ ! -f "${SHA256_FILE}" ]]; then
    printf 'OPENWRT_SHA256_FILE 指定文件不存在: %s\n' "${SHA256_FILE}" >&2
    exit 2
  fi

  if [[ "${SHA256_ASC_FILE}" == "${OPENWRT_SHA256_ASC_FILE}" ]]; then
    download_file "${SHA256_ASC_URL}" "${SHA256_ASC_FILE}"
  elif [[ ! -f "${SHA256_ASC_FILE}" ]]; then
    printf 'OPENWRT_SHA256_ASC_FILE 指定文件不存在: %s\n' "${SHA256_ASC_FILE}" >&2
    exit 2
  fi

  if [[ -n "${SHA256_SIG_URL}" && -n "${SHA256_SIG_FILE}" ]]; then
    download_file "${SHA256_SIG_URL}" "${SHA256_SIG_FILE}" "1" || true
  fi

  verify_gpg_signature "${SHA256_FILE}" "${SHA256_ASC_FILE}" "${GPG_VERIFY_LOG}"

  if [[ ! -s "${IMAGE_PATH}" || "${IMAGE_ARCHIVE_PATH}" -nt "${IMAGE_PATH}" ]]; then
    gzip -dc "${IMAGE_ARCHIVE_PATH}" > "${IMAGE_PATH}.tmp"
    mv -f "${IMAGE_PATH}.tmp" "${IMAGE_PATH}"
  fi

  verify_sha256 "${SHA256_FILE}" "${IMAGE_ARCHIVE_PATH}" "${IMAGE_PATH}" "${FETCH_VERIFY_LOG}"

  printf '镜像校验完成: %s\n' "${IMAGE_PATH}"
  printf 'GPG 日志: %s\n' "${GPG_VERIFY_LOG}"
  printf '校验日志: %s\n' "${FETCH_VERIFY_LOG}"
}

main "$@"
