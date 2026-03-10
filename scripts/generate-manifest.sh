#!/usr/bin/env bash
#
# generate-manifest.sh - 自动生成 files/managed-manifest.json
#
# 用法:
#   ./scripts/generate-manifest.sh                    # 生成到 files/managed-manifest.json
#   ./scripts/generate-manifest.sh --check            # 仅校验，不修改
#   ./scripts/generate-manifest.sh --output /tmp/test.json  # 输出到指定路径
#
# 推断规则 (与 server/openwrt_manifest.go 对齐):
#   permission:
#     /etc/init.d/transparent-proxy -> 0755
#     /etc/hotplug.d/*              -> 0755
#     /etc/transparent-proxy/*.sh   -> 0755
#     其他                          -> 0644
#   requiresRestart:
#     /etc/init.d/transparent-proxy -> true
#     /etc/hotplug.d/*              -> true
#     其他                          -> false
#   requiresReload:
#     /etc/nftables.d/*             -> true
#     *.nft                         -> true
#     其他                          -> false
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
FILES_DIR="${REPO_ROOT}/files"
DEFAULT_OUTPUT="${FILES_DIR}/managed-manifest.json"
DEFAULT_SCHEMA_VERSION="v1"
DEFAULT_MANIFEST_VERSION="1.0.0"

# 默认值
OUTPUT_PATH=""
CHECK_ONLY=false
SCHEMA_VERSION="${DEFAULT_SCHEMA_VERSION}"
MANIFEST_VERSION=""

# 帮助信息
usage() {
  cat <<EOF
用法: $(basename "$0") [选项]

选项:
  -o, --output PATH        输出路径 (默认: files/managed-manifest.json)
  -c, --check              仅校验，不修改文件
  -s, --schema-version V   schema 版本 (默认: ${DEFAULT_SCHEMA_VERSION})
  -m, --manifest-version V manifest 版本 (默认: 自动生成时间戳)
  -h, --help               显示帮助信息

示例:
  $(basename "$0")                           # 生成 manifest
  $(basename "$0") --check                   # 校验现有 manifest 是否与文件一致
  $(basename "$0") -m "2.0.0" -o /tmp/m.json # 指定版本和输出路径
EOF
  exit 0
}

# 参数解析
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o|--output)
      OUTPUT_PATH="$2"
      shift 2
      ;;
    -c|--check)
      CHECK_ONLY=true
      shift
      ;;
    -s|--schema-version)
      SCHEMA_VERSION="$2"
      shift 2
      ;;
    -m|--manifest-version)
      MANIFEST_VERSION="$2"
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

# 设置默认值
: "${OUTPUT_PATH:=${DEFAULT_OUTPUT}}"

resolve_manifest_version() {
  if [[ -n "${MANIFEST_VERSION}" ]]; then
    return
  fi

  if [[ "${CHECK_ONLY}" == true && -f "${OUTPUT_PATH}" ]]; then
    local existing_manifest_version
    existing_manifest_version="$(jq -r '.manifestVersion // empty' "${OUTPUT_PATH}")"
    if [[ -n "${existing_manifest_version}" && "${existing_manifest_version}" != "null" ]]; then
      MANIFEST_VERSION="${existing_manifest_version}"
      return
    fi
  fi

  MANIFEST_VERSION="$(date -u +"%Y%m%d%H%M%S")"
}

# 检查依赖
check_dependencies() {
  local missing=()
  
  # 检查 jq
  if ! command -v jq &>/dev/null; then
    missing+=("jq")
  fi
  
  # 检查 sha256 工具 (macOS 用 shasum, Linux 用 sha256sum)
  if ! command -v shasum &>/dev/null && ! command -v sha256sum &>/dev/null; then
    missing+=("shasum 或 sha256sum")
  fi
  
  if [[ ${#missing[@]} -gt 0 ]]; then
    echo "错误: 缺少依赖工具: ${missing[*]}" >&2
    exit 2
  fi
}

# 计算文件 SHA256
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

# 推断权限
infer_permission() {
  local target="$1"
  
  # /etc/init.d/transparent-proxy
  if [[ "$target" == "/etc/init.d/transparent-proxy" ]]; then
    echo "0755"
    return
  fi
  
  # /etc/hotplug.d/*
  if [[ "$target" == /etc/hotplug.d/* ]]; then
    echo "0755"
    return
  fi
  
  # /etc/transparent-proxy/*.sh
  if [[ "$target" == /etc/transparent-proxy/*.sh ]]; then
    echo "0755"
    return
  fi
  
  # 默认
  echo "0644"
}

# 推断是否需要重启
infer_requires_restart() {
  local target="$1"
  
  # /etc/init.d/transparent-proxy
  if [[ "$target" == "/etc/init.d/transparent-proxy" ]]; then
    echo "true"
    return
  fi
  
  # /etc/hotplug.d/*
  if [[ "$target" == /etc/hotplug.d/* ]]; then
    echo "true"
    return
  fi
  
  echo "false"
}

# 推断是否需要重载
infer_requires_reload() {
  local target="$1"
  
  # /etc/nftables.d/*
  if [[ "$target" == /etc/nftables.d/* ]]; then
    echo "true"
    return
  fi
  
  # *.nft
  if [[ "$target" == *.nft ]]; then
    echo "true"
    return
  fi
  
  echo "false"
}

# 生成单个 entry
generate_entry() {
  local file="$1"
  local files_root="$2"
  
  # 计算相对路径 (去掉 files/ 前缀)
  local rel_path="${file#${files_root}/}"
  
  # source 字段包含 files/ 前缀 (与后端代码对齐)
  local source="files/${rel_path}"
  
  # 计算 target (绝对路径，不含 files/ 前缀)
  local target="/${rel_path}"
  
  # 计算 sha256
  local sha256
  sha256=$(compute_sha256 "$file")
  
  # 推断属性
  local permission
  permission=$(infer_permission "$target")
  
  local requires_restart
  requires_restart=$(infer_requires_restart "$target")
  
  local requires_reload
  requires_reload=$(infer_requires_reload "$target")
  
  # 生成 JSON entry
  jq -n \
    --arg target "$target" \
    --arg source "$source" \
    --arg sha256 "$sha256" \
    --arg permission "$permission" \
    --argjson requiresReload "$requires_reload" \
    --argjson requiresRestart "$requires_restart" \
    '{
      target: $target,
      source: $source,
      sha256: $sha256,
      permission: $permission,
      requiresReload: $requiresReload,
      requiresRestart: $requiresRestart
    }'
}

# 扫描并生成所有 entries
generate_entries() {
  local files_root="$1"
  local entries=()
  
  # 排除的文件
  local exclude_patterns=(
    "managed-manifest.json"
    ".DS_Store"
    "*.swp"
    "*.tmp"
  )
  
  # 查找所有文件
  while IFS= read -r -d '' file; do
    # 获取相对路径
    local rel_path="${file#${files_root}/}"
    
    # 检查排除模式
    local skip=false
    for pattern in "${exclude_patterns[@]}"; do
      if [[ "$rel_path" == $pattern ]]; then
        skip=true
        break
      fi
    done
    
    if [[ "$skip" == true ]]; then
      continue
    fi
    
    # 生成 entry
    local entry
    entry=$(generate_entry "$file" "$files_root")
    entries+=("$entry")
  done < <(find "$files_root" -type f ! -name "managed-manifest.json" -print0 | sort -z)
  
  # 合并所有 entries 为 JSON 数组
  if [[ ${#entries[@]} -eq 0 ]]; then
    echo "[]"
    return
  fi
  
  local entries_json
  entries_json=$(printf '%s\n' "${entries[@]}" | jq -s 'sort_by(.target)')
  echo "$entries_json"
}

# 生成完整 manifest
generate_manifest() {
  local schema_version="$1"
  local manifest_version="$2"
  local entries_json="$3"
  
  jq -n \
    --arg schemaVersion "$schema_version" \
    --arg manifestVersion "$manifest_version" \
    --argjson entries "$entries_json" \
    '{
      schemaVersion: $schemaVersion,
      manifestVersion: $manifestVersion,
      entries: $entries
    }'
}

# 主流程
main() {
  check_dependencies

  resolve_manifest_version
  
  if [[ ! -d "$FILES_DIR" ]]; then
    echo "错误: files 目录不存在: $FILES_DIR" >&2
    exit 4
  fi
  
  echo "生成 managed-manifest.json..."
  echo "  schema 版本: ${SCHEMA_VERSION}"
  echo "  manifest 版本: ${MANIFEST_VERSION}"
  echo "  输出路径: ${OUTPUT_PATH}"
  
  # 生成 entries
  local entries_json
  entries_json=$(generate_entries "$FILES_DIR")
  
  # 生成完整 manifest
  local manifest_json
  manifest_json=$(generate_manifest "$SCHEMA_VERSION" "$MANIFEST_VERSION" "$entries_json")
  
  if [[ "$CHECK_ONLY" == true ]]; then
    # 校验模式
    if [[ ! -f "$OUTPUT_PATH" ]]; then
      echo "错误: manifest 文件不存在: $OUTPUT_PATH" >&2
      exit 5
    fi
    
    local existing_manifest
    existing_manifest=$(jq -S '.' "$OUTPUT_PATH")
    
    local new_manifest
    new_manifest=$(echo "$manifest_json" | jq -S '.')
    
    if [[ "$existing_manifest" == "$new_manifest" ]]; then
      echo "✓ manifest 校验通过，无变化"
      exit 0
    else
      echo "✗ manifest 已过时，需要重新生成" >&2
      echo "--- 现有 ---" >&2
      echo "$existing_manifest" | jq '.' >&2
      echo "--- 期望 ---" >&2
      echo "$new_manifest" | jq '.' >&2
      exit 6
    fi
  fi
  
  # 原子写入
  local temp_file
  temp_file=$(mktemp "${OUTPUT_PATH}.XXXXXX")
  
  echo "$manifest_json" | jq '.' > "$temp_file"
  mv "$temp_file" "$OUTPUT_PATH"
  
  # 输出统计
  local entry_count
  entry_count=$(echo "$manifest_json" | jq '.entries | length')
  
  echo "✓ 已生成 manifest: ${OUTPUT_PATH}"
  echo "  条目数: ${entry_count}"
}

main "$@"
