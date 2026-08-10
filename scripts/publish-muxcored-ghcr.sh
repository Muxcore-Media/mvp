#!/usr/bin/env bash
# Build muxcored and optionally push to GHCR (rootless podman).
#
# Prerequisites:
#   - podman
#   - For push: gh auth login with scopes: repo + write:packages (or packages:write)
#   - permission to push ghcr.io/muxcore-media/muxcored
#
# Usage:
#   ./scripts/publish-muxcored-ghcr.sh            # tag = core HEAD describe / v0.5.2 fallback
#   ./scripts/publish-muxcored-ghcr.sh v0.5.2
#   BUILD_ONLY=1 ./scripts/publish-muxcored-ghcr.sh v0.5.2   # local image only (no GHCR login/push)
#   MUXCORE_CORE_DIR=/path/to/core ./scripts/publish-muxcored-ghcr.sh v0.5.2
set -euo pipefail

TAG="${1:-}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CORE_DIR="${MUXCORE_CORE_DIR:-$ROOT/../core}"
BUILD_ONLY="${BUILD_ONLY:-0}"

if [[ ! -d "$CORE_DIR" ]]; then
  echo "core dir not found: $CORE_DIR (set MUXCORE_CORE_DIR)" >&2
  exit 1
fi

if [[ -z "$TAG" ]]; then
  TAG="$(git -C "$CORE_DIR" describe --tags --abbrev=0 2>/dev/null || true)"
  TAG="${TAG:-v0.5.2}"
fi

IMAGE="ghcr.io/muxcore-media/muxcored:${TAG}"
LOCAL="localhost/muxcored:${TAG}"

echo "building $LOCAL from $CORE_DIR"
podman build -t "$LOCAL" --build-arg "VERSION=${TAG#v}" -f "$CORE_DIR/Dockerfile" "$CORE_DIR"
podman tag "$LOCAL" "localhost/muxcored:latest" 2>/dev/null || true

if [[ "$BUILD_ONLY" == "1" ]]; then
  echo "BUILD_ONLY=1 — skipping GHCR login/push; image ready as $LOCAL"
  exit 0
fi

USER_LOGIN="$(gh api user -q .login)"
echo "logging into ghcr.io as $USER_LOGIN"
gh auth token | podman login ghcr.io -u "$USER_LOGIN" --password-stdin

podman tag "$LOCAL" "$IMAGE"
podman tag "$LOCAL" "ghcr.io/muxcore-media/core:${TAG}"

echo "pushing $IMAGE"
if ! podman push "$IMAGE"; then
  cat >&2 <<'ERR'

Push denied. Current gh token likely lacks packages write scope.
Re-auth with:  gh auth refresh -s write:packages,repo
Or build only:  BUILD_ONLY=1 ./scripts/publish-muxcored-ghcr.sh <tag>
Then re-run this script.
ERR
  exit 1
fi

podman push "ghcr.io/muxcore-media/core:${TAG}" || true
echo "published $IMAGE"
