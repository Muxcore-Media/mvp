#!/usr/bin/env bash
# Verify GHCR publish path builds locally (BUILD_ONLY). Push requires gh write:packages.
#
# Usage:
#   ./scripts/smoke-ghcr-build.sh v0.5.8
#   BUILD_ONLY=1 ./scripts/smoke-ghcr-build.sh
#
# Unlock push (interactive, on desk):
#   gh auth refresh -h github.com -s write:packages,repo
#   ./scripts/publish-muxcored-ghcr.sh v0.5.8
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TAG="${1:-v0.5.8}"

echo "== GHCR build smoke (BUILD_ONLY) tag=$TAG =="
if ! gh auth status 2>&1 | grep -q 'write:packages'; then
  echo "NOTE: gh token lacks write:packages — push will fail until:"
  echo "  gh auth refresh -h github.com -s write:packages,repo"
fi

BUILD_ONLY=1 "$ROOT/scripts/publish-muxcored-ghcr.sh" "$TAG"

RT="$(command -v podman 2>/dev/null || command -v docker 2>/dev/null || true)"
if [[ -n "$RT" ]] && "$RT" image inspect "localhost/muxcored:${TAG}" >/dev/null 2>&1; then
  echo "OK image localhost/muxcored:${TAG}"
  exit 0
fi
echo "WARN: image not found after build" >&2
exit 1
