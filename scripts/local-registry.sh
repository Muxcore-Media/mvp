#!/usr/bin/env bash
# Thin wrapper around ../local-registry.sh (canonical LAN OCI helper).
#
# Usage:
#   ./scripts/local-registry.sh          # start registry on :5000
#   ./scripts/local-registry.sh stop
#   MUXCORE_REGISTRY=localhost:5000/muxcore ./scripts/publish-muxcored-local.sh v0.5.7
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ACTION="${1:-start}"

case "$ACTION" in
  start)
    exec "$ROOT/local-registry.sh" up
    ;;
  stop)
    exec "$ROOT/local-registry.sh" down
    ;;
  *)
    exec "$ROOT/local-registry.sh" "$@"
    ;;
esac
