#!/usr/bin/env bash
# CI: optional acquisition downloader services are profile-gated in compose files.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

DOWNLOADERS=(
  "downloader-native-torrent:downloader-torrent:9461"
  "downloader-qbittorrent:downloader-qbittorrent:9462"
  "downloader-native-usenet:downloader-usenet:9622"
  "downloader-sabnzbd:downloader-sabnzbd:9620"
  "downloader-debrid:downloader-debrid:9630"
)

check_file() {
  local file="$1"
  [[ -f "$file" ]] || fail "missing $file"
  for entry in "${DOWNLOADERS[@]}"; do
    IFS=: read -r svc profile port <<<"$entry"
    grep -qE "^  ${svc}:" "$file" || fail "$file missing service ${svc}"
    awk "/^  ${svc}:/{p=1} p && /profiles:/{print; exit}" "$file" | grep -q "\"${profile}\"" \
      || fail "$file ${svc} must use compose profile ${profile}"
    grep -A25 "^  ${svc}:" "$file" | grep -q ":${port}" \
      || fail "$file ${svc} must publish container gRPC port ${port}"
  done
}

check_file "$ROOT/docker-compose.yml"
check_file "$ROOT/docker-compose.registry.yml"

echo "OK: compose downloader profiles wired (${#DOWNLOADERS[@]} services)"
