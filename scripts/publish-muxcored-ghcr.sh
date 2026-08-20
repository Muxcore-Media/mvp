#!/usr/bin/env bash
# Build muxcored and optionally push to GHCR (future public mirror).
#
# Prefer Forgejo/LAN for day-1 installs (no write:packages):
#   ./scripts/publish-muxcored-local.sh
#   docker compose -f docker-compose.registry.yml up -d
#
# Prerequisites:
#   - podman or docker (CONTAINER_RUNTIME overrides)
#   - For push: gh auth login with scopes: repo + write:packages (or packages:write)
#   - permission to push ghcr.io/muxcore-media/muxcored
#
# Usage:
#   ./scripts/publish-muxcored-ghcr.sh            # tag = core HEAD describe / v0.5.4 fallback
#   ./scripts/publish-muxcored-ghcr.sh v0.5.4
#   BUILD_ONLY=1 ./scripts/publish-muxcored-ghcr.sh v0.5.4   # local image only (no GHCR login/push)
#   MUXCORE_CORE_DIR=/path/to/core ./scripts/publish-muxcored-ghcr.sh v0.5.4
#
# Docker equivalent when podman is absent:
#   docker build --build-arg VERSION=0.5.4 -t localhost/muxcored:v0.5.4 -f ../core/Dockerfile ../core
set -euo pipefail

TAG="${1:-}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CORE_DIR="${MUXCORE_CORE_DIR:-$ROOT/../core}"
BUILD_ONLY="${BUILD_ONLY:-0}"

die() { echo "FAIL: $*" >&2; exit 1; }

detect_runtime() {
  if [[ -n "${CONTAINER_RUNTIME:-}" ]]; then
    command -v "$CONTAINER_RUNTIME" >/dev/null 2>&1 || die "CONTAINER_RUNTIME=${CONTAINER_RUNTIME} not found"
    printf '%s\n' "$CONTAINER_RUNTIME"
    return 0
  fi
  if command -v podman >/dev/null 2>&1; then
    printf '%s\n' podman
    return 0
  fi
  if command -v docker >/dev/null 2>&1; then
    printf '%s\n' docker
    return 0
  fi
  return 1
}

if [[ ! -d "$CORE_DIR" ]]; then
  die "core dir not found: $CORE_DIR (set MUXCORE_CORE_DIR)"
fi

if [[ -z "$TAG" ]]; then
  TAG="$(git -C "$CORE_DIR" describe --tags --abbrev=0 2>/dev/null || true)"
  TAG="${TAG:-v0.5.4}"
fi

IMAGE="ghcr.io/muxcore-media/muxcored:${TAG}"
LOCAL="localhost/muxcored:${TAG}"

if ! RUNTIME="$(detect_runtime)"; then
  cat >&2 <<EOF
FAIL: neither podman nor docker is on PATH.

Docker equivalent:
  docker build --build-arg VERSION=${TAG#v} -t ${LOCAL} -f ${CORE_DIR}/Dockerfile ${CORE_DIR}

For installs without GHCR write:packages use:
  ./scripts/publish-muxcored-local.sh ${TAG}
EOF
  exit 1
fi

echo "building $LOCAL from $CORE_DIR (runtime=$RUNTIME)"
"$RUNTIME" build -t "$LOCAL" --build-arg "VERSION=${TAG#v}" -f "$CORE_DIR/Dockerfile" "$CORE_DIR"
"$RUNTIME" tag "$LOCAL" "localhost/muxcored:latest" 2>/dev/null || true

if [[ "$BUILD_ONLY" == "1" ]]; then
  echo "BUILD_ONLY=1 — skipping GHCR login/push; image ready as $LOCAL"
  echo "Tip: Forgejo/LAN publish without write:packages → ./scripts/publish-muxcored-local.sh ${TAG}"
  exit 0
fi

USER_LOGIN="$(gh api user -q .login)"
echo "logging into ghcr.io as $USER_LOGIN"
gh auth token | "$RUNTIME" login ghcr.io -u "$USER_LOGIN" --password-stdin

"$RUNTIME" tag "$LOCAL" "$IMAGE"
"$RUNTIME" tag "$LOCAL" "ghcr.io/muxcore-media/core:${TAG}"

echo "pushing $IMAGE"
if ! "$RUNTIME" push "$IMAGE"; then
  cat >&2 <<'ERR'

Push denied. Current gh token likely lacks packages write scope.
Re-auth with:  gh auth refresh -s write:packages,repo
Or build only:  BUILD_ONLY=1 ./scripts/publish-muxcored-ghcr.sh <tag>
Or use Forgejo/LAN (no GHCR): ./scripts/publish-muxcored-local.sh <tag>
ERR
  exit 1
fi

"$RUNTIME" push "ghcr.io/muxcore-media/core:${TAG}" || true
echo "published $IMAGE"
