#!/usr/bin/env bash
# CI: optional media-dlna service is profile-gated in compose files.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

check_file() {
  local file="$1"
  [[ -f "$file" ]] || fail "missing $file"
  grep -qE '^  media-dlna:' "$file" || fail "$file missing service media-dlna"
  awk '/^  media-dlna:/{p=1} p && /profiles:/{print; exit}' "$file" | grep -q '"dlna"' \
    || fail "$file media-dlna must use compose profile dlna"
  grep -A30 '^  media-dlna:' "$file" | grep -q ':9750' \
    || fail "$file media-dlna must publish container DLNA HTTP port 9750"
  grep -A30 '^  media-dlna:' "$file" | grep -q ':9751' \
    || fail "$file media-dlna must publish container gRPC port 9751"
  grep -A30 '^  media-dlna:' "$file" | grep -q ':8751' \
    || fail "$file media-dlna must publish container health port 8751"
  grep -A30 '^  media-dlna:' "$file" | grep -q 'MUXCORE_MODULE_ID: media-dlna' \
    || fail "$file media-dlna must set MUXCORE_MODULE_ID"
}

check_file "$ROOT/docker-compose.yml"
check_file "$ROOT/docker-compose.registry.yml"

echo "OK: compose media-dlna profile wired"
