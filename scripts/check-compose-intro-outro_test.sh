#!/usr/bin/env bash
# CI: optional media-intro-outro service is profile-gated in compose files.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

check_file() {
  local file="$1"
  [[ -f "$file" ]] || fail "missing $file"
  grep -qE '^  media-intro-outro:' "$file" || fail "$file missing service media-intro-outro"
  awk '/^  media-intro-outro:/{p=1} p && /profiles:/{print; exit}' "$file" | grep -q '"intro-outro"' \
    || fail "$file media-intro-outro must use compose profile intro-outro"
  grep -A30 '^  media-intro-outro:' "$file" | grep -q ':9710' \
    || fail "$file media-intro-outro must publish container gRPC port 9710"
  grep -A30 '^  media-intro-outro:' "$file" | grep -q ':9711' \
    || fail "$file media-intro-outro must publish container health port 9711"
  grep -A30 '^  media-intro-outro:' "$file" | grep -q 'MUXCORE_MODULE_ID: media-intro-outro' \
    || fail "$file media-intro-outro must set MUXCORE_MODULE_ID"
  grep -A30 '^  media-intro-outro:' "$file" | grep -q 'INTRO_OUTRO_DATA_DIR: /data/intro-outro' \
    || fail "$file media-intro-outro must set INTRO_OUTRO_DATA_DIR"
}

check_file "$ROOT/docker-compose.yml"
check_file "$ROOT/docker-compose.registry.yml"

echo "OK: compose media-intro-outro profile wired"
