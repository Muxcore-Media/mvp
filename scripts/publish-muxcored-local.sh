#!/usr/bin/env bash
# Build muxcored and tag/push to a Forgejo or LAN OCI registry (no GHCR write:packages).
#
# Prerequisites:
#   - podman or docker on PATH (CONTAINER_RUNTIME overrides)
#   - For push: login to the registry (Forgejo package token, or insecure local registry)
#
# Usage:
#   ./scripts/publish-muxcored-local.sh                 # tag from core HEAD / v0.5.8
#   ./scripts/publish-muxcored-local.sh v0.5.8
#   BUILD_ONLY=1 ./scripts/publish-muxcored-local.sh v0.5.8   # build+tag, skip push
#   MUXCORE_REGISTRY=git.zem.systems/muxcore ./scripts/publish-muxcored-local.sh v0.5.8
#   MUXCORE_REGISTRY=localhost:5000/muxcore ./scripts/publish-muxcored-local.sh v0.5.8
#
# Defaults:
#   MUXCORE_REGISTRY=git.zem.systems/muxcore   (Forgejo org packages)
#   LAN registry: set MUXCORE_REGISTRY=localhost:5000/muxcore (see ../local-registry.sh)
#
# Compose consumers pull via docker-compose.registry.yml:
#   export MUXCORE_REGISTRY=… MUXCORE_IMAGE_TAG=v0.5.8
#   docker compose -f docker-compose.registry.yml up -d
#
# Docker equivalent (when podman is absent):
#   docker build --build-arg VERSION=0.5.8 -t localhost/muxcored:v0.5.8 -f ../core/Dockerfile ../core
#   docker tag localhost/muxcored:v0.5.8 ${MUXCORE_REGISTRY:-git.zem.systems/muxcore}/muxcored:v0.5.8
#   docker push ${MUXCORE_REGISTRY:-git.zem.systems/muxcore}/muxcored:v0.5.8
set -euo pipefail

TAG="${1:-}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CORE_DIR="${MUXCORE_CORE_DIR:-$ROOT/../core}"
BUILD_ONLY="${BUILD_ONLY:-0}"
# Forgejo org packages by default; override for LAN (localhost:5000/muxcore).
REGISTRY="${MUXCORE_REGISTRY:-git.zem.systems/muxcore}"

die() {
  echo "FAIL: $*" >&2
  exit 1
}

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

print_no_runtime() {
  cat >&2 <<EOF
FAIL: neither podman nor docker is on PATH.

Install Podman or Docker Engine, then re-run this script.
Docker-only equivalent:

  cd ${CORE_DIR}
  docker build --build-arg VERSION=${TAG#v} \\
    -t localhost/muxcored:${TAG} -f Dockerfile .
  docker tag localhost/muxcored:${TAG} ${REGISTRY}/muxcored:${TAG}
  # optional push (skip when BUILD_ONLY=1):
  docker push ${REGISTRY}/muxcored:${TAG}

LAN registry helper: ${ROOT}/local-registry.sh start
EOF
}

if [[ ! -d "$CORE_DIR" ]]; then
  die "core dir not found: $CORE_DIR (set MUXCORE_CORE_DIR)"
fi
if [[ ! -f "$CORE_DIR/Dockerfile" ]]; then
  die "missing Dockerfile at $CORE_DIR/Dockerfile"
fi

if [[ -z "$TAG" ]]; then
  TAG="$(git -C "$CORE_DIR" describe --tags --abbrev=0 2>/dev/null || true)"
  TAG="${TAG:-v0.5.8}"
fi

if ! RUNTIME="$(detect_runtime)"; then
  print_no_runtime
  exit 1
fi

LOCAL="localhost/muxcored:${TAG}"
REMOTE="${REGISTRY}/muxcored:${TAG}"
VERSION_ARG="${TAG#v}"

echo "runtime=$RUNTIME registry=$REGISTRY tag=$TAG"
echo "building $LOCAL from $CORE_DIR"
"$RUNTIME" build -t "$LOCAL" --build-arg "VERSION=${VERSION_ARG}" -f "$CORE_DIR/Dockerfile" "$CORE_DIR"
"$RUNTIME" tag "$LOCAL" "localhost/muxcored:latest" 2>/dev/null || true
"$RUNTIME" tag "$LOCAL" "$REMOTE"
"$RUNTIME" tag "$LOCAL" "${REGISTRY}/core:${TAG}" 2>/dev/null || true

if [[ "$BUILD_ONLY" == "1" ]]; then
  echo "BUILD_ONLY=1 — skip push; images ready:"
  echo "  $LOCAL"
  echo "  $REMOTE"
  exit 0
fi

echo "pushing $REMOTE"
if ! "$RUNTIME" push "$REMOTE"; then
  cat >&2 <<ERR

Push failed for $REMOTE.

Forgejo: create a package/write token for org muxcore, then:
  echo \$TOKEN | $RUNTIME login git.zem.systems -u <user> --password-stdin

LAN: start a local registry and retry with MUXCORE_REGISTRY=localhost:5000/muxcore
  ${ROOT}/local-registry.sh start
  MUXCORE_REGISTRY=localhost:5000/muxcore $0 ${TAG}

Or build/tag only:
  BUILD_ONLY=1 MUXCORE_REGISTRY=${REGISTRY} $0 ${TAG}
ERR
  exit 1
fi

"$RUNTIME" push "${REGISTRY}/core:${TAG}" 2>/dev/null || true
echo "published $REMOTE"
echo "Install: MUXCORE_REGISTRY=${REGISTRY} MUXCORE_IMAGE_TAG=${TAG} docker compose -f docker-compose.registry.yml up -d"
