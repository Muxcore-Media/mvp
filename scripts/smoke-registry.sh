#!/usr/bin/env bash
# Path A registry install verify — same gate as smoke.sh without host Go or sibling clones.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export MUXCORE_SMOKE_REGISTRY=1
export MUXCORE_COMPOSE_FILE="${MUXCORE_COMPOSE_FILE:-docker-compose.registry.yml}"
exec "$ROOT/smoke.sh" "$@"
