#!/usr/bin/env bash
# Host-process MVP runner (used when Docker is unavailable).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
WS="$(cd "$ROOT/.." && pwd)"
BIN="$ROOT/bin"
RUN="$ROOT/run"
DATA="$ROOT/data"
# shellcheck disable=SC1091
[[ -f "$ROOT/.env" ]] && source "$ROOT/.env" || true

export MUXCORE_INSECURE_DISABLE_TLS=true
export MUXCORE_LOG_LEVEL="${MUXCORE_LOG_LEVEL:-info}"
export MUXCORE_CONFIG="${MUXCORE_CONFIG:-$ROOT/muxcore.json}"
MESH="${MUXCORE_MESH_ADDR:-127.0.0.1:9090}"

    mkdir -p "$BIN" "$RUN" "$DATA"/{movies,tvshows,automation,scanner,roots,sqlite,secrets,library/tv,storage,auth,jellyfin,downloads,request}

start_one() {
  local name="$1"; shift
  local pidfile="$RUN/$name.pid"
  local logfile="$RUN/$name.log"
  if [[ -f "$pidfile" ]] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
    echo "already running $name (pid $(cat "$pidfile"))"
    return 0
  fi
  echo "starting $name -> $logfile"
  nohup "$@" >"$logfile" 2>&1 &
  echo $! >"$pidfile"
}

stop_all() {
  for f in "$RUN"/*.pid; do
    [[ -f "$f" ]] || continue
    pid=$(cat "$f")
    name=$(basename "$f" .pid)
    if kill -0 "$pid" 2>/dev/null; then
      echo "stopping $name ($pid)"
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
    rm -f "$f"
  done
}

cmd="${1:-up}"
case "$cmd" in
  stop) stop_all; exit 0 ;;
  up)
    stop_all
    [[ -x "$BIN/muxcored" ]] || (cd "$WS/core" && go build -o "$BIN/muxcored" ./cmd/muxcored)
    start_one core env \
      MUXCORE_CONFIG="$ROOT/muxcore.json" \
      MUXCORE_INSECURE_DISABLE_TLS=true \
      MUXCORE_STORAGE_DIR="$DATA/storage" \
      MUXCORE_LOG_LEVEL="${MUXCORE_LOG_LEVEL:-info}" \
      "$BIN/muxcored"
    # wait for mesh (prefer HTTP 200 once storage is registered)
    for _ in $(seq 1 40); do
      code=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/health || echo 000)
      [[ "$code" == "200" || "$code" == "503" ]] && break
      sleep 0.5
    done

    start_one api-rest env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=api-rest MUXCORE_INSECURE_DISABLE_TLS=true \
      API_REST_HTTP_ADDR=":18080" API_REST_GRPC_ADDR=":9400" \
      "$BIN/api-rest"

    start_one auth-local env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=auth-local MUXCORE_INSECURE_DISABLE_TLS=true \
      AUTH_DB_PATH="$DATA/auth/auth.db" \
      AUTH_GRPC_ADDR=":9403" AUTH_HTTP_ADDR=":9401" \
      "$BIN/auth-local"

    start_one database-sqlite env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=database-sqlite MUXCORE_INSECURE_DISABLE_TLS=true \
      SQLITE_DB_PATH="$DATA/sqlite/muxcore.db" \
      "$BIN/database-sqlite"

    mkdir -p "$DATA/secrets" "$DATA/encryption"
    start_one secrets-file env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=secrets-file MUXCORE_INSECURE_DISABLE_TLS=true \
      SECRETS_STORE="$DATA/secrets/store.json" \
      SECRETS_KEY_FILE="$DATA/secrets/master.key" \
      "$BIN/secrets-file"

    start_one encryption-aesgcm env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=encryption-aesgcm MUXCORE_INSECURE_DISABLE_TLS=true \
      ENCRYPTION_KEY_FILE="$DATA/encryption/master.key" \
      "$BIN/encryption-aesgcm"

    start_one call-policy-default env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=call-policy-default MUXCORE_INSECURE_DISABLE_TLS=true \
      CALL_POLICY_FILE="$WS/call-policy-default/policies.yaml" \
      "$BIN/call-policy-default"

    start_one publish-policy-default env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=publish-policy-default MUXCORE_INSECURE_DISABLE_TLS=true \
      PUBLISH_POLICY_FILE="$WS/publish-policy-default/policies.yaml" \
      "$BIN/publish-policy-default"

    start_one health-monitor env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=health-monitor MUXCORE_INSECURE_DISABLE_TLS=true \
      MUXCORE_MESH_DIAL_LOCAL=true \
      HEALTH_MONITOR_GRPC_ADDR="${HEALTH_MONITOR_GRPC_ADDR:-:9202}" \
      HEALTH_MONITOR_HTTP_ADDR="${HEALTH_MONITOR_HTTP_ADDR:-:9203}" \
      HEALTH_MONITOR_INTERVAL="${HEALTH_MONITOR_INTERVAL:-5s}" \
      "$BIN/health-monitor"

    # :8082 is in auth-local's safeRedirect allowlist for OAuth-style callbacks
    start_one admin-ui env \
      ADMIN_UI_ADDR=":8082" \
      ADMIN_UI_CORE_ADDR="$MESH" \
      ADMIN_UI_INSECURE=true \
      ADMIN_UI_AUTH_ADDR="http://127.0.0.1:9401" \
      ADMIN_UI_HEALTH_MONITOR_URL="${ADMIN_UI_HEALTH_MONITOR_URL:-http://127.0.0.1:9203}" \
      MUXCORE_MESH_DIAL_LOCAL=true \
      "$BIN/admin-ui"

    start_one metadata-tmdb env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=metadata-tmdb MUXCORE_INSECURE_DISABLE_TLS=true \
      TMDB_API_KEY="${TMDB_API_KEY:-}" \
      MUXCORE_CFG_TMDB_API_KEY="${MUXCORE_CFG_TMDB_API_KEY:-${TMDB_API_KEY:-}}" \
      TMDB_FIXTURE="${TMDB_FIXTURE:-}" \
      "$BIN/metadata-tmdb"

    start_one media-movies env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-movies MUXCORE_INSECURE_DISABLE_TLS=true \
      MOVIES_DB_PATH="$DATA/movies/movies.db" MOVIES_IMAGE_DIR="$DATA/movies/images" \
      MOVIES_HTTP_ADDR=":9430" \
      "$BIN/media-movies"

    start_one media-tvshows env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-tvshows MUXCORE_INSECURE_DISABLE_TLS=true \
      TVSHOWS_DB_PATH="$DATA/tvshows/tvshows.db" TVSHOWS_IMAGE_DIR="$DATA/tvshows/images" \
      TVSHOWS_GRPC_ADDR=":9440" TVSHOWS_HTTP_ADDR=":9450" \
      "$BIN/media-tvshows"

    start_one media-automation env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-automation MUXCORE_INSECURE_DISABLE_TLS=true \
      MUXCORE_MESH_DIAL_LOCAL=true \
      AUTOMATION_DB_PATH="$DATA/automation/automation.db" \
      AUTOMATION_GRPC_ADDR=":9460" \
      AUTOMATION_EVENT_SUBSCRIBE_DELAY=1s \
      "$BIN/media-automation"

    start_one media-scanner env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-scanner MUXCORE_INSECURE_DISABLE_TLS=true \
      SCANNER_DB_PATH="$DATA/scanner/scanner.db" \
      SCANNER_LIBRARY_ROOT="$DATA/library" \
      SCANNER_DEFAULT_WATCH_DIR="$DATA/downloads" \
      SCANNER_GRPC_ADDR=":9470" \
      SCANNER_IMPORT_MODE=copy \
      SCANNER_MIN_VIDEO_BYTES=0 \
      "$BIN/media-scanner"

    # DOWNLOADER_ENGINE=fixture (default) for offline smoke; set to empty/anacrolix for live torrents.
    start_one downloader-native-torrent env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=downloader-native-torrent MUXCORE_INSECURE_DISABLE_TLS=true \
      DOWNLOADER_GRPC_ADDR=":9461" \
      DOWNLOAD_DIR="$DATA/downloads" \
      DOWNLOADER_ENGINE="${DOWNLOADER_ENGINE:-fixture}" \
      SEED_MINUTES=1 \
      SEED_RATIO=1.0 \
      "$BIN/downloader-native-torrent"

    # Live Apibay indexer when PIRATEBAY_API_BASE is set (VPN recommended).
    if [[ -n "${PIRATEBAY_API_BASE:-}" ]]; then
      start_one indexer-piratebay env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=indexer-piratebay MUXCORE_INSECURE_DISABLE_TLS=true \
        PIRATEBAY_GRPC_ADDR=":9485" \
        PIRATEBAY_API_BASE="$PIRATEBAY_API_BASE" \
        "$BIN/indexer-piratebay"
    fi

    start_one media-root-folders env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-root-folders MUXCORE_INSECURE_DISABLE_TLS=true \
      ROOTS_DB_PATH="$DATA/roots/roots.db" \
      "$BIN/media-root-folders"

    start_one request-media env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=request-media MUXCORE_INSECURE_DISABLE_TLS=true \
      MUXCORE_MESH_DIAL_LOCAL=true \
      REQUEST_GRPC_ADDR=":9481" \
      REQUEST_HTTP_ADDR=":9380" \
      REQUEST_DATA_DIR="$DATA/request" \
      "$BIN/request-media"

    # Log-only webhook sink (no Discord/SMTP secrets). Health requires ≥1 channel.
    if [[ ! -x "$BIN/notification-default" ]]; then
      echo "building notification-default"
      (cd "$WS/notification-default" && go build -o "$BIN/notification-default" ./cmd/module)
    fi
    start_one notification-default env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=notification-default MUXCORE_INSECURE_DISABLE_TLS=true \
      NOTIFY_GRPC_ADDR=":9441" \
      WEBHOOK_URL="${NOTIFY_WEBHOOK_URL:-http://127.0.0.1:9/muxcore-notify-sink}" \
      "$BIN/notification-default"

    # Soft-config OK without live Jellyfin; set JELLYFIN_BASE_URL + JELLYFIN_API_KEY for sync.
    start_one jellyfin env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=jellyfin MUXCORE_INSECURE_DISABLE_TLS=true \
      JELLYFIN_GRPC_ADDR=":9475" JELLYFIN_HTTP_ADDR=":8475" \
      JELLYFIN_DATA_DIR="$DATA/jellyfin" \
      JELLYFIN_BASE_URL="${JELLYFIN_BASE_URL:-}" \
      JELLYFIN_API_KEY="${JELLYFIN_API_KEY:-}" \
      JELLYFIN_WEBHOOK_SECRET="${JELLYFIN_WEBHOOK_SECRET:-}" \
      "$BIN/jellyfin"

    # Consumer SPA from media-ui-app (clean extract; not the polluted media-ui dump).
    # Disable with MVP_ENABLE_MEDIA_UI=0.
    if [[ "${MVP_ENABLE_MEDIA_UI:-1}" != "0" ]]; then
      UI_DIST="${MEDIA_UI_DIST:-$WS/media-ui-app/dist-app}"
      if [[ ! -d "$UI_DIST" ]]; then
        echo "WARN: media-ui dist missing at $UI_DIST — run: (cd ../media-ui-app && npm ci && npm run build)" >&2
      else
        if [[ ! -x "$BIN/mediauiprox" ]]; then
          echo "building mediauiprox"
          (cd "$ROOT" && go build -o "$BIN/mediauiprox" ./cmd/mediauiprox)
        fi
        start_one media-ui env \
          MEDIA_UI_LISTEN="${MEDIA_UI_LISTEN:-:5173}" \
          MEDIA_UI_DIST="$UI_DIST" \
          MEDIA_UI_REQUIRE_AUTH="${MEDIA_UI_REQUIRE_AUTH:-1}" \
          AUTH_HTTP_URL="${AUTH_HTTP_URL:-http://127.0.0.1:9401}" \
          MOVIES_GRPC_CLIENT_ADDR="127.0.0.1:9420" \
          TVSHOWS_GRPC_CLIENT_ADDR="127.0.0.1:9440" \
          MOVIES_HTTP_URL="http://127.0.0.1:9430" \
          TVSHOWS_HTTP_URL="http://127.0.0.1:9450" \
          REQUEST_MEDIA_HTTP_URL="http://127.0.0.1:9380" \
          "$BIN/mediauiprox" \
            -listen "${MEDIA_UI_LISTEN:-:5173}" \
            -dist "$UI_DIST" \
            -request-http "http://127.0.0.1:9380" \
            -auth-http "${AUTH_HTTP_URL:-http://127.0.0.1:9401}"
      fi
    fi

    echo "started. logs in $RUN ; SMOKE_API_URL=http://127.0.0.1:18080 ./smoke.sh"
    ;;
  *)
    echo "usage: $0 {up|stop}" >&2
    exit 2
    ;;
esac
