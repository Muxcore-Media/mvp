#!/usr/bin/env bash
# Build and install a sibling module only when its version is on origin/main.
#
# Usage:
#   ./scripts/install-origin-module.sh media-scanner
#   ./scripts/install-origin-module.sh media-automation --dest /path/to/mvp/bin
#   ./scripts/install-origin-module.sh downloader-native-torrent --verify-only
#   ./scripts/install-origin-module.sh --verify-live media-scanner media-automation downloader-native-torrent
#
# Env:
#   MVP_DEPLOY_SSH   ssh target for --verify-live (optional)
#   MVP_DEPLOY_RUN   remote run dir (default /mnt/fast-storage/appdata/muxcore/mvp/run)
#   MVP_ORIGIN_RECORD  host log path (default $ROOT/run/ORIGIN-BINARIES.log)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/origin-module.sh"

ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
WS="$(cd "$ROOT/.." && pwd)"
BIN="${MVP_BIN:-$ROOT/bin}"
RECORD="${MVP_ORIGIN_RECORD:-$ROOT/run/ORIGIN-BINARIES.log}"

usage() {
  echo "usage: $0 <module> [--dest DIR] [--verify-only] [--build-only]" >&2
  echo "       $0 --verify-live [module ...]" >&2
  exit 2
}

build_module() {
  local repo="$1" ver="$2" out="$3"
  local cmd="CGO_ENABLED=0 go build -ldflags=\"-s -w -X main.version=v${ver}\" -o \"$out\" ./cmd/module"
  if command -v go >/dev/null 2>&1; then
    (cd "$repo" && eval "$cmd")
  else
    nix-shell -p go --run "cd \"$repo\" && $cmd"
  fi
}

record_line() {
  mkdir -p "$(dirname "$RECORD")"
  echo "$1" >>"$RECORD"
}

verify_live_one() {
  local name="$1"
  local repo="$WS/$name"
  local pin version sha ref log=""
  pin="$(assert_module_on_origin "$repo")"
  version="${pin%%$'\t'*}"
  ref="${pin##*$'\t'}"
  sha="$(echo "$pin" | cut -f2)"
  local ssh="${MVP_DEPLOY_SSH:-}"
  local run_dir="${MVP_DEPLOY_RUN:-/mnt/fast-storage/appdata/muxcore/mvp/run}"
  if [[ -n "$ssh" ]]; then
    log="$(ssh -6 -o BatchMode=yes -o ConnectTimeout=20 "$ssh" "grep \"module registered with core id=${name}\" \"$run_dir/${name}.log\" | tail -1" || true)"
  elif [[ -f "$ROOT/run/${name}.log" ]]; then
    log="$(grep "module registered with core id=${name}" "$ROOT/run/${name}.log" | tail -1 || true)"
  fi
  # Logs look like: version=0.1.21 mesh_addr=  (space after version so 0.1.2 ≠ 0.1.21)
  if [[ "$log" != *"version=${version} "* ]]; then
    echo "origin-module: live $name is not origin version $version" >&2
    echo "  origin $ref $sha" >&2
    echo "  log: ${log:-<none>}" >&2
    return 1
  fi
  local line
  line="$(format_origin_record "$name" "$version" "$sha" "$ref")"
  record_line "$line"
  echo "ok $line"
}

if [[ "${1:-}" == "--verify-live" ]]; then
  shift
  mods=("$@")
  if [[ ${#mods[@]} -eq 0 ]]; then
    mods=(media-scanner media-automation downloader-native-torrent)
  fi
  if [[ -n "${MVP_DEPLOY_SSH:-}" && "${MVP_SKIP_PREFLIGHT:-0}" != "1" ]]; then
    MVP_DEPLOY_SSH="$MVP_DEPLOY_SSH" "$SCRIPT_DIR/preflight-vault-ssh.sh"
  fi
  for m in "${mods[@]}"; do
    verify_live_one "$m"
  done
  exit 0
fi

[[ $# -ge 1 ]] || usage
NAME="$1"
shift
DEST=""
VERIFY_ONLY=0
BUILD_ONLY=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dest) DEST="${2:-}"; shift 2 ;;
    --verify-only) VERIFY_ONLY=1; shift ;;
    --build-only) BUILD_ONLY=1; shift ;;
    -h|--help) usage ;;
    *) echo "unknown arg: $1" >&2; usage ;;
  esac
done

REPO="$WS/$NAME"
if [[ ! -d "$REPO" ]]; then
  echo "origin-module: missing sibling $REPO" >&2
  exit 1
fi

PIN="$(assert_module_on_origin "$REPO")"
VERSION="${PIN%%$'\t'*}"
REF="${PIN##*$'\t'}"
SHA="$(echo "$PIN" | cut -f2)"
echo "origin pin $NAME version=$VERSION sha=$SHA ref=$REF"

if [[ "$VERIFY_ONLY" -eq 1 ]]; then
  exit 0
fi

mkdir -p "$BIN"
OUT="$BIN/${NAME}.origin-build"
build_module "$REPO" "$VERSION" "$OUT"
LINE="$(format_origin_record "$NAME" "$VERSION" "$SHA" "$REF")"
echo "$LINE" >"${OUT}.origin"
record_line "$LINE"

if [[ "$BUILD_ONLY" -eq 1 ]]; then
  echo "built $OUT"
  exit 0
fi

if [[ -z "$DEST" ]]; then
  DEST="$BIN"
fi
mkdir -p "$DEST"
cp -f "$OUT" "$DEST/${NAME}.new"
cp -f "${OUT}.origin" "$DEST/${NAME}.origin"
echo "staged $DEST/${NAME}.new ($LINE)"
echo "install: stop the module, mv ${NAME}.new $NAME, chmod +x, restart"
echo "record appended to $RECORD"
