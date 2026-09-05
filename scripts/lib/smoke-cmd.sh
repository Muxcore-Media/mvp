#!/usr/bin/env bash
# Dispatch smoke helper invocations to host Go binaries or registry grpcurl path.
set -euo pipefail

smoke_cmd_root="${SMOKE_CMD_ROOT:-${ROOT:-}}"

smoke_cmd_init() {
  # shellcheck disable=SC1091
  source "${smoke_cmd_root}/scripts/lib/registry-smoke.sh"
  registry_smoke_root="$smoke_cmd_root"
  if [[ "${MUXCORE_SMOKE_REGISTRY:-}" == "1" ]]; then
    registry_smoke_enable
    return 0
  fi
  if registry_smoke_should_use; then
    registry_smoke_enable
    export MUXCORE_SMOKE_REGISTRY=1
  fi
}

smoke_cmd() {
  local name="$1"
  shift
  if [[ "${MUXCORE_SMOKE_REGISTRY:-}" == "1" ]]; then
    registry_smoke_cmd "$name" "$@"
    return
  fi
  local pkg="./cmd/${name}"
  if [[ ! -x "${smoke_cmd_root}/bin/${name}" ]]; then
    echo "==> building ${name}"
    (cd "$smoke_cmd_root" && go build -o "bin/${name}" "$pkg")
  fi
  "${smoke_cmd_root}/bin/${name}" "$@"
}

smoke_cmd_listmodules() {
  if [[ "${MUXCORE_SMOKE_REGISTRY:-}" == "1" ]]; then
    registry_smoke_cmd listmodules "$@"
    return
  fi
  echo "==> building listmodules"
  (cd "$smoke_cmd_root" && go build -o bin/listmodules ./cmd/listmodules)
  "${smoke_cmd_root}/bin/listmodules" "$@"
}
