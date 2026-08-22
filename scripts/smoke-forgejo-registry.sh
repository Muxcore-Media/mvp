#!/usr/bin/env bash
# Smoke-test Forgejo/LAN OCI publish path for muxcored (build + tag; push optional).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TAG="${1:-v0.5.4}"

echo "== Forgejo registry smoke (BUILD_ONLY) =="
BUILD_ONLY=1 "$ROOT/scripts/publish-muxcored-local.sh" "$TAG"

if command -v podman >/dev/null 2>&1 || command -v docker >/dev/null 2>&1; then
  runtime="$(command -v podman 2>/dev/null || command -v docker)"
  img="${MUXCORE_REGISTRY:-git.zem.systems/muxcore}/muxcored:${TAG}"
  if "$runtime" image inspect "$img" >/dev/null 2>&1; then
    echo "OK image present locally: $img"
    exit 0
  fi
  echo "WARN: image not found after build: $img" >&2
  exit 1
fi

echo "WARN: no container runtime; build script path only" >&2
exit 0
