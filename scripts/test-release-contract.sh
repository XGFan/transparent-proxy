#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
BUILD_SCRIPT="${SCRIPT_DIR}/build-release.sh"
MANIFEST_PATH="${REPO_ROOT}/files/managed-manifest.json"

usage() {
  cat <<EOF
用法: $(basename "$0") <version>

示例:
  $(basename "$0") v1.2.3
EOF
}

if [[ $# -ne 1 ]]; then
  usage >&2
  exit 2
fi

VERSION="$1"
CLEAN_VERSION="${VERSION#v}"
PACKAGE_NAME="transparent-proxy_${CLEAN_VERSION}_linux_arm64"

if [[ ! -x "${BUILD_SCRIPT}" ]]; then
  echo "single-artifact contract violated: build script not executable: ${BUILD_SCRIPT}" >&2
  exit 3
fi

if ! command -v file >/dev/null 2>&1; then
  echo "single-artifact contract violated: missing dependency 'file'" >&2
  exit 4
fi

TMP_DIR="$(mktemp -d)"
MANIFEST_BACKUP=""
MANIFEST_EXISTED=false
if [[ -f "${MANIFEST_PATH}" ]]; then
  MANIFEST_EXISTED=true
  MANIFEST_BACKUP="$(mktemp)"
  cp "${MANIFEST_PATH}" "${MANIFEST_BACKUP}"
fi

cleanup() {
  if [[ "${MANIFEST_EXISTED}" == true && -n "${MANIFEST_BACKUP}" && -f "${MANIFEST_BACKUP}" ]]; then
    cp "${MANIFEST_BACKUP}" "${MANIFEST_PATH}"
  fi
  if [[ "${MANIFEST_EXISTED}" != true ]]; then
    rm -f "${MANIFEST_PATH}"
  fi
  rm -f "${MANIFEST_BACKUP}"
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

echo "[release-contract] build artifacts into: ${TMP_DIR}"
bash "${BUILD_SCRIPT}" --version "${VERSION}" --skip-frontend --output-dir "${TMP_DIR}"

violations=0

shopt -s nullglob
tarballs=("${TMP_DIR}"/*.tar.gz)
shopt -u nullglob
if [[ ${#tarballs[@]} -gt 0 ]]; then
  for tb in "${tarballs[@]}"; do
    echo "single-artifact contract violated: tar.gz artifact found -> ${tb}" >&2
  done
  violations=$((violations + 1))
fi

release_tree="${TMP_DIR}/${PACKAGE_NAME}"

for forbidden_dir in "config" "files" "metadata"; do
  if [[ -d "${release_tree}/${forbidden_dir}" ]]; then
    echo "single-artifact contract violated: ${forbidden_dir}/ directory found -> ${release_tree}/${forbidden_dir}" >&2
    violations=$((violations + 1))
  fi
done

candidate_binary="${TMP_DIR}/${PACKAGE_NAME}"
if [[ ! -f "${candidate_binary}" ]]; then
  if [[ -f "${release_tree}/bin/transparent-proxy" ]]; then
    candidate_binary="${release_tree}/bin/transparent-proxy"
  else
    echo "single-artifact contract violated: expected single artifact missing -> ${TMP_DIR}/${PACKAGE_NAME}" >&2
    violations=$((violations + 1))
    candidate_binary=""
  fi
fi

if [[ -n "${candidate_binary}" && -f "${candidate_binary}" ]]; then
  file_output="$(file "${candidate_binary}")"
  if [[ ! "${file_output}" =~ ELF ]] || [[ ! "${file_output}" =~ ARM\ aarch64 ]]; then
    echo "single-artifact contract violated: artifact is not Linux/arm64 ELF -> ${file_output}" >&2
    violations=$((violations + 1))
  fi
fi

if [[ ${violations} -gt 0 ]]; then
  echo "single-artifact contract violated: ${violations} violation(s) detected" >&2
  exit 1
fi

echo "single-artifact contract satisfied"
