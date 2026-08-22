#!/usr/bin/env bash
# Build and push sidecar module images to Forgejo/LAN OCI registry (MASTER-ROADMAP P0).
#
# Usage:
#   ./scripts/publish-module-images.sh v0.5.4
#   MODULES="api-rest auth-local media-automation" ./scripts/publish-module-images.sh v0.5.4
#   BUILD_ONLY=1 MUXCORE_REGISTRY=localhost:5000/muxcore ./scripts/publish-module-images.sh v0.5.4
#
# Defaults MODULES to the MVP registry compose set in docker-compose.registry.yml.
set -euo pipefail

TAG="${1:-}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WS="$(cd "$ROOT/.." && pwd)"
REGISTRY="${MUXCORE_REGISTRY:-git.zem.systems/muxcore}"
BUILD_ONLY="${BUILD_ONLY:-0}"
DOCKERFILE="$ROOT/dockerfiles/module.Dockerfile"

DEFAULT_MODULES=(
  api-rest auth-local database-sqlite secrets-file encryption-aesgcm
  call-policy-default publish-policy-default health-monitor admin-ui
  media-movies media-tvshows media-scanner media-automation metadata-tmdb
  request-media media-ui
)

if [[ -n "${MODULES:-}" ]]; then
  read -ra MODULE_LIST <<<"$MODULES"
else
  MODULE_LIST=("${DEFAULT_MODULES[@]}")
fi

die() { echo "FAIL: $*" >&2; exit 1; }

detect_runtime() {
  if [[ -n "${CONTAINER_RUNTIME:-}" ]]; then
    command -v "$CONTAINER_RUNTIME" >/dev/null 2>&1 || die "CONTAINER_RUNTIME not found"
    echo "$CONTAINER_RUNTIME"
    return
  fi
  if command -v podman >/dev/null 2>&1; then echo podman; return; fi
  if command -v docker >/dev/null 2>&1; then echo docker; return; fi
  die "neither podman nor docker on PATH"
}

if [[ -z "$TAG" ]]; then
  die "usage: $0 <tag> (e.g. v0.5.4)"
fi
[[ -f "$DOCKERFILE" ]] || die "missing $DOCKERFILE"

RT="$(detect_runtime)"
echo "==> publishing ${#MODULE_LIST[@]} modules to ${REGISTRY} tag ${TAG} via $RT"

for name in "${MODULE_LIST[@]}"; do
  mod_dir="$WS/$name"
  [[ -d "$mod_dir" ]] || die "module dir not found: $mod_dir"
  image="${REGISTRY}/${name}:${TAG}"
  echo "==> build $name -> $image"
  "$RT" build -f "$DOCKERFILE" \
    --build-arg MODULE="$name" \
    --build-arg VERSION="${TAG#v}" \
    -t "$image" \
    "$WS"
  if [[ "$BUILD_ONLY" != "1" ]]; then
    "$RT" push "$image"
  fi
done

echo "OK: published ${#MODULE_LIST[@]} module images (${TAG})"
