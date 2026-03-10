#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/env.sh"

CURL_BIN="${CURL_BIN:-curl}"
SSH_BIN="${SSH_BIN:-ssh}"
PYTHON_BIN="${PYTHON_BIN:-python3}"

FORCE_NEGATIVE=0
PROTECTED_SUBPATH="${LUCI_PROBE_SUBPATH:-/cgi-bin/luci/admin/services/transparent-proxy}"
UNKNOWN_PROBE_SUBPATH="${LUCI_PROBE_UNKNOWN_SUBPATH:-/cgi-bin/luci/admin/services/__tp_probe_nonexistent__}"
LUCI_LOGIN_USERNAME="${LUCI_PROBE_USERNAME:-root}"
LUCI_LOGIN_PASSWORD="${LUCI_PROBE_PASSWORD:-}"

EXIT_CODE_SUPPORTED=0
EXIT_CODE_UNSUPPORTED=20
EXIT_CODE_FAILURE=1

EVIDENCE_DIR="${OPENWRT_VM_REPO_ROOT}/.sisyphus/evidence"
PROBE_ENV_FILE="${OPENWRT_VM_WORK_DIR}/luci-probe.env"
PROBE_SUMMARY_FILE="${OPENWRT_VM_WORK_DIR}/luci-probe-summary.txt"
PROBE_LOG_FILE="${OPENWRT_VM_LOG_DIR}/luci-proxy-probe-$(date +%Y%m%d-%H%M%S).log"
PROBE_EVIDENCE_FILE="${EVIDENCE_DIR}/task-1-luci-proxy-probe.txt"
PROBE_ERROR_EVIDENCE_FILE="${EVIDENCE_DIR}/task-1-luci-proxy-probe-error.txt"

SSH_BASE=()
TMP_DIR=""
TUNNEL_PID=""
LUCI_TUNNEL_PORT=""

BACKEND_ASSET_PATH=""
UHTTPD_PROXY_HINT=0

LUCI_LOGIN_URL=""
LUCI_LOGIN_STATUS=""
LUCI_LOGIN_LOCATION=""
LUCI_LOGIN_REQUIRED_HEADER=""
LUCI_AUTH_COOKIE=""

AUTH_BASELINE_URL=""
AUTH_BASELINE_STATUS=""
AUTH_BASELINE_LOCATION=""

UNKNOWN_URL=""
UNKNOWN_STATUS=""
UNKNOWN_LOCATION=""

HTML_URL=""
HTML_STATUS=""
HTML_LOCATION=""
HTML_LOGIN_REQUIRED_HEADER=""
STATIC_URL=""
STATIC_STATUS=""
STATIC_LOCATION=""
STATIC_CONTENT_TYPE=""
API_URL=""
API_STATUS=""
API_LOCATION=""
API_CONTENT_TYPE=""

SAME_ORIGIN_SUPPORTED=0
TASK9_FALLBACK_REQUIRED=1
TASK9_FALLBACK_MARKER="TASK9_FALLBACK_BRANCH_REQUIRED"

declare -a FAILURE_REASONS=()

log() {
  printf '[luci-probe] %s\n' "$*"
}

fail() {
  printf '[luci-probe][ERROR] %s\n' "$*" >&2
  exit "${EXIT_CODE_FAILURE}"
}

usage() {
  cat <<'EOF'
Usage: bash scripts/openwrt-vm/test-luci-proxy-probe.sh [--force-negative]

Options:
  --force-negative   Force SAME_ORIGIN_SUPPORTED=0 and fallback marker
  -h, --help         Show this help

Exit code contract:
  0   SAME_ORIGIN_SUPPORTED=1（明确支持）
  20  SAME_ORIGIN_SUPPORTED=0（探针完成但判定不支持/不可验证）
  1   脚本执行失败（工具缺失、命令异常、未完成探针）
EOF
}

cleanup() {
  local exit_code=$?

  if [[ -n "${TUNNEL_PID}" ]] && kill -0 "${TUNNEL_PID}" >/dev/null 2>&1; then
    kill "${TUNNEL_PID}" >/dev/null 2>&1 || true
    wait "${TUNNEL_PID}" >/dev/null 2>&1 || true
  fi

  if [[ -n "${TMP_DIR}" && -d "${TMP_DIR}" ]]; then
    rm -rf "${TMP_DIR}"
  fi

  trap - EXIT
  exit "${exit_code}"
}
trap cleanup EXIT

add_failure_reason() {
  FAILURE_REASONS+=("$1")
  log "记录失败原因: $1"
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

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --force-negative)
        FORCE_NEGATIVE=1
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        usage
        fail "未知参数: $1"
        ;;
    esac
    shift
  done
}

prepare_directories() {
  mkdir -p "${OPENWRT_VM_WORK_DIR}" "${OPENWRT_VM_RUN_DIR}" "${OPENWRT_VM_LOG_DIR}" "${EVIDENCE_DIR}"
  TMP_DIR="$(mktemp -d "${OPENWRT_VM_RUN_DIR}/luci-probe.XXXXXX")"
}

prepare_tools() {
  CURL_BIN="$(resolve_command "${CURL_BIN}")" || fail "缺少 curl 可执行文件: ${CURL_BIN}"
  SSH_BIN="$(resolve_command "${SSH_BIN}")" || fail "缺少 ssh 可执行文件: ${SSH_BIN}"
  PYTHON_BIN="$(resolve_command "${PYTHON_BIN}")" || fail "缺少 python3 可执行文件: ${PYTHON_BIN}"

  [[ -f "${OPENWRT_TEST_KEY_PATH}" ]] || fail "测试 SSH 私钥不存在: ${OPENWRT_TEST_KEY_PATH}"

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
}

run_ssh() {
  "${SSH_BASE[@]}" "$@"
}

http_probe() {
  local url="$1"
  local body_file="$2"
  local header_file="$3"
  shift 3
  local code

  set +e
  code="$(${CURL_BIN} -sS -o "${body_file}" -D "${header_file}" -w '%{http_code}' --connect-timeout 5 --max-time 20 "$@" "${url}")"
  local curl_rc=$?
  set -e

  if (( curl_rc != 0 )); then
    printf 'curl-exit-%s' "${curl_rc}"
    return 0
  fi

  if [[ -z "${code}" ]]; then
    printf 'curl-empty-status'
    return 0
  fi

  printf '%s' "${code}"
}

extract_header_value() {
  local header_file="$1"
  local header_name="$2"

  "${PYTHON_BIN}" - "${header_file}" "${header_name}" <<'PY'
import pathlib
import sys

header_path = pathlib.Path(sys.argv[1])
header_name = sys.argv[2].lower().strip()
value = ""

for line in header_path.read_text(encoding="utf-8", errors="ignore").splitlines():
    if not line:
        continue
    lower = line.lower()
    if lower.startswith(header_name + ":"):
        value = line.split(":", 1)[1].strip()

print(value)
PY
}

extract_auth_cookie() {
  local header_file="$1"

  "${PYTHON_BIN}" - "${header_file}" <<'PY'
import pathlib
import sys

header_path = pathlib.Path(sys.argv[1])
candidate = ""

for line in header_path.read_text(encoding="utf-8", errors="ignore").splitlines():
    if not line.lower().startswith("set-cookie:"):
        continue
    cookie_kv = line.split(":", 1)[1].strip().split(";", 1)[0].strip()
    if cookie_kv.startswith("sysauth_http="):
        print(cookie_kv)
        raise SystemExit(0)
    if cookie_kv.startswith("sysauth=") and not candidate:
        candidate = cookie_kv

print(candidate)
PY
}

body_has_html_markers() {
  local body_file="$1"

  "${PYTHON_BIN}" - "${body_file}" <<'PY'
import pathlib
import re
import sys

content = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8", errors="ignore")
head = content[:4096].lstrip().lower()

html_markers = (
    "<!doctype html",
    "<html",
    "<head",
    "<body",
    "<title",
    "data-page=\"admin-",
)

if any(marker in head for marker in html_markers):
    raise SystemExit(0)

if re.search(r"<html[\\s>]", head):
    raise SystemExit(0)

raise SystemExit(1)
PY
}

asset_body_semantic_error() {
  local body_file="$1"
  local asset_url="$2"

  "${PYTHON_BIN}" - "${body_file}" "${asset_url}" <<'PY'
from urllib.parse import urlparse
import pathlib
import re
import sys

content = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8", errors="ignore")
asset_url = sys.argv[2]
parsed = urlparse(asset_url)
path = parsed.path.lower()

if not content.strip():
    print("响应体为空")
    raise SystemExit(0)

if path.endswith('.css'):
    if '{' not in content or '}' not in content:
        print('CSS 资源缺少样式块标记')
        raise SystemExit(0)
else:
    if len(content.strip()) < 20:
        print('JS 资源响应体过短，疑似非真实构建产物')
        raise SystemExit(0)
    if not re.search(r'[;{}()=]', content):
        print('JS 资源缺少常见脚本语法标记')
        raise SystemExit(0)

print('')
PY
}

api_body_semantic_error() {
  local body_file="$1"

  "${PYTHON_BIN}" - "${body_file}" <<'PY'
import json
import pathlib
import sys

content = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8", errors="ignore").strip()

if not content:
    print('API 响应体为空')
    raise SystemExit(0)

try:
    payload = json.loads(content)
except json.JSONDecodeError as err:
    print(f'API 响应不是合法 JSON: {err.msg}')
    raise SystemExit(0)

if not isinstance(payload, dict):
    print('API JSON 根节点不是对象')
    raise SystemExit(0)

if not any(key in payload for key in ('code', 'status', 'data', 'message')):
    print('API JSON 缺少预期 envelope 字段（code/status/data/message）')
    raise SystemExit(0)

print('')
PY
}

capture_backend_asset_path() {
  local backend_index="${TMP_DIR}/backend-index.html"
  local backend_headers="${TMP_DIR}/backend-index.headers"
  local backend_code

  backend_code="$(http_probe "${TP_API_BASE_URL}/" "${backend_index}" "${backend_headers}")"
  if [[ "${backend_code}" != "200" ]]; then
    add_failure_reason "后端 HTML shell 不可达: url=${TP_API_BASE_URL}/ status=${backend_code}"
    return
  fi

  BACKEND_ASSET_PATH="$("${PYTHON_BIN}" - "${backend_index}" <<'PY'
import pathlib
import re
import sys

content = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8", errors="ignore")
match = re.search(r'["\']((?:\./|/)?assets/[^"\']+\.(?:js|css)(?:\?[^"\']*)?)["\']', content)
print(match.group(1) if match else "")
PY
)"

  if [[ -z "${BACKEND_ASSET_PATH}" ]]; then
    add_failure_reason '后端首页未发现 ./assets/* 或 /assets/* 静态资源引用'
  else
    log "解析到后端静态资源路径: ${BACKEND_ASSET_PATH}"
  fi
}

resolve_static_url_from_html() {
  local html_url="$1"
  local asset_ref="$2"

  "${PYTHON_BIN}" - "${html_url}" "${asset_ref}" <<'PY'
from urllib.parse import urljoin
import sys

print(urljoin(sys.argv[1], sys.argv[2]))
PY
}

start_luci_tunnel() {
  LUCI_TUNNEL_PORT="$(${PYTHON_BIN} - <<'PY'
import socket

sock = socket.socket()
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()
PY
)"

  "${SSH_BASE[@]}" -N -L "127.0.0.1:${LUCI_TUNNEL_PORT}:127.0.0.1:80" >/dev/null 2>&1 &
  TUNNEL_PID="$!"

  local tunnel_url="http://127.0.0.1:${LUCI_TUNNEL_PORT}/"
  local attempt
  for attempt in $(seq 1 20); do
    if ! kill -0 "${TUNNEL_PID}" >/dev/null 2>&1; then
      break
    fi

    if "${CURL_BIN}" -sS -o /dev/null --connect-timeout 1 --max-time 2 "${tunnel_url}" >/dev/null 2>&1; then
      log "LuCI/uhttpd tunnel ready: ${tunnel_url}"
      return 0
    fi

    sleep 1
  done

  add_failure_reason "无法建立 LuCI/uhttpd 本地 tunnel: ${tunnel_url}"
}

probe_platform_capability() {
  local hint
  hint="$(run_ssh "uhttpd -h 2>&1 | grep -qi proxy && printf 1 || printf 0" || printf 0)"
  if [[ "${hint}" == "1" ]]; then
    UHTTPD_PROXY_HINT=1
  fi

  log 'guest uhttpd/luci 版本信息（证据）:'
  run_ssh "opkg list-installed | grep -E '^(uhttpd|luci)' || true"
}

probe_luci_authentication_and_baseline() {
  local base_url="http://127.0.0.1:${LUCI_TUNNEL_PORT}"
  LUCI_LOGIN_URL="${base_url}/cgi-bin/luci"

  local login_body="${TMP_DIR}/luci-login.body"
  local login_header="${TMP_DIR}/luci-login.headers"

  LUCI_LOGIN_STATUS="$(http_probe "${LUCI_LOGIN_URL}" "${login_body}" "${login_header}" \
    -X POST \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    --data-urlencode "luci_username=${LUCI_LOGIN_USERNAME}" \
    --data-urlencode "luci_password=${LUCI_LOGIN_PASSWORD}")"
  LUCI_LOGIN_LOCATION="$(extract_header_value "${login_header}" 'location')"
  LUCI_LOGIN_REQUIRED_HEADER="$(extract_header_value "${login_header}" 'x-luci-login-required')"
  LUCI_AUTH_COOKIE="$(extract_auth_cookie "${login_header}")"

  log "LuCI 登录探测: status=${LUCI_LOGIN_STATUS} location=${LUCI_LOGIN_LOCATION:-<none>} cookie=${LUCI_AUTH_COOKIE:-<none>}"

  case "${LUCI_LOGIN_STATUS}" in
    302|303|200)
      ;;
    *)
      add_failure_reason "LuCI 登录请求异常: status=${LUCI_LOGIN_STATUS}"
      ;;
  esac

  if [[ -z "${LUCI_AUTH_COOKIE}" ]]; then
    add_failure_reason '无法建立 LuCI 登录态（缺少 sysauth_http/sysauth cookie）'
  fi

  AUTH_BASELINE_URL="${base_url}/cgi-bin/luci/admin/status/overview"
  local baseline_body="${TMP_DIR}/luci-baseline.body"
  local baseline_header="${TMP_DIR}/luci-baseline.headers"

  if [[ -n "${LUCI_AUTH_COOKIE}" ]]; then
    AUTH_BASELINE_STATUS="$(http_probe "${AUTH_BASELINE_URL}" "${baseline_body}" "${baseline_header}" -H "Cookie: ${LUCI_AUTH_COOKIE}")"
  else
    AUTH_BASELINE_STATUS="$(http_probe "${AUTH_BASELINE_URL}" "${baseline_body}" "${baseline_header}")"
  fi
  AUTH_BASELINE_LOCATION="$(extract_header_value "${baseline_header}" 'location')"

  log "LuCI 登录态基线: url=${AUTH_BASELINE_URL} status=${AUTH_BASELINE_STATUS}"

  if [[ "${AUTH_BASELINE_STATUS}" != '200' ]]; then
    add_failure_reason "无法通过登录态访问 LuCI 基线页面: status=${AUTH_BASELINE_STATUS}"
  fi
}

probe_same_origin_routes() {
  local base_url="http://127.0.0.1:${LUCI_TUNNEL_PORT}"
  local html_body="${TMP_DIR}/same-origin-html.body"
  local html_header="${TMP_DIR}/same-origin-html.headers"
  local static_body="${TMP_DIR}/same-origin-static.body"
  local static_header="${TMP_DIR}/same-origin-static.headers"
  local api_body="${TMP_DIR}/same-origin-api.body"
  local api_header="${TMP_DIR}/same-origin-api.headers"
  local unknown_body="${TMP_DIR}/same-origin-unknown.body"
  local unknown_header="${TMP_DIR}/same-origin-unknown.headers"

  HTML_URL="${base_url}${PROTECTED_SUBPATH}/"
  if [[ -n "${BACKEND_ASSET_PATH}" ]]; then
    STATIC_URL="$(resolve_static_url_from_html "${HTML_URL}" "${BACKEND_ASSET_PATH}")"
  else
    STATIC_URL="${HTML_URL}assets/<missing-from-backend-index>"
  fi
  API_URL="${base_url}${PROTECTED_SUBPATH}/api/status"
  UNKNOWN_URL="${base_url}${UNKNOWN_PROBE_SUBPATH}/"

  if [[ -n "${LUCI_AUTH_COOKIE}" ]]; then
    HTML_STATUS="$(http_probe "${HTML_URL}" "${html_body}" "${html_header}" -H "Cookie: ${LUCI_AUTH_COOKIE}")"
    STATIC_STATUS="$(http_probe "${STATIC_URL}" "${static_body}" "${static_header}" -H "Cookie: ${LUCI_AUTH_COOKIE}")"
    API_STATUS="$(http_probe "${API_URL}" "${api_body}" "${api_header}" -H "Cookie: ${LUCI_AUTH_COOKIE}")"
    UNKNOWN_STATUS="$(http_probe "${UNKNOWN_URL}" "${unknown_body}" "${unknown_header}" -H "Cookie: ${LUCI_AUTH_COOKIE}")"
  else
    HTML_STATUS="$(http_probe "${HTML_URL}" "${html_body}" "${html_header}")"
    STATIC_STATUS="$(http_probe "${STATIC_URL}" "${static_body}" "${static_header}")"
    API_STATUS="$(http_probe "${API_URL}" "${api_body}" "${api_header}")"
    UNKNOWN_STATUS="$(http_probe "${UNKNOWN_URL}" "${unknown_body}" "${unknown_header}")"
  fi

  HTML_LOCATION="$(extract_header_value "${html_header}" 'location')"
  HTML_LOGIN_REQUIRED_HEADER="$(extract_header_value "${html_header}" 'x-luci-login-required')"
  STATIC_LOCATION="$(extract_header_value "${static_header}" 'location')"
  STATIC_CONTENT_TYPE="$(extract_header_value "${static_header}" 'content-type')"
  API_LOCATION="$(extract_header_value "${api_header}" 'location')"
  API_CONTENT_TYPE="$(extract_header_value "${api_header}" 'content-type')"
  UNKNOWN_LOCATION="$(extract_header_value "${unknown_header}" 'location')"

  log "测试 URL: ${HTML_URL}"
  log "HTML shell reachability: status=${HTML_STATUS} url=${HTML_URL} login-required=${HTML_LOGIN_REQUIRED_HEADER:-<none>}"
  log "Static asset reachability: status=${STATIC_STATUS} url=${STATIC_URL} content-type=${STATIC_CONTENT_TYPE:-<none>}"
  log "API reachability: status=${API_STATUS} url=${API_URL} content-type=${API_CONTENT_TYPE:-<none>}"
  log "Unknown route baseline: status=${UNKNOWN_STATUS} url=${UNKNOWN_URL}"

  if [[ "${HTML_STATUS}" != '200' ]]; then
    add_failure_reason "HTML shell 不可达: url=${HTML_URL} status=${HTML_STATUS} location=${HTML_LOCATION:-<none>}"
  fi
  if [[ "${STATIC_STATUS}" != '200' ]]; then
    add_failure_reason "静态资源不可达: url=${STATIC_URL} status=${STATIC_STATUS} location=${STATIC_LOCATION:-<none>}"
  else
    if [[ "${STATIC_CONTENT_TYPE,,}" == *text/html* ]]; then
      add_failure_reason "静态资源语义校验失败: url=${STATIC_URL} 返回 text/html（疑似 LuCI HTML fallback），content-type=${STATIC_CONTENT_TYPE:-<none>}"
    fi
    if body_has_html_markers "${static_body}"; then
      add_failure_reason "静态资源语义校验失败: url=${STATIC_URL} 响应体含 HTML/LuCI 页面标记（疑似 fallback shell）"
    fi

    local static_semantic_error
    static_semantic_error="$(asset_body_semantic_error "${static_body}" "${STATIC_URL}")"
    if [[ -n "${static_semantic_error}" ]]; then
      add_failure_reason "静态资源语义校验失败: url=${STATIC_URL} ${static_semantic_error}"
    fi
  fi
  if [[ "${API_STATUS}" != '200' ]]; then
    add_failure_reason "API 不可达: url=${API_URL} status=${API_STATUS} location=${API_LOCATION:-<none>}"
  else
    if [[ "${API_CONTENT_TYPE,,}" == *text/html* ]]; then
      add_failure_reason "API 语义校验失败: url=${API_URL} 返回 text/html（疑似 LuCI HTML fallback），content-type=${API_CONTENT_TYPE:-<none>}"
    fi
    if [[ "${API_CONTENT_TYPE,,}" != *application/json* ]]; then
      add_failure_reason "API 语义校验失败: url=${API_URL} content-type 非 application/json（当前=${API_CONTENT_TYPE:-<none>}）"
    fi
    if body_has_html_markers "${api_body}"; then
      add_failure_reason "API 语义校验失败: url=${API_URL} 响应体含 HTML/LuCI 页面标记（疑似 fallback shell）"
    fi

    local api_semantic_error
    api_semantic_error="$(api_body_semantic_error "${api_body}")"
    if [[ -n "${api_semantic_error}" ]]; then
      add_failure_reason "API 语义校验失败: url=${API_URL} ${api_semantic_error}"
    fi
  fi

  if [[ "${HTML_STATUS}" == "${UNKNOWN_STATUS}" && "${HTML_STATUS}" != '200' ]]; then
    add_failure_reason "登录态下 target 与随机未注册路径同为 ${HTML_STATUS}，未观察到 transparent-proxy 专用 LuCI dispatcher 映射"
  fi
}

decide_result() {
  if (( FORCE_NEGATIVE == 1 )); then
    add_failure_reason '--force-negative 已启用：强制走 Task 9 fallback 分支'
  fi

  if (( ${#FAILURE_REASONS[@]} == 0 )); then
    SAME_ORIGIN_SUPPORTED=1
    TASK9_FALLBACK_REQUIRED=0
  else
    SAME_ORIGIN_SUPPORTED=0
    TASK9_FALLBACK_REQUIRED=1
  fi
}

write_outputs() {
  local now_utc
  now_utc="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

  {
    printf 'SAME_ORIGIN_SUPPORTED=%s\n' "${SAME_ORIGIN_SUPPORTED}"
    printf 'TASK9_FALLBACK_REQUIRED=%s\n' "${TASK9_FALLBACK_REQUIRED}"
    printf 'TASK9_FALLBACK_MARKER=%q\n' "${TASK9_FALLBACK_MARKER}"
    printf 'PROBE_EXIT_CODE_SUPPORTED=%s\n' "${EXIT_CODE_SUPPORTED}"
    printf 'PROBE_EXIT_CODE_UNSUPPORTED=%s\n' "${EXIT_CODE_UNSUPPORTED}"
    printf 'PROBE_EXIT_CODE_FAILURE=%s\n' "${EXIT_CODE_FAILURE}"
    printf 'PROBE_TESTED_URL=%q\n' "${HTML_URL}"
    printf 'PROBE_HTML_URL=%q\n' "${HTML_URL}"
    printf 'PROBE_HTML_STATUS=%q\n' "${HTML_STATUS}"
    printf 'PROBE_STATIC_URL=%q\n' "${STATIC_URL}"
    printf 'PROBE_STATIC_STATUS=%q\n' "${STATIC_STATUS}"
    printf 'PROBE_STATIC_CONTENT_TYPE=%q\n' "${STATIC_CONTENT_TYPE}"
    printf 'PROBE_API_URL=%q\n' "${API_URL}"
    printf 'PROBE_API_STATUS=%q\n' "${API_STATUS}"
    printf 'PROBE_API_CONTENT_TYPE=%q\n' "${API_CONTENT_TYPE}"
    printf 'PROBE_UNKNOWN_URL=%q\n' "${UNKNOWN_URL}"
    printf 'PROBE_UNKNOWN_STATUS=%q\n' "${UNKNOWN_STATUS}"
    printf 'PROBE_LUCI_LOGIN_URL=%q\n' "${LUCI_LOGIN_URL}"
    printf 'PROBE_LUCI_LOGIN_STATUS=%q\n' "${LUCI_LOGIN_STATUS}"
    printf 'PROBE_LUCI_LOGIN_LOCATION=%q\n' "${LUCI_LOGIN_LOCATION}"
    printf 'PROBE_LUCI_BASELINE_URL=%q\n' "${AUTH_BASELINE_URL}"
    printf 'PROBE_LUCI_BASELINE_STATUS=%q\n' "${AUTH_BASELINE_STATUS}"
    printf 'PROBE_UHTTPD_PROXY_HINT=%q\n' "${UHTTPD_PROXY_HINT}"
    printf 'PROBE_FAILURE_REASON_COUNT=%s\n' "${#FAILURE_REASONS[@]}"
    printf 'PROBE_GENERATED_AT=%q\n' "${now_utc}"

    local i
    for i in "${!FAILURE_REASONS[@]}"; do
      printf 'PROBE_FAILURE_REASON_%d=%q\n' "$((i + 1))" "${FAILURE_REASONS[$i]}"
    done
  } > "${PROBE_ENV_FILE}"

  {
    printf 'timestamp(utc): %s\n' "${now_utc}"
    printf 'same-origin-supported: %s\n' "${SAME_ORIGIN_SUPPORTED}"
    printf 'task9-fallback-required: %s\n' "${TASK9_FALLBACK_REQUIRED}"
    printf 'task9-fallback-marker: %s\n' "${TASK9_FALLBACK_MARKER}"
    printf 'exit-code-contract: supported=%s unsupported=%s script-failure=%s\n' "${EXIT_CODE_SUPPORTED}" "${EXIT_CODE_UNSUPPORTED}" "${EXIT_CODE_FAILURE}"
    printf '\n'
    printf 'tested-url: %s\n' "${HTML_URL}"
    printf 'html-shell: status=%s location=%s x-luci-login-required=%s\n' "${HTML_STATUS}" "${HTML_LOCATION:-<none>}" "${HTML_LOGIN_REQUIRED_HEADER:-<none>}"
    printf 'static-asset: status=%s location=%s content-type=%s\n' "${STATIC_STATUS}" "${STATIC_LOCATION:-<none>}" "${STATIC_CONTENT_TYPE:-<none>}"
    printf 'api-status: status=%s location=%s content-type=%s\n' "${API_STATUS}" "${API_LOCATION:-<none>}" "${API_CONTENT_TYPE:-<none>}"
    printf 'unknown-route-baseline: status=%s location=%s url=%s\n' "${UNKNOWN_STATUS}" "${UNKNOWN_LOCATION:-<none>}" "${UNKNOWN_URL}"
    printf 'luci-login: status=%s location=%s cookie=%s x-luci-login-required=%s\n' "${LUCI_LOGIN_STATUS}" "${LUCI_LOGIN_LOCATION:-<none>}" "${LUCI_AUTH_COOKIE:-<none>}" "${LUCI_LOGIN_REQUIRED_HEADER:-<none>}"
    printf 'luci-baseline: status=%s location=%s url=%s\n' "${AUTH_BASELINE_STATUS}" "${AUTH_BASELINE_LOCATION:-<none>}" "${AUTH_BASELINE_URL}"
    printf 'uhttpd-proxy-hint: %s\n' "${UHTTPD_PROXY_HINT}"
    printf '\n'
    printf 'failure-reason-count: %s\n' "${#FAILURE_REASONS[@]}"

    local i
    for i in "${!FAILURE_REASONS[@]}"; do
      printf 'failure-reason-%d: %s\n' "$((i + 1))" "${FAILURE_REASONS[$i]}"
    done
  } | tee "${PROBE_SUMMARY_FILE}" > "${PROBE_EVIDENCE_FILE}"

  if (( TASK9_FALLBACK_REQUIRED == 1 )); then
    cp "${PROBE_EVIDENCE_FILE}" "${PROBE_ERROR_EVIDENCE_FILE}"
    {
      printf '\n'
      printf 'fallback-decision: Task 9 fallback branch MUST be used.\n'
    } >> "${PROBE_ERROR_EVIDENCE_FILE}"
  else
    rm -f "${PROBE_ERROR_EVIDENCE_FILE}"
  fi

  log "machine-readable env: ${PROBE_ENV_FILE}"
  log "summary evidence (.tmp): ${PROBE_SUMMARY_FILE}"
  log "human evidence: ${PROBE_EVIDENCE_FILE}"
  if (( TASK9_FALLBACK_REQUIRED == 1 )); then
    log "negative-path evidence: ${PROBE_ERROR_EVIDENCE_FILE}"
  fi
}

main() {
  parse_args "$@"
  prepare_directories
  exec > >(tee -a "${PROBE_LOG_FILE}") 2>&1

  prepare_tools

  log '调用共享 readiness 真值入口: test-common.sh --ensure-vm-ready'
  bash "${SCRIPT_DIR}/test-common.sh" --ensure-vm-ready

  capture_backend_asset_path
  start_luci_tunnel
  probe_platform_capability

  if [[ -n "${LUCI_TUNNEL_PORT}" ]]; then
    probe_luci_authentication_and_baseline
    probe_same_origin_routes
  else
    add_failure_reason 'LuCI tunnel 端口未知，无法执行 URL reachability 探测'
  fi

  decide_result
  write_outputs

  printf 'SAME_ORIGIN_SUPPORTED=%s\n' "${SAME_ORIGIN_SUPPORTED}"
  if (( TASK9_FALLBACK_REQUIRED == 1 )); then
    printf '%s=1\n' "${TASK9_FALLBACK_MARKER}"
    exit "${EXIT_CODE_UNSUPPORTED}"
  fi

  exit "${EXIT_CODE_SUPPORTED}"
}

main "$@"
