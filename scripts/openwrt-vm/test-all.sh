#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

log() {
  printf '[test-all] %s\n' "$*"
}

fail() {
  printf '[test-all][ERROR] %s\n' "$*" >&2
  exit 1
}

run_go_tests() {
  log '运行后端快速层: (cd server && go test ./...)'
  (
    cd "${REPO_ROOT}/server"
    go test ./...
  )
}

run_hotplug_tests() {
  log '运行平台集成层: hotplug'
  bash "${SCRIPT_DIR}/test-hotplug.sh"
}

run_ipk_install_tests() {
  log '运行平台集成层: ipk-install'
  bash "${SCRIPT_DIR}/test-ipk-install.sh"
}

run_ipk_upgrade_tests() {
  log '运行平台集成层: ipk-upgrade-uninstall'
  bash "${SCRIPT_DIR}/test-ipk-upgrade-uninstall.sh"
}

run_luci_probe_tests() {
  log '运行平台集成层: luci-proxy-probe'
  bash "${SCRIPT_DIR}/test-luci-proxy-probe.sh"
}

main() {
  log "开始执行 (Go tests -> VM platform integration)"

  run_go_tests
  run_hotplug_tests
  run_ipk_install_tests
  run_ipk_upgrade_tests
  run_luci_probe_tests

  log "全部通过"
}

main "$@"
