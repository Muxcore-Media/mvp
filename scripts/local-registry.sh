#!/usr/bin/env bash
# Start a LAN OCI registry for MuxCore image publish/pull (no GHCR write:packages).
#
# Usage:
#   ./scripts/local-registry.sh          # start registry on :5000
#   ./scripts/local-registry.sh stop
#   MUXCORE_REGISTRY=localhost:5000/muxcore ./scripts/publish-muxcored-local.sh v0.5.8
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NAME="${MUXCORE_LOCAL_REGISTRY_NAME:-muxcore-registry}"
PORT="${MUXCORE_LOCAL_REGISTRY_PORT:-5000}"
ACTION="${1:-start}"

have() { command -v "$1" >/dev/null 2>&1; }

runtime() {
  if have podman; then echo podman
  elif have docker; then echo docker
  else
    echo "need podman or docker to run a local registry" >&2
    exit 1
  fi
}

RT="$(runtime)"

case "$ACTION" in
  start)
    if "$RT" inspect "$NAME" >/dev/null 2>&1; then
      "$RT" start "$NAME" >/dev/null 2>&1 || true
      echo "registry already present: http://127.0.0.1:${PORT}/v2/"
    else
      "$RT" run -d --name "$NAME" -p "${PORT}:5000" \
        -v muxcore-registry-data:/var/lib/registry \
        docker.io/library/registry:2
      echo "registry started: http://127.0.0.1:${PORT}/v2/"
    fi
    echo "export MUXCORE_REGISTRY=localhost:${PORT}/muxcore"
    echo "then: ./scripts/publish-muxcored-local.sh v0.5.8"
    ;;
  stop)
    "$RT" stop "$NAME" >/dev/null 2>&1 || true
    echo "registry stopped ($NAME)"
    ;;
  *)
    echo "usage: $0 [start|stop]" >&2
    exit 2
    ;;
esac
