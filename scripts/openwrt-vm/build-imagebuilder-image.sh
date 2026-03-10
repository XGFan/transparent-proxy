#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

DOCKER_BIN="${DOCKER_BIN:-docker}"
SSH_KEYGEN_BIN="${SSH_KEYGEN_BIN:-ssh-keygen}"

IMAGEBUILDER_ARCHIVE="openwrt-imagebuilder-${OPENWRT_RELEASE}-armsr-armv8.Linux-x86_64.tar.zst"
IMAGEBUILDER_DIRNAME="${IMAGEBUILDER_ARCHIVE%.tar.zst}"
IMAGEBUILDER_URL="${OPENWRT_DOWNLOAD_BASE_URL}/${IMAGEBUILDER_ARCHIVE}"

IMAGEBUILDER_ROOT="${OPENWRT_VM_WORK_DIR}/imagebuilder"
IMAGEBUILDER_BASE_DIR="${IMAGEBUILDER_ROOT}/base"
IMAGEBUILDER_BUILD_DIR="${IMAGEBUILDER_ROOT}/build"
IMAGEBUILDER_STAGING_DIR="${IMAGEBUILDER_ROOT}/staging"
IMAGEBUILDER_FILES_DIR="${IMAGEBUILDER_STAGING_DIR}/files"
IMAGEBUILDER_ARCHIVE_PATH="${IMAGEBUILDER_BASE_DIR}/${IMAGEBUILDER_ARCHIVE}"

OUT_DIR="${OPENWRT_VM_WORK_DIR}/out"
OUT_IMAGE_GZ="${OUT_DIR}/openwrt-vm-ci.img.gz"
OUT_IMAGE_RAW="${OUT_DIR}/openwrt-vm-ci.img"

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

ensure_test_key() {
  mkdir -p "${OPENWRT_VM_KEYS_DIR}"

  if [[ -f "${OPENWRT_TEST_KEY_PATH}" && -f "${OPENWRT_TEST_KEY_PATH}.pub" ]]; then
    return 0
  fi

  if [[ -f "${OPENWRT_TEST_KEY_PATH}" && ! -f "${OPENWRT_TEST_KEY_PATH}.pub" ]]; then
    "${SSH_KEYGEN_BIN}" -y -f "${OPENWRT_TEST_KEY_PATH}" > "${OPENWRT_TEST_KEY_PATH}.pub"
    chmod 600 "${OPENWRT_TEST_KEY_PATH}"
    chmod 644 "${OPENWRT_TEST_KEY_PATH}.pub"
    return 0
  fi

  "${SSH_KEYGEN_BIN}" -t ed25519 -N '' -f "${OPENWRT_TEST_KEY_PATH}" >/dev/null
  chmod 600 "${OPENWRT_TEST_KEY_PATH}"
  chmod 644 "${OPENWRT_TEST_KEY_PATH}.pub"
}

prepare_staging_files() {
  rm -rf "${IMAGEBUILDER_STAGING_DIR}"

  mkdir -p \
    "${IMAGEBUILDER_FILES_DIR}/etc/dropbear" \
    "${IMAGEBUILDER_FILES_DIR}/etc/uci-defaults"

  cp "${OPENWRT_TEST_KEY_PATH}.pub" "${IMAGEBUILDER_FILES_DIR}/etc/dropbear/authorized_keys"

  cat > "${IMAGEBUILDER_FILES_DIR}/etc/uci-defaults/99-openwrt-vm-ci-network" <<'EOF'
#!/bin/sh
set -eu

changed=0
if ! ip -4 addr show dev br-lan 2>/dev/null | grep -q '10.0.2.15/'; then
  uci -q set network.lan.ipaddr='10.0.2.15'
  uci -q set network.lan.netmask='255.255.255.0'
  changed=1
fi

if [ "${changed}" -eq 1 ]; then
  uci -q commit network
  /etc/init.d/network restart >/dev/null 2>&1 || true
fi

/etc/init.d/dropbear enable >/dev/null 2>&1 || true
/etc/init.d/dropbear restart >/dev/null 2>&1 || /etc/init.d/dropbear start >/dev/null 2>&1 || true

exit 0
EOF

  chmod 755 \
    "${IMAGEBUILDER_FILES_DIR}/etc/uci-defaults/99-openwrt-vm-ci-network"

  chmod 700 "${IMAGEBUILDER_FILES_DIR}/etc/dropbear"
  chmod 600 "${IMAGEBUILDER_FILES_DIR}/etc/dropbear/authorized_keys"

  if grep -R '/mnt/ext/app/' "${IMAGEBUILDER_FILES_DIR}/etc" >/dev/null 2>&1; then
    printf 'staging 包含 legacy 路径 /mnt/ext/app/，拒绝继续构建。\n' >&2
    exit 6
  fi
}

run_imagebuilder_in_docker() {
  mkdir -p "${IMAGEBUILDER_BASE_DIR}" "${OUT_DIR}"

  "${DOCKER_BIN}" run --rm --platform linux/amd64 \
    -v "${IMAGEBUILDER_BASE_DIR}:/input/base" \
    -v "${IMAGEBUILDER_FILES_DIR}:/input/files:ro" \
    -v "${OUT_DIR}:/output" \
    -e IMAGEBUILDER_URL="${IMAGEBUILDER_URL}" \
    -e IMAGEBUILDER_ARCHIVE="${IMAGEBUILDER_ARCHIVE}" \
    -e IMAGEBUILDER_DIRNAME="${IMAGEBUILDER_DIRNAME}" \
    ubuntu:24.04 \
    bash -lc '
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive
apt-get update >/dev/null
apt-get install -y --no-install-recommends \
  bash bzip2 ca-certificates coreutils curl file findutils gawk grep gzip make patch perl \
  python-is-python3 python3 python3-setuptools sed tar unzip wget xz-utils zstd >/dev/null

for cmd in curl tar make gzip perl zstd patch awk unzip bzip2 wget python3 file; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "容器缺少命令: $cmd" >&2; exit 2; }
done

archive_path="/input/base/${IMAGEBUILDER_ARCHIVE}"
work_root="$(mktemp -d /tmp/openwrt-imagebuilder.XXXXXX)"
trap "rm -rf \"${work_root}\"" EXIT
ib_dir="${work_root}/${IMAGEBUILDER_DIRNAME}"
local_files="${work_root}/files"
local_out="${work_root}/out"

mkdir -p /input/base "${local_files}" "${local_out}" /output

if [ ! -s "${archive_path}" ]; then
  curl -fL --retry 3 --retry-delay 1 --connect-timeout 10 --max-time 600 \
    -o "${archive_path}.tmp" "${IMAGEBUILDER_URL}"
  mv -f "${archive_path}.tmp" "${archive_path}"
fi

tar --zstd -xf "${archive_path}" -C "${work_root}"
cp -a /input/files/. "${local_files}/"

cd "${ib_dir}"
make image PROFILE="generic" FILES="${local_files}" BIN_DIR="${local_out}"

built_img_gz="$(ls -1 "${local_out}"/openwrt-*-armsr-armv8-generic-ext4-combined-efi.img.gz 2>/dev/null | head -n 1 || true)"

if [ -z "${built_img_gz}" ] || [ ! -f "${built_img_gz}" ]; then
  echo "未找到 ImageBuilder 产物 .img.gz" >&2
  ls -la "${local_out}" >&2 || true
  exit 4
fi

cp "${built_img_gz}" /output/openwrt-vm-ci.img.gz
python3 - <<'PY'
import sys
import zlib

src = "/output/openwrt-vm-ci.img.gz"
dst = "/output/openwrt-vm-ci.img.tmp"

dec = zlib.decompressobj(16 + zlib.MAX_WBITS)
total = 0
trailing = 0

with open(src, "rb") as fin, open(dst, "wb") as fout:
    while True:
        chunk = fin.read(1024 * 1024)
        if not chunk:
            break

        data = dec.decompress(chunk)
        if data:
            fout.write(data)
            total += len(data)

        if dec.unused_data:
            trailing += len(dec.unused_data)
            rest = fin.read()
            trailing += len(rest)
            break

    tail = dec.flush()
    if tail:
        fout.write(tail)
        total += len(tail)

if total <= 0:
    print("gzip 解压失败：未获得有效镜像数据", file=sys.stderr)
    raise SystemExit(5)

if trailing > 0:
    print(f"warning: gzip trailing bytes ignored: {trailing}", file=sys.stderr)
PY
mv -f /output/openwrt-vm-ci.img.tmp /output/openwrt-vm-ci.img
'
}

main() {
  DOCKER_BIN="$(resolve_command "${DOCKER_BIN}")" || {
    printf '缺少 docker 可执行文件: %s\n' "${DOCKER_BIN}" >&2
    exit 2
  }

  SSH_KEYGEN_BIN="$(resolve_command "${SSH_KEYGEN_BIN}")" || {
    printf '缺少 ssh-keygen 可执行文件: %s\n' "${SSH_KEYGEN_BIN}" >&2
    exit 2
  }

  mkdir -p "${OPENWRT_VM_WORK_DIR}" "${OUT_DIR}"

  ensure_test_key
  prepare_staging_files
  run_imagebuilder_in_docker

  if [[ ! -s "${OUT_IMAGE_RAW}" ]]; then
    printf '预置镜像产物缺失: %s\n' "${OUT_IMAGE_RAW}" >&2
    exit 5
  fi

  printf 'ImageBuilder 预置镜像构建完成。\n'
  printf '镜像: %s\n' "${OUT_IMAGE_RAW}"
  printf '压缩包: %s\n' "${OUT_IMAGE_GZ}"
}

main "$@"
