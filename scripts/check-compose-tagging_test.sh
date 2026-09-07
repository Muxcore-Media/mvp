#!/usr/bin/env bash
# CI: optional media-tagging service is profile-gated in compose files.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

check_file() {
  local file="$1"
  [[ -f "$file" ]] || fail "missing $file"
  grep -qE '^  media-tagging:' "$file" || fail "$file missing service media-tagging"
  awk '/^  media-tagging:/{p=1} p && /profiles:/{print; exit}' "$file" | grep -q '"tagging"' \
    || fail "$file media-tagging must use compose profile tagging"
  grep -A30 '^  media-tagging:' "$file" | grep -q ':9740' \
    || fail "$file media-tagging must publish container gRPC port 9740"
  grep -A30 '^  media-tagging:' "$file" | grep -q ':9741' \
    || fail "$file media-tagging must publish container health port 9741"
  grep -A30 '^  media-tagging:' "$file" | grep -q 'MUXCORE_MODULE_ID: media-tagging' \
    || fail "$file media-tagging must set MUXCORE_MODULE_ID"
  grep -A30 '^  media-tagging:' "$file" | grep -q 'TAGGING_DATA_DIR: /data/tagging' \
    || fail "$file media-tagging must set TAGGING_DATA_DIR"
}

check_file "$ROOT/docker-compose.yml"
check_file "$ROOT/docker-compose.registry.yml"

echo "OK: compose media-tagging profile wired"
