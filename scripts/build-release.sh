#!/usr/bin/env bash
#
# build-release.sh - 构建 OpenWrt ARM64 发布包
#
# 用法:
#   ./scripts/build-release.sh                    # 交互式构建
#   ./scripts/build-release.sh --version v1.0.0   # 指定版本
#   ./scripts/build-release.sh --upx              # 启用 UPX 压缩
#   ./scripts/build-release.sh --skip-frontend    # 跳过前端构建（调试用）
#
# 输出:
#   dist/transparent-proxy_${version}_linux_arm64
#   dist/transparent-proxy_${version}_linux_arm64.sha256
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
DIST_DIR="${REPO_ROOT}/dist"

# 默认值
VERSION=""
ENABLE_UPX=false
SKIP_FRONTEND=false
OUTPUT_DIR="${DIST_DIR}"

# 帮助信息
usage() {
  cat <<EOF
用法: $(basename "$0") [选项]

选项:
  -v, --version VERSION    版本号 (如 v1.0.0)，必填或从 git 推断
  -u, --upx                启用 UPX 压缩
  -s, --skip-frontend      跳过前端构建（调试用）
  -o, --output-dir DIR     输出目录 (默认: dist/)
  -h, --help               显示帮助信息

示例:
  $(basename "$0") -v v1.0.0                 # 构建版本 v1.0.0
  $(basename "$0") -v v1.0.0 --upx           # 构建 + UPX 压缩
  $(basename "$0") --version v2.0.0 -o /tmp  # 指定输出目录
EOF
  exit 0
}

# 参数解析
while [[ $# -gt 0 ]]; do
  case "$1" in
    -v|--version)
      VERSION="$2"
      shift 2
      ;;
    -u|--upx)
      ENABLE_UPX=true
      shift
      ;;
    -s|--skip-frontend)
      SKIP_FRONTEND=true
      shift
      ;;
    -o|--output-dir)
      OUTPUT_DIR="$2"
      shift 2
      ;;
    -h|--help)
      usage
      ;;
    *)
      echo "错误: 未知选项 '$1'" >&2
      exit 1
      ;;
  esac
done

# 检查依赖
check_dependencies() {
  local missing=()
  
  command -v go &>/dev/null || missing+=("go")
  command -v npm &>/dev/null || missing+=("npm")
  command -v jq &>/dev/null || missing+=("jq")
  
  if [[ "$ENABLE_UPX" == true ]]; then
    command -v upx &>/dev/null || missing+=("upx")
  fi
  
  if [[ ${#missing[@]} -gt 0 ]]; then
    echo "错误: 缺少依赖工具: ${missing[*]}" >&2
    exit 2
  fi
}

# 推断版本号
infer_version() {
  if [[ -d "${REPO_ROOT}/.git" ]] && command -v git &>/dev/null; then
    git describe --tags --always --dirty 2>/dev/null || echo "v0.0.0-dev"
  else
    echo "v0.0.0-$(date +%Y%m%d%H%M%S)"
  fi
}

# 验证版本号格式
validate_version() {
  local version="$1"
  if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+ ]]; then
    echo "警告: 版本号 '$version' 不符合语义化版本格式 (vX.Y.Z)" >&2
  fi
}

# 计算 SHA256
compute_sha256() {
  local file="$1"
  
  if command -v shasum &>/dev/null; then
    shasum -a 256 "$file" | cut -d' ' -f1
  elif command -v sha256sum &>/dev/null; then
    sha256sum "$file" | cut -d' ' -f1
  else
    echo "错误: 无法计算 SHA256" >&2
    exit 3
  fi
}

# 生成 manifest
generate_manifest() {
  echo "生成 managed-manifest.json..."
  
  if [[ ! -x "${SCRIPT_DIR}/generate-manifest.sh" ]]; then
    echo "错误: generate-manifest.sh 不可执行" >&2
    exit 4
  fi
  
  "${SCRIPT_DIR}/generate-manifest.sh" -m "${VERSION#v}"
}

# 构建前端
build_frontend() {
  echo "构建前端..."
  
  cd "${REPO_ROOT}/portal"
  
  if [[ ! -f "package.json" ]]; then
    echo "错误: portal/package.json 不存在" >&2
    exit 5
  fi
  
  npm ci --quiet
  npm run build
  
  # 验证输出
  if [[ ! -f "../server/web/index.html" ]]; then
    echo "错误: 前端构建输出缺失: server/web/index.html" >&2
    exit 6
  fi
  
  if [[ ! -d "../server/web/assets" ]] || [[ -z "$(ls -A ../server/web/assets 2>/dev/null)" ]]; then
    echo "错误: 前端构建输出缺失: server/web/assets" >&2
    exit 7
  fi
  
  echo "✓ 前端构建完成"
}

# 构建后端
build_backend() {
  local output="$1"
  
  echo "构建后端 (linux/arm64)..."
  
  cd "${REPO_ROOT}/server"
  
  if [[ ! -f "main.go" ]]; then
    echo "错误: server/main.go 不存在" >&2
    exit 8
  fi
  
  local ldflags="-s -w"
  
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags "$ldflags" -o "$output" .
  
  echo "✓ 后端构建完成: $output"
}

# UPX 压缩
compress_binary() {
  local binary="$1"
  
  if [[ "$ENABLE_UPX" != true ]]; then
    return 0
  fi
  
  echo "UPX 压缩..."
  
  if ! command -v upx &>/dev/null; then
    echo "错误: UPX 未安装" >&2
    exit 9
  fi
  
  upx --best --lzma "$binary"
  
  echo "✓ UPX 压缩完成"
}

# 主流程
main() {
  check_dependencies
  
  # 推断或验证版本号
  if [[ -z "$VERSION" ]]; then
    VERSION=$(infer_version)
    echo "自动推断版本号: ${VERSION}"
  fi
  
  validate_version "$VERSION"
  
  # 清理版本号前缀
  local clean_version="${VERSION#v}"
  local package_name="transparent-proxy_${clean_version}_linux_arm64"
  local output_binary="${OUTPUT_DIR}/${package_name}"
  local output_sha256="${output_binary}.sha256"
  local legacy_archive="${OUTPUT_DIR}/${package_name}.tar.gz"
   
  echo "=========================================="
  echo "构建发布产物: ${package_name}"
  echo "版本: ${VERSION}"
  echo "UPX: ${ENABLE_UPX}"
  echo "跳过前端: ${SKIP_FRONTEND}"
  echo "输出目录: ${OUTPUT_DIR}"
  echo "=========================================="
  
  # 创建输出目录
  mkdir -p "$OUTPUT_DIR"
  
  rm -rf "$output_binary"
  rm -f "$output_sha256" "$legacy_archive" "${legacy_archive}.sha256"
  
  # 1. 生成 manifest
  generate_manifest
  
  # 2. 构建前端
  if [[ "$SKIP_FRONTEND" != true ]]; then
    build_frontend
  else
    echo "⚠ 跳过前端构建"
  fi
  
  # 3. 构建后端
  build_backend "$output_binary"
  
  # 4. UPX 压缩
  compress_binary "$output_binary"
  
  local sha256
  sha256=$(compute_sha256 "$output_binary")
  echo "$sha256  $(basename "$output_binary")" > "$output_sha256"
  
  local binary_size
  binary_size=$(stat -f%z "$output_binary" 2>/dev/null || stat -c%s "$output_binary")
   
  echo ""
  echo "=========================================="
  echo "构建完成!"
  echo "=========================================="
  echo "版本: ${VERSION}"
  echo "二进制大小: $(( binary_size / 1024 )) KB"
  echo ""
  echo "产物:"
  echo "  ${output_binary}"
  echo "  ${output_sha256}"
  echo ""
  echo "SHA256: ${sha256}"
}

main "$@"
