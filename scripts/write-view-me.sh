#!/usr/bin/env bash
# Write mvp/run/VIEW-ME.txt with operator URLs for the host stack.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck disable=SC1091
[[ -f "$ROOT/.env" ]] && source "$ROOT/.env" || true
mkdir -p "$ROOT/run"

{
  cat <<EOF
MuxCore MVP — operator URLs
===========================

Admin UI:     http://127.0.0.1:8082
Media UI:     http://127.0.0.1:5173
API (rest):   http://127.0.0.1:18080
Core health:  http://127.0.0.1:8080/health
Request UI:   http://127.0.0.1:9380

Admin user:   ${MVP_ADMIN_USER:-admin}
Auth gRPC:    ${AUTH_GRPC_ADDR:-127.0.0.1:9403}

Library dir:  ${LIBRARY_DIR:-${MVP_LIBRARY_ROOT:-$ROOT/data/library}}
Download dir: ${DOWNLOAD_DIR:-${MVP_DOWNLOAD_DIR:-$ROOT/data/downloads}}

Stop stack:   ./run-host.sh stop
Smoke test:   ./smoke.sh
Rebuild pins: ./scripts/rebuild-catalog-peers.sh <module...>
EOF

  echo
  echo "Optional peers (from .env):"
  [[ "${MVP_ENABLE_MEDIA_LIST_SYNC:-0}" == "1" ]] && echo "  media-list-sync     :9530  (admin /list-sync)"
  [[ "${MVP_ENABLE_WORKFLOW_TAPESTRY:-0}" == "1" ]] && echo "  workflow-tapestry   :9603"
  [[ "${MVP_ENABLE_CACHE_REDIS:-0}" == "1" || -n "${REDIS_ADDR:-}" ]] && echo "  cache-redis         :9600  (REDIS_ADDR=${REDIS_ADDR:-})"
  [[ "${MVP_ENABLE_MEDIA_TRANSCODER:-0}" == "1" ]] && echo "  media-transcoder    :9525"
  [[ "${MVP_ENABLE_NOTIFICATION_APPRISE:-0}" == "1" ]] && echo "  notification-apprise :9445"
  [[ "${TMDB_FIXTURE:-}" == "1" ]] && echo "  metadata-tmdb       fixture mode (TMDB_FIXTURE=1)"
} >"$ROOT/run/VIEW-ME.txt"

echo "wrote $ROOT/run/VIEW-ME.txt"
