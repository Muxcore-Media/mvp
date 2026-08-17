#!/usr/bin/env bash
# Laptop-only local OCI registry helper (localhost:5000).
# Starts registry:2 via Docker or Podman when available; documents muxcored
# build/tag/push. Never reports push success when the container runtime is missing.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
REGISTRY_HOST="${MUXCORE_LOCAL_REGISTRY:-localhost:5000}"
REGISTRY_NAME="${MUXCORE_LOCAL_REGISTRY_NAME:-muxcore-local-registry}"
REGISTRY_IMAGE="${MUXCORE_LOCAL_REGISTRY_IMAGE:-docker.io/library/registry:2}"
IMAGE_REPO="${MUXCORE_LOCAL_IMAGE_REPO:-muxcore/muxcored}"
DEFAULT_TAG="${MUXCORE_LOCAL_IMAGE_TAG:-v0.5.0}"
CORE_DIR="${MUXCORE_CORE_DIR:-$ROOT/../core}"

usage() {
  cat <<EOF
usage: $(basename "$0") <command> [args]

Laptop helper for a local image registry at ${REGISTRY_HOST}.

Commands:
  start|up          Start registry:2 on ${REGISTRY_HOST} (docker or podman)
  stop|down         Stop and remove the registry container
  status            Print runtime + whether the registry responds
  push [tag]        Build/tag/push muxcored to ${REGISTRY_HOST}/${IMAGE_REPO}:<tag>
                    (default tag: ${DEFAULT_TAG})
  docs|help         Print this help and the manual build/tag/push recipe

Env overrides:
  MUXCORE_LOCAL_REGISTRY       default ${REGISTRY_HOST}
  MUXCORE_LOCAL_REGISTRY_NAME  container name (default ${REGISTRY_NAME})
  MUXCORE_LOCAL_REGISTRY_IMAGE registry image (default ${REGISTRY_IMAGE})
  MUXCORE_LOCAL_IMAGE_REPO     image path under registry (default ${IMAGE_REPO})
  MUXCORE_LOCAL_IMAGE_TAG      default tag for push (default ${DEFAULT_TAG})
  MUXCORE_CORE_DIR             path to core/ with Dockerfile (default ../core)
  CONTAINER_RUNTIME            force docker or podman

muxcored build / tag / push (manual):
  cd ${CORE_DIR}
  \$RUNTIME build --build-arg VERSION=${DEFAULT_TAG} \\
    -t ${REGISTRY_HOST}/${IMAGE_REPO}:${DEFAULT_TAG} .
  \$RUNTIME tag ${REGISTRY_HOST}/${IMAGE_REPO}:${DEFAULT_TAG} \\
    ${REGISTRY_HOST}/${IMAGE_REPO}:latest   # optional
  \$RUNTIME push ${REGISTRY_HOST}/${IMAGE_REPO}:${DEFAULT_TAG}

Or: $(basename "$0") start && $(basename "$0") push ${DEFAULT_TAG}
EOF
}

die() {
  echo "FAIL: $*" >&2
  exit 1
}

detect_runtime() {
  if [[ -n "${CONTAINER_RUNTIME:-}" ]]; then
    if command -v "$CONTAINER_RUNTIME" >/dev/null 2>&1; then
      printf '%s\n' "$CONTAINER_RUNTIME"
      return 0
    fi
    die "CONTAINER_RUNTIME=${CONTAINER_RUNTIME} not found on PATH"
  fi
  if command -v docker >/dev/null 2>&1; then
    printf '%s\n' docker
    return 0
  fi
  if command -v podman >/dev/null 2>&1; then
    printf '%s\n' podman
    return 0
  fi
  return 1
}

print_no_runtime_instructions() {
  cat >&2 <<EOF
FAIL: neither docker nor podman is available on PATH.

This laptop helper cannot start a registry or push images without a
container runtime. Install one of:

  - Docker Engine (https://docs.docker.com/engine/install/)
  - Podman (https://podman.io/getting-started/installation)

Then re-run: $(basename "$0") start && $(basename "$0") push ${DEFAULT_TAG}

Manual recipe once a runtime exists:
  cd ${CORE_DIR}
  docker build --build-arg VERSION=${DEFAULT_TAG} -t ${REGISTRY_HOST}/${IMAGE_REPO}:${DEFAULT_TAG} .
  docker push ${REGISTRY_HOST}/${IMAGE_REPO}:${DEFAULT_TAG}
EOF
}

registry_up() {
  local runtime="$1"
  if "$runtime" inspect "$REGISTRY_NAME" >/dev/null 2>&1; then
    local running
    running="$("$runtime" inspect -f '{{.State.Running}}' "$REGISTRY_NAME" 2>/dev/null || echo false)"
    if [[ "$running" == "true" ]]; then
      echo "OK: registry already running (${REGISTRY_NAME} @ ${REGISTRY_HOST})"
      return 0
    fi
    "$runtime" start "$REGISTRY_NAME" >/dev/null
    echo "OK: started existing registry container ${REGISTRY_NAME}"
    return 0
  fi
  "$runtime" run -d --restart=unless-stopped \
    --name "$REGISTRY_NAME" \
    -p "${REGISTRY_HOST##*:}:5000" \
    "$REGISTRY_IMAGE" >/dev/null
  echo "OK: registry listening on ${REGISTRY_HOST} (container ${REGISTRY_NAME})"
}

registry_down() {
  local runtime="$1"
  if "$runtime" inspect "$REGISTRY_NAME" >/dev/null 2>&1; then
    "$runtime" rm -f "$REGISTRY_NAME" >/dev/null
    echo "OK: removed ${REGISTRY_NAME}"
  else
    echo "OK: registry container not present"
  fi
}

registry_status() {
  if ! runtime="$(detect_runtime)"; then
    echo "runtime: none"
    echo "registry: unknown (no docker/podman)"
    return 1
  fi
  echo "runtime: $runtime"
  if "$runtime" inspect "$REGISTRY_NAME" >/dev/null 2>&1; then
    local running
    running="$("$runtime" inspect -f '{{.State.Running}}' "$REGISTRY_NAME" 2>/dev/null || echo false)"
    echo "container: ${REGISTRY_NAME} running=${running}"
  else
    echo "container: ${REGISTRY_NAME} absent"
  fi
  if curl -sf "http://${REGISTRY_HOST}/v2/" >/dev/null 2>&1; then
    echo "registry: responding at http://${REGISTRY_HOST}/v2/"
  else
    echo "registry: not responding at http://${REGISTRY_HOST}/v2/"
    return 1
  fi
}

push_muxcored() {
  local tag="${1:-$DEFAULT_TAG}"
  local runtime
  if ! runtime="$(detect_runtime)"; then
    print_no_runtime_instructions
    exit 1
  fi
  if [[ ! -f "$CORE_DIR/Dockerfile" ]]; then
    die "missing Dockerfile at $CORE_DIR/Dockerfile"
  fi
  if ! curl -sf "http://${REGISTRY_HOST}/v2/" >/dev/null 2>&1; then
    echo "WARN: registry not responding; attempting start…"
    registry_up "$runtime"
  fi
  (
    cd "$CORE_DIR"
    "$runtime" build --build-arg "VERSION=${tag}" -t "${REGISTRY_HOST}/${IMAGE_REPO}:${tag}" .
    "$runtime" push "${REGISTRY_HOST}/${IMAGE_REPO}:${tag}"
  )
  echo "OK: pushed ${REGISTRY_HOST}/${IMAGE_REPO}:${tag}"
}

cmd="${1:-docs}"
shift || true
case "$cmd" in
  start|up)
    if ! runtime="$(detect_runtime)"; then print_no_runtime_instructions; exit 1; fi
    registry_up "$runtime"
    ;;
  stop|down)
    if ! runtime="$(detect_runtime)"; then print_no_runtime_instructions; exit 1; fi
    registry_down "$runtime"
    ;;
  status)
    registry_status
    ;;
  push)
    push_muxcored "${1:-}"
    ;;
  docs|help|-h|--help)
    usage
    ;;
  *)
    usage >&2
    die "unknown command: $cmd"
    ;;
esac
