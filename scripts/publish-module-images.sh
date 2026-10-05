#!/usr/bin/env bash
# Build and push sidecar module images to an OCI registry (LAN default; GHCR optional).
#
# Usage:
#   ./scripts/publish-module-images.sh v0.6.7
#   MODULES="api-rest auth-local media-automation" ./scripts/publish-module-images.sh v0.6.7
#   BUILD_ONLY=1 MUXCORE_REGISTRY=localhost:5000/muxcore ./scripts/publish-module-images.sh v0.6.7
#   MUXCORE_REGISTRY=ghcr.io/muxcore-media ./scripts/publish-module-images.sh v0.6.7   # needs write:packages
#
# MUXCORE_REGISTRY defaults to localhost:5000/muxcore (see ../local-registry.sh).
#
# Defaults MODULES to every image in docker-compose.registry.yml (all profiles) except
# muxcored, which publish-muxcored-local.sh builds. Enforced by scripts/check-publish-set.sh
# (TDD §5; part of run-script-tests.sh).
set -euo pipefail

TAG="${1:-}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WS="$(cd "$ROOT/.." && pwd)"
REGISTRY="${MUXCORE_REGISTRY:-localhost:5000/muxcore}"
BUILD_ONLY="${BUILD_ONLY:-0}"
DOCKERFILE="$ROOT/dockerfiles/module.Dockerfile"

DEFAULT_MODULES=(
  api-rest auth-local database-sqlite secrets-file encryption-aesgcm
  call-policy-default publish-policy-default health-monitor admin-ui
  media-movies media-tvshows media-scanner media-automation metadata-tmdb
  media-custom-formats media-rename media-ffprobe media-subtitles
  media-root-folders request-media notification-default userdata-local media-ui
  jellyfin auth-oidc backup-local cache-local downloader-debrid
  downloader-native-torrent downloader-native-usenet downloader-qbittorrent
  downloader-sabnzbd emby indexer-piratebay indexer-torznab media-dlna
  media-intro-outro media-tagging media-transcoder-pool playback-guard
  playback-monitor plex secrets-vault
)

if [[ -n "${MODULES:-}" ]]; then
  read -ra MODULE_LIST <<<"$MODULES"
else
  MODULE_LIST=("${DEFAULT_MODULES[@]}")
fi

die() { echo "FAIL: $*" >&2; exit 1; }

resolve_mvp_dir() {
  if [[ -d "$WS/mvp" ]]; then
    echo mvp
  elif [[ -d "$WS/_mvp" ]]; then
    echo _mvp
  else
    die "mvp module dir not found in workspace $WS (expected mvp/ or legacy _mvp/)"
  fi
}

media_ui_siblings=(
  media-ui-app core contracts-automation contracts-metadata contracts-scanner
  contracts-media jellyfin media-ffprobe media-intro-outro media-list-sync
  media-movies media-subtitles media-tvshows userdata-local
)

preflight_media_ui_context() {
  local mvp_dir="$1"
  local path
  [[ -d "$WS/$mvp_dir" ]] || die "media-ui build missing $WS/$mvp_dir"
  for path in "${media_ui_siblings[@]}"; do
    [[ -d "$WS/$path" ]] || die "media-ui build missing sibling: $WS/$path"
  done
}

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
  die "usage: $0 <tag> (e.g. v0.6.7)"
fi
[[ -f "$DOCKERFILE" ]] || die "missing $DOCKERFILE"

RT="$(detect_runtime)"
echo "==> publishing ${#MODULE_LIST[@]} modules to ${REGISTRY} tag ${TAG} via $RT"

for name in "${MODULE_LIST[@]}"; do
  mod_dir="$WS/$name"
  [[ -d "$mod_dir" ]] || die "module dir not found: $mod_dir"
  image="${REGISTRY}/${name}:${TAG}"
  dockerfile="$DOCKERFILE"
  extra_build_args=()
  if [[ "$name" == "media-ui" ]]; then
    mvp_dir="$(resolve_mvp_dir)"
    preflight_media_ui_context "$mvp_dir"
    dockerfile="$ROOT/dockerfiles/media-ui.Dockerfile"
    extra_build_args=(--build-arg "MVP_DIR=$mvp_dir")
  fi
  echo "==> build $name -> $image"
  "$RT" build -f "$dockerfile" \
    --build-arg MODULE="$name" \
    --build-arg VERSION="${TAG#v}" \
    "${extra_build_args[@]}" \
    -t "$image" \
    "$WS"
  if [[ "$BUILD_ONLY" != "1" ]]; then
    "$RT" push "$image"
  fi
done

echo "OK: published ${#MODULE_LIST[@]} module images (${TAG})"
