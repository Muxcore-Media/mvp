#!/usr/bin/env bash
# Build and push sidecar module images to an OCI registry (LAN default; GHCR optional).
#
# Usage:
#   ./scripts/publish-module-images.sh v0.6.15
#   MODULES="api-rest auth-local media-automation" ./scripts/publish-module-images.sh v0.6.15
#   BUILD_ONLY=1 MUXCORE_REGISTRY=localhost:5000/muxcore ./scripts/publish-module-images.sh v0.6.15
#   MUXCORE_REGISTRY=ghcr.io/muxcore-media ./scripts/publish-module-images.sh v0.6.15   # needs write:packages
#   PREBUILT_DIR=/tmp/muxcore-bin ./scripts/publish-module-images.sh v0.6.15      # ADR-0014 prebuilt mode
#
# PREBUILT_DIR mode packages binaries built on the host by
# scripts/build-module-binaries.sh (workspace mode, ADR-0012 flags) with
# dockerfiles/module-prebuilt.Dockerfile / media-ui-prebuilt.Dockerfile — no Go
# toolchain or private-module credentials inside the image build. Missing
# binaries are built first (build-module-binaries.sh) unless PREBUILT_NO_BUILD=1.
# Without PREBUILT_DIR each image compiles in-container (module.Dockerfile),
# which needs the module's private dependencies to be fetchable there.
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
PREBUILT_DIR="${PREBUILT_DIR:-}"

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
  die "usage: $0 <tag> (e.g. v0.6.15)"
fi
[[ -f "$DOCKERFILE" ]] || die "missing $DOCKERFILE"

RT="$(detect_runtime)"
echo "==> publishing ${#MODULE_LIST[@]} modules to ${REGISTRY} tag ${TAG} via $RT${PREBUILT_DIR:+ (prebuilt: $PREBUILT_DIR)}"

# prebuilt_ready <name> — host artefacts for <name> exist under PREBUILT_DIR.
prebuilt_ready() {
  if [[ "$1" == "media-ui" ]]; then
    [[ -x "$PREBUILT_DIR/media-ui/mediauiprox" && -f "$PREBUILT_DIR/media-ui/dist-app/index.html" ]]
  else
    [[ -x "$PREBUILT_DIR/$1/module" ]]
  fi
}

if [[ -n "$PREBUILT_DIR" ]]; then
  mkdir -p "$PREBUILT_DIR"
  PREBUILT_DIR="$(cd "$PREBUILT_DIR" && pwd)"
  missing=()
  for name in "${MODULE_LIST[@]}"; do
    prebuilt_ready "$name" || missing+=("$name")
  done
  if [[ ${#missing[@]} -gt 0 ]]; then
    [[ "${PREBUILT_NO_BUILD:-0}" != "1" ]] || die "PREBUILT_DIR missing binaries: ${missing[*]}"
    "$ROOT/scripts/build-module-binaries.sh" "$PREBUILT_DIR" "${missing[@]}"
  fi
fi

# Extra alpine packages a module needs at runtime (module Dockerfiles' APK_EXTRA).
module_apk_extra() {
  case "$1" in
    media-ffprobe | media-intro-outro | media-transcoder-pool) echo ffmpeg ;;
    downloader-native-torrent | indexer-piratebay | indexer-torznab) echo "wireguard-tools iptables iproute2" ;;
    *) echo "" ;;
  esac
}

# Plain-HTTP LAN registry with podman: mark it insecure in registries.conf
# (scripts/clean-room.sh writes a registries.conf.d drop-in for localhost:5000).
push_image() {
  "$RT" push "$1"
}

for name in "${MODULE_LIST[@]}"; do
  image="${REGISTRY}/${name}:${TAG}"
  if [[ -n "$PREBUILT_DIR" ]]; then
    if [[ "$name" == "media-ui" ]]; then
      dockerfile="$ROOT/dockerfiles/media-ui-prebuilt.Dockerfile"
    else
      dockerfile="$ROOT/dockerfiles/module-prebuilt.Dockerfile"
    fi
    echo "==> package $name -> $image"
    "$RT" build -f "$dockerfile" \
      --build-arg APK_EXTRA="$(module_apk_extra "$name")" \
      --label "org.opencontainers.image.version=${TAG#v}" \
      -t "$image" "$PREBUILT_DIR/$name"
    if [[ "$BUILD_ONLY" != "1" ]]; then
      push_image "$image"
    fi
    continue
  fi
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
    --build-arg APK_EXTRA="$(module_apk_extra "$name")" \
    "${extra_build_args[@]}" \
    -t "$image" \
    "$WS"
  if [[ "$BUILD_ONLY" != "1" ]]; then
    push_image "$image"
  fi
done

echo "OK: published ${#MODULE_LIST[@]} module images (${TAG})"
