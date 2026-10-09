#!/usr/bin/env bash
# Build module binaries once on the host, in umbrella workspace mode, for the
# prebuilt image path (ADR-0014; publish-module-images.sh PREBUILT_DIR=…).
#
# Usage:
#   ./scripts/build-module-binaries.sh <out-dir> [module ...]
#   MODULES="api-rest auth-local" ./scripts/build-module-binaries.sh /tmp/muxcore-bin
#
# Per module it writes <out-dir>/<module>/module (linux/amd64 by default), plus
# runtime files the module loads from its working dir (policies*.yaml), using
# the ADR-0012 canonical flags:
#   CGO_ENABLED=0 GOFLAGS=-mod=readonly GOTOOLCHAIN=go<V> \
#     go build -trimpath -buildvcs=false -ldflags=-buildid= ./cmd/module/
# but against the umbrella go.work (scripts/gen-go-work.sh) instead of a clean
# clone of the tag: images come from the pinned submodule commits, and private
# Muxcore-Media modules resolve from the workspace without registry credentials
# inside the image build (ADR-0014 "Consequences"). <V> is the go.work `go` line.
#
# `media-ui` is special: it builds the BFF (`_mvp/cmd/mediauiprox`) into
# <out-dir>/media-ui/mediauiprox and the SPA (`media-ui-app`, npm) into
# <out-dir>/media-ui/dist-app. The SPA is built from a copy of the tracked
# sources so the media-ui-app checkout is not modified.
#
# Env:
#   MODULES          module list (default: arguments, else every image in
#                    publish-module-images.sh DEFAULT_MODULES)
#   GOOS / GOARCH    target platform (default linux / amd64)
#   BUILD_JOBS       parallel module builds (default 2); GOFLAGS -p is set to
#                    BUILD_GO_P (default 2): at most ~4 compilers at once
#   MUXCORE_GO_WORK  go.work to use (default <umbrella>/go.work, regenerated)
#   GOTOOLCHAIN      override the toolchain (default go<go.work go line>)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WS="$(cd "$ROOT/.." && pwd)"

die() { echo "FAIL: $*" >&2; exit 1; }

OUT="${1:-}"
[[ -n "$OUT" ]] || die "usage: $0 <out-dir> [module ...]"
shift
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"

if [[ $# -gt 0 ]]; then
  MODULE_LIST=("$@")
elif [[ -n "${MODULES:-}" ]]; then
  read -ra MODULE_LIST <<<"$MODULES"
else
  # Same default set as the image publisher (single source of truth).
  mapfile -t MODULE_LIST < <(sed -n '/^DEFAULT_MODULES=(/,/^)/p' "$ROOT/scripts/publish-module-images.sh" \
    | sed '1d;$d' | tr ' ' '\n' | sed '/^$/d')
fi
[[ ${#MODULE_LIST[@]} -gt 0 ]] || die "no modules to build"

command -v go >/dev/null 2>&1 || die "go not on PATH"

GOWORK_FILE="${MUXCORE_GO_WORK:-}"
if [[ -z "$GOWORK_FILE" ]]; then
  [[ -x "$WS/scripts/gen-go-work.sh" ]] || die "not inside the umbrella workspace ($WS/scripts/gen-go-work.sh missing)"
  "$WS/scripts/gen-go-work.sh" >&2
  GOWORK_FILE="$WS/go.work"
fi
[[ -f "$GOWORK_FILE" ]] || die "go.work not found: $GOWORK_FILE"
go_line="$(sed -n 's/^go[[:space:]]\+//p' "$GOWORK_FILE" | head -1)"
[[ -n "$go_line" ]] || die "no go line in $GOWORK_FILE"
[[ "$go_line" =~ ^[0-9]+\.[0-9]+$ ]] && go_line="${go_line}.0"

export GOWORK="$GOWORK_FILE"
export GOOS="${GOOS:-linux}" GOARCH="${GOARCH:-amd64}" CGO_ENABLED=0
export GOTOOLCHAIN="${GOTOOLCHAIN:-go${go_line}}"
export GOFLAGS="-mod=readonly -p=${BUILD_GO_P:-2}"
export GOPRIVATE="${GOPRIVATE:-github.com/Muxcore-Media/*}"
export GONOSUMDB="${GONOSUMDB:-github.com/Muxcore-Media/*}"
export GIT_TERMINAL_PROMPT=0
JOBS="${BUILD_JOBS:-2}"

echo "==> building ${#MODULE_LIST[@]} module(s) -> $OUT (${GOOS}/${GOARCH}, ${GOTOOLCHAIN}, GOWORK=${GOWORK}, jobs=${JOBS})"

# build_go <module-dir> <pkg> <out-file>
build_go() {
  (cd "$1" && go build -trimpath -buildvcs=false -ldflags=-buildid= -o "$3" "$2")
}

build_spa() {
  local src="$WS/media-ui-app" tmp
  [[ -f "$src/package.json" ]] || die "media-ui-app not found at $src"
  command -v npm >/dev/null 2>&1 || die "npm not on PATH (needed for the media-ui SPA)"
  tmp="$OUT/.media-ui-app-src"
  rm -rf "$tmp" "$OUT/media-ui/dist-app"
  mkdir -p "$tmp" "$OUT/media-ui"
  (cd "$src" && git ls-files -z --cached --others --exclude-standard | tar --null -T - -cf -) | tar -C "$tmp" -xf -
  (cd "$tmp" && npm ci --no-fund --no-audit --loglevel=error && npm run build)
  [[ -f "$tmp/dist-app/index.html" ]] || die "SPA build produced no dist-app/index.html"
  mv "$tmp/dist-app" "$OUT/media-ui/dist-app"
  rm -rf "$tmp"
}

build_one() {
  local name="$1" dir
  if [[ "$name" == "media-ui" ]]; then
    mkdir -p "$OUT/media-ui"
    build_go "$ROOT" ./cmd/mediauiprox "$OUT/media-ui/mediauiprox"
    build_spa
    return
  fi
  dir="$WS/$name"
  [[ -d "$dir" ]] || die "module dir not found: $dir"
  [[ -d "$dir/cmd/module" ]] || die "$name has no ./cmd/module entrypoint"
  mkdir -p "$OUT/$name"
  build_go "$dir" ./cmd/module/ "$OUT/$name/module"
  # Local readiness probe shipped next to the daemon (ADR-0033: userdata-local
  # >= v0.1.6); module-prebuilt.Dockerfile copies it to /app/userdata-health.
  if [[ -d "$dir/cmd/userdata-health" ]]; then
    build_go "$dir" ./cmd/userdata-health/ "$OUT/$name/userdata-health"
  fi
  # Runtime files the module reads relative to its working dir (/app in the
  # image), e.g. call/publish-policy-default policies.yaml.
  local f
  for f in "$dir"/policies*.yaml; do
    [[ -f "$f" ]] && cp "$f" "$OUT/$name/"
  done
  return 0
}

export -f build_go build_spa build_one die
export OUT WS ROOT

logdir="$OUT/.logs"
mkdir -p "$logdir"
failed=0
# shellcheck disable=SC2016 # $1/$2 are expanded by the child bash
printf '%s\n' "${MODULE_LIST[@]}" | xargs -P "$JOBS" -I{} bash -c \
  'if build_one "$1" >"$2/$1.log" 2>&1; then echo "OK   $1"; else echo "FAIL $1 (log: $2/$1.log)"; tail -20 "$2/$1.log" | sed "s/^/     /"; exit 1; fi' \
  _ {} "$logdir" || failed=1

[[ "$failed" -eq 0 ]] || die "module build failed (logs in $logdir)"
echo "OK: built ${#MODULE_LIST[@]} module(s) into $OUT"
