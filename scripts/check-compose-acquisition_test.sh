#!/usr/bin/env bash
# CI: household BFF can probe profile-gated indexer/downloader health HTTP.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

check_media_ui() {
  local file="$1"
  [[ -f "$file" ]] || fail "missing $file"
  grep -A50 '^  media-ui:' "$file" | grep -q 'INDEXER_PIRATEBAY_HTTP_URL' \
    || fail "$file media-ui must set INDEXER_PIRATEBAY_HTTP_URL for household acquisition status"
  grep -A50 '^  media-ui:' "$file" | grep -q 'INDEXER_TORZNAB_GRPC_CLIENT_ADDR' \
    || fail "$file media-ui must set INDEXER_TORZNAB_GRPC_CLIENT_ADDR for household Prowlarr/Jackett catalog"
  grep -A50 '^  media-ui:' "$file" | grep -q 'DOWNLOADER_TORRENT_HTTP_URL' \
    || fail "$file media-ui must set DOWNLOADER_TORRENT_HTTP_URL for household acquisition status"
  grep -A50 '^  media-ui:' "$file" | grep -q 'DOWNLOADER_QBIT_HTTP_URL' \
    || fail "$file media-ui must set DOWNLOADER_QBIT_HTTP_URL for household acquisition status"
}

check_torrent_http() {
  local file="$1"
  grep -A30 '^  downloader-native-torrent:' "$file" | grep -q 'MUXCORE_HTTP_ADDR' \
    || fail "$file downloader-native-torrent must set MUXCORE_HTTP_ADDR for BFF health probe"
  grep -A30 '^  downloader-native-torrent:' "$file" | grep -q ':9464' \
    || fail "$file downloader-native-torrent must publish HTTP port 9464"
  awk '/^  downloader-native-torrent:/{p=1} p && /profiles:/{print; exit}' "$file" | grep -q '"downloader-torrent"' \
    || fail "$file downloader-native-torrent must stay on compose profile downloader-torrent"
}

check_media_ui "$ROOT/docker-compose.yml"
check_torrent_http "$ROOT/docker-compose.yml"
check_media_ui "$ROOT/docker-compose.registry.yml"
check_torrent_http "$ROOT/docker-compose.registry.yml"

echo "OK: compose household acquisition probes wired"
