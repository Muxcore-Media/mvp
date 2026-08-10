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

# Dev default is insecure mesh TLS. Staging profile (run-host-staging.sh) leaves this unset.
if [[ "${MUXCORE_PROFILE:-}" == "staging" ]]; then
  unset MUXCORE_INSECURE_DISABLE_TLS || true
else
  export MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-true}"
fi
export MUXCORE_LOG_LEVEL="${MUXCORE_LOG_LEVEL:-info}"
export MUXCORE_CONFIG="${MUXCORE_CONFIG:-$ROOT/muxcore.json}"
MESH="${MUXCORE_MESH_ADDR:-127.0.0.1:9090}"
MODULE_CERT_ROOT="${MUXCORE_MODULE_CERT_DIR:-$ROOT/tls/module-certs}"

mkdir -p "$BIN" "$RUN" "$DATA"/{movies,tvshows,automation,scanner,roots,sqlite,secrets,library/tv,storage,auth,jellyfin,downloads,request,formats,rename,ffprobe,subtitles}

start_one() {
  local name="$1"; shift
  local pidfile="$RUN/$name.pid"
  local logfile="$RUN/$name.log"
  if [[ -f "$pidfile" ]] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
    echo "already running $name (pid $(cat "$pidfile"))"
    return 0
  fi
  echo "starting $name -> $logfile"
  local -a tls_env=()
  if [[ "${MUXCORE_PROFILE:-}" == "staging" ]]; then
    local dir="$MODULE_CERT_ROOT/$name"
    if [[ -f "$dir/module.crt" && -f "$dir/module.key" && -f "$dir/ca.crt" ]]; then
      tls_env=(
        "MUXCORE_TLS_CERT=$dir/module.crt"
        "MUXCORE_TLS_KEY=$dir/module.key"
        "MUXCORE_TLS_CA=$dir/ca.crt"
      )
    fi
  fi
  if [[ "${1:-}" == "env" ]]; then
    shift
    nohup env "${tls_env[@]}" "$@" >"$logfile" 2>&1 &
  else
    nohup env "${tls_env[@]}" "$@" >"$logfile" 2>&1 &
  fi
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
  # Sweep orphan host binaries that outlived their pidfiles (port collisions otherwise).
  if [[ -d "$BIN" ]]; then
    for bin in "$BIN"/*; do
      [[ -x "$bin" && -f "$bin" ]] || continue
      base=$(basename "$bin")
      pkill -x "$base" 2>/dev/null || true
    done
    sleep 0.5
  fi
}

# Graceful stop of a single sidecar (SIGTERM, wait, then SIGKILL). Removes pidfile.
stop_one() {
  local name="$1"
  local pidfile="$RUN/$name.pid"
  if [[ -f "$pidfile" ]]; then
    local pid
    pid=$(cat "$pidfile")
    if kill -0 "$pid" 2>/dev/null; then
      echo "stopping $name ($pid)"
      kill "$pid" 2>/dev/null || true
      for _ in $(seq 1 20); do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.25
      done
      if kill -0 "$pid" 2>/dev/null; then
        echo "WARN: $name still alive; SIGKILL" >&2
        kill -9 "$pid" 2>/dev/null || true
      fi
    fi
    rm -f "$pidfile"
  fi
  # Orphan process without pidfile (e.g. after manual start).
  if [[ -x "$BIN/$name" ]] || [[ "$name" == "media-ui" ]]; then
    local bin_base="$name"
    [[ "$name" == "core" ]] && bin_base=muxcored
    [[ "$name" == "media-ui" ]] && bin_base=mediauiprox
    pkill -x "$bin_base" 2>/dev/null || true
  fi
}

unregister_best_effort() {
  local name="$1"
  [[ "$name" == "core" || "$name" == "healthtick" || "$name" == "media-ui" || "$name" == "admin-ui" ]] && return 0
  if [[ ! -x "$BIN/unregistermodule" ]]; then
    (cd "$ROOT" && go build -o "$BIN/unregistermodule" ./cmd/unregistermodule) || true
  fi
  if [[ -x "$BIN/unregistermodule" ]]; then
    "$BIN/unregistermodule" -addr "$MESH" "$name" 2>/dev/null || true
  fi
}

# When START_ONLY is set (restart path), skip launching other modules.
maybe_start() {
  local name="$1"
  shift
  if [[ -n "${START_ONLY:-}" && "$START_ONLY" != "$name" ]]; then
    return 0
  fi
  start_one "$name" "$@"
}

cmd="${1:-up}"
case "$cmd" in
  stop) stop_all; exit 0 ;;
  stop-one|stop_one)
    [[ -n "${2:-}" ]] || { echo "usage: $0 stop-one <name>" >&2; exit 2; }
    stop_one "$2"
    unregister_best_effort "$2"
    exit 0
    ;;
  unregister)
    [[ -n "${2:-}" ]] || { echo "usage: $0 unregister <module-id>" >&2; exit 2; }
    if [[ ! -x "$BIN/unregistermodule" ]]; then
      (cd "$ROOT" && go build -o "$BIN/unregistermodule" ./cmd/unregistermodule)
    fi
    "$BIN/unregistermodule" -addr "$MESH" "$2"
    exit 0
    ;;
  restart)
    [[ -n "${2:-}" ]] || { echo "usage: $0 restart <name>" >&2; exit 2; }
    stop_one "$2"
    unregister_best_effort "$2"
    sleep 0.5
    exec env START_ONLY="$2" bash "$0" up
    ;;
  up)
    if [[ -z "${START_ONLY:-}" ]]; then
      stop_all
      [[ -x "$BIN/muxcored" ]] || (cd "$WS/core" && go build -o "$BIN/muxcored" ./cmd/muxcored)
      start_one core env \
        MUXCORE_CONFIG="$MUXCORE_CONFIG" \
        MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        MUXCORE_STORAGE_DIR="$DATA/storage" \
        MUXCORE_LOG_LEVEL="${MUXCORE_LOG_LEVEL:-info}" \
        "$BIN/muxcored"
      # wait for mesh (prefer HTTP 200 once storage is registered; staging API is TLS)
      for _ in $(seq 1 40); do
        if [[ "${MUXCORE_PROFILE:-}" == "staging" ]]; then
          code=$(curl -sk -o /dev/null -w '%{http_code}' https://127.0.0.1:8080/health || echo 000)
        else
          code=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/health || echo 000)
        fi
        [[ "$code" == "200" || "$code" == "503" ]] && break
        sleep 0.5
      done
    else
      echo "restart path: starting only $START_ONLY (core left running)"
    fi

    maybe_start api-rest env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=api-rest MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      MUXCORE_MESH_DIAL_LOCAL=true \
      API_REST_HTTP_ADDR=":18080" API_REST_GRPC_ADDR=":9400" \
      "$BIN/api-rest"

    maybe_start auth-local env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=auth-local MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      AUTH_DB_PATH="$DATA/auth/auth.db" \
      AUTH_GRPC_ADDR=":9403" AUTH_HTTP_ADDR=":9401" \
      "$BIN/auth-local"

    maybe_start database-sqlite env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=database-sqlite MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      SQLITE_DB_PATH="$DATA/sqlite/muxcore.db" \
      "$BIN/database-sqlite"

    mkdir -p "$DATA/secrets" "$DATA/encryption"
    maybe_start secrets-file env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=secrets-file MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      SECRETS_STORE="$DATA/secrets/store.json" \
      SECRETS_KEY_FILE="$DATA/secrets/master.key" \
      SECRETS_GRPC_ADDR="${SECRETS_GRPC_ADDR:-:9550}" \
      "$BIN/secrets-file"

    maybe_start encryption-aesgcm env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=encryption-aesgcm MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      ENCRYPTION_KEY_FILE="$DATA/encryption/master.key" \
      "$BIN/encryption-aesgcm"

    maybe_start call-policy-default env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=call-policy-default MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      CALL_POLICY_FILE="$WS/call-policy-default/policies.yaml" \
      "$BIN/call-policy-default"

    maybe_start publish-policy-default env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=publish-policy-default MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      PUBLISH_POLICY_FILE="$WS/publish-policy-default/policies.yaml" \
      "$BIN/publish-policy-default"

    maybe_start health-monitor env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=health-monitor MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      MUXCORE_MESH_DIAL_LOCAL=true \
      HEALTH_MONITOR_GRPC_ADDR="${HEALTH_MONITOR_GRPC_ADDR:-:9202}" \
      HEALTH_MONITOR_HTTP_ADDR="${HEALTH_MONITOR_HTTP_ADDR:-:9203}" \
      HEALTH_MONITOR_INTERVAL="${HEALTH_MONITOR_INTERVAL:-5s}" \
      "$BIN/health-monitor"

    # Keep /status non-idle between module self-reports (local mesh fan-out already allowed).
    if [[ ! -x "$BIN/healthtick" ]]; then
      echo "building healthtick"
      (cd "$ROOT" && go build -o "$BIN/healthtick" ./cmd/healthtick) || true
    fi
    if [[ -x "$BIN/healthtick" ]]; then
      maybe_start healthtick \
        "$BIN/healthtick" -addr "127.0.0.1:9202" -interval 30s
    fi

    # Public auth URL for browser redirects (Caddy); internal for code exchange.
    maybe_start admin-ui env \
      ADMIN_UI_ADDR=":8082" \
      ADMIN_UI_CORE_ADDR="$MESH" \
      ADMIN_UI_INSECURE=true \
      ADMIN_UI_AUTH_ADDR="${ADMIN_UI_AUTH_ADDR:-https://auth.gringotts}" \
      ADMIN_UI_AUTH_INTERNAL_ADDR="${ADMIN_UI_AUTH_INTERNAL_ADDR:-http://127.0.0.1:9401}" \
      ADMIN_UI_PUBLIC_URL="${ADMIN_UI_PUBLIC_URL:-https://admin.gringotts}" \
      ADMIN_UI_TRUSTED_PROXIES="${ADMIN_UI_TRUSTED_PROXIES:-127.0.0.1/32,::1/128}" \
      ADMIN_UI_HEALTH_MONITOR_URL="${ADMIN_UI_HEALTH_MONITOR_URL:-http://127.0.0.1:9203}" \
      MUXCORE_MESH_DIAL_LOCAL=true \
      "$BIN/admin-ui"

    maybe_start metadata-tmdb env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=metadata-tmdb MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      TMDB_API_KEY="${TMDB_API_KEY:-}" \
      MUXCORE_CFG_TMDB_API_KEY="${MUXCORE_CFG_TMDB_API_KEY:-${TMDB_API_KEY:-}}" \
      TMDB_FIXTURE="${TMDB_FIXTURE:-}" \
      "$BIN/metadata-tmdb"

    maybe_start media-movies env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-movies MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      MUXCORE_MESH_DIAL_LOCAL=true \
      MOVIES_DB_PATH="$DATA/movies/movies.db" MOVIES_IMAGE_DIR="$DATA/movies/images" \
      MOVIES_HTTP_ADDR=":9430" \
      "$BIN/media-movies"

    maybe_start media-tvshows env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-tvshows MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      MUXCORE_MESH_DIAL_LOCAL=true \
      TVSHOWS_DB_PATH="$DATA/tvshows/tvshows.db" TVSHOWS_IMAGE_DIR="$DATA/tvshows/images" \
      TVSHOWS_GRPC_ADDR=":9440" TVSHOWS_HTTP_ADDR=":9450" \
      "$BIN/media-tvshows"

    maybe_start media-automation env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-automation MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      MUXCORE_MESH_DIAL_LOCAL=true \
      AUTOMATION_DB_PATH="$DATA/automation/automation.db" \
      AUTOMATION_GRPC_ADDR=":9460" \
      AUTOMATION_EVENT_SUBSCRIBE_DELAY=1s \
      "$BIN/media-automation"

    # Scoring + naming + analyze peers (default host).
    mkdir -p "$DATA/formats" "$DATA/rename" "$DATA/ffprobe" "$DATA/subtitles/files"
    if [[ ! -x "$BIN/media-custom-formats" ]]; then
      echo "building media-custom-formats"
      (cd "$WS/media-custom-formats" && go build -o "$BIN/media-custom-formats" ./cmd/module)
    fi
    maybe_start media-custom-formats env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-custom-formats MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      FORMATS_DB_PATH="$DATA/formats/formats.db" \
      FORMATS_GRPC_ADDR=":9490" \
      FORMATS_SEED_DEFAULTS=true \
      "$BIN/media-custom-formats"

    if [[ ! -x "$BIN/media-rename" ]]; then
      echo "building media-rename"
      (cd "$WS/media-rename" && go build -o "$BIN/media-rename" ./cmd/module)
    fi
    maybe_start media-rename env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-rename MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      RENAME_DB_PATH="$DATA/rename/rename.db" \
      RENAME_GRPC_ADDR="${RENAME_GRPC_ADDR:-:9510}" \
      RENAME_IMPORT_MODE=copy \
      "$BIN/media-rename"

    if [[ ! -x "$BIN/media-ffprobe" ]]; then
      echo "building media-ffprobe"
      (cd "$WS/media-ffprobe" && go build -o "$BIN/media-ffprobe" ./cmd/module)
    fi
    maybe_start media-ffprobe env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-ffprobe MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      FFPROBE_DB_PATH="$DATA/ffprobe/cache.db" \
      FFPROBE_GRPC_ADDR="${FFPROBE_GRPC_ADDR:-:9480}" \
      "$BIN/media-ffprobe"

    if [[ ! -x "$BIN/media-subtitles" ]]; then
      echo "building media-subtitles"
      (cd "$WS/media-subtitles" && go build -o "$BIN/media-subtitles" ./cmd/module)
    fi
    maybe_start media-subtitles env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-subtitles MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      SUBS_DB_PATH="$DATA/subtitles/subtitles.db" \
      SUBS_DIR="$DATA/subtitles/files" \
      SUBS_GRPC_ADDR="${SUBS_GRPC_ADDR:-:9520}" \
      "$BIN/media-subtitles"

    LIBRARY_ROOT="${MVP_LIBRARY_ROOT:-$DATA/library}"
    TV_LIBRARY_ROOT="${MVP_TV_LIBRARY_ROOT:-$DATA/library/tv}"
    DOWNLOADS_DIR="${MVP_DOWNLOADS_DIR:-$DATA/downloads}"
    mkdir -p "$LIBRARY_ROOT" "$TV_LIBRARY_ROOT" "$DOWNLOADS_DIR"

    maybe_start media-scanner env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-scanner MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      MUXCORE_MESH_DIAL_LOCAL=true \
      SCANNER_DB_PATH="$DATA/scanner/scanner.db" \
      SCANNER_LIBRARY_ROOT="$LIBRARY_ROOT" \
      SCANNER_DEFAULT_WATCH_DIR="$DOWNLOADS_DIR" \
      SCANNER_GRPC_ADDR=":9470" \
      SCANNER_IMPORT_MODE=copy \
      SCANNER_MIN_VIDEO_BYTES=0 \
      "$BIN/media-scanner"

    # DOWNLOADER_ENGINE=fixture (default) for offline smoke; set to empty/anacrolix for live torrents.
    # WireGuard needs CAP_NET_ADMIN (setcap below). NAT-PMP auto-starts when WG conf comments say
    # "NAT-PMP ... = on" or NAT_PMP_PORT is set (maps UDP+TCP on the torrent listen port).
    if [[ -x "$BIN/downloader-native-torrent" ]] && command -v setcap >/dev/null 2>&1; then
      if ! getcap "$BIN/downloader-native-torrent" 2>/dev/null | grep -q 'cap_net_admin'; then
        sudo setcap 'cap_net_admin,cap_net_raw+ep' "$BIN/downloader-native-torrent" 2>/dev/null \
          || echo "WARN: setcap failed — WireGuard auto-start needs CAP_NET_ADMIN" >&2
      fi
    fi
    WG_CONF_PATH="${WG_CONF:-}"
    if [[ -z "$WG_CONF_PATH" && -f "$WS/wg-mux.conf" ]]; then
      WG_CONF_PATH="$WS/wg-mux.conf"
    elif [[ -z "$WG_CONF_PATH" && -f "$WS/wg.conf" ]]; then
      WG_CONF_PATH="$WS/wg.conf"
    fi
    maybe_start downloader-native-torrent env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=downloader-native-torrent MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      DOWNLOADER_GRPC_ADDR=":9461" \
      DOWNLOAD_DIR="$DOWNLOADS_DIR" \
      DOWNLOADER_ENGINE="${DOWNLOADER_ENGINE:-fixture}" \
      SEED_MINUTES=1 \
      SEED_RATIO=1.0 \
      WG_CONF="${WG_CONF_PATH}" \
      WG_KILL_SWITCH="${WG_KILL_SWITCH:-false}" \
      TORRENT_LISTEN_PORT="${TORRENT_LISTEN_PORT:-6881}" \
      NAT_PMP_PORT="${NAT_PMP_PORT:-${TORRENT_LISTEN_PORT:-6881}}" \
      "$BIN/downloader-native-torrent"

    # Live Apibay indexer when PIRATEBAY_API_BASE is set (VPN recommended).
    if [[ -n "${PIRATEBAY_API_BASE:-}" ]]; then
      maybe_start indexer-piratebay env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=indexer-piratebay MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        PIRATEBAY_GRPC_ADDR=":9485" \
        PIRATEBAY_API_BASE="$PIRATEBAY_API_BASE" \
        "$BIN/indexer-piratebay"
    fi

    # Torznab aggregator (Prowlarr/Jackett). Soft-empty when TORZNAB_URL unset.
    if [[ ! -x "$BIN/indexer-torznab" ]]; then
      echo "building indexer-torznab"
      (cd "$WS/indexer-torznab" && go build -o "$BIN/indexer-torznab" ./cmd/module)
    fi
    maybe_start indexer-torznab env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=indexer-torznab MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      TORZNAB_GRPC_ADDR="${TORZNAB_GRPC_ADDR:-:9486}" \
      TORZNAB_URL="${TORZNAB_URL:-}" \
      TORZNAB_API_KEY="${TORZNAB_API_KEY:-}" \
      TORZNAB_NAME="${TORZNAB_NAME:-Torznab}" \
      "$BIN/indexer-torznab"

    maybe_start media-root-folders env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-root-folders MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      ROOTS_DB_PATH="$DATA/roots/roots.db" \
      "$BIN/media-root-folders"

    maybe_start request-media env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=request-media MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
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
    maybe_start notification-default env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=notification-default MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      NOTIFY_GRPC_ADDR=":9441" \
      WEBHOOK_URL="${NOTIFY_WEBHOOK_URL:-http://127.0.0.1:9/muxcore-notify-sink}" \
      "$BIN/notification-default"

    # Soft-config OK without live Jellyfin; set JELLYFIN_BASE_URL + JELLYFIN_API_KEY for sync.
    maybe_start jellyfin env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=jellyfin MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      JELLYFIN_GRPC_ADDR=":9475" JELLYFIN_HTTP_ADDR=":8475" \
      JELLYFIN_DATA_DIR="$DATA/jellyfin" \
      JELLYFIN_BASE_URL="${JELLYFIN_BASE_URL:-}" \
      JELLYFIN_API_KEY="${JELLYFIN_API_KEY:-}" \
      JELLYFIN_WEBHOOK_SECRET="${JELLYFIN_WEBHOOK_SECRET:-}" \
      "$BIN/jellyfin"

    # Optional DAG engine (movie-request / tv-request → real mesh RPCs via meta.method).
    if [[ "${MVP_ENABLE_WORKFLOW_TAPESTRY:-0}" == "1" ]]; then
      if [[ ! -x "$BIN/workflow-tapestry" ]]; then
        echo "building workflow-tapestry"
        (cd "$WS/workflow-tapestry" && go build -o "$BIN/workflow-tapestry" ./cmd/module)
      fi
      maybe_start workflow-tapestry env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=workflow-tapestry MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        WORKFLOW_GRPC_ADDR=":9603" \
        "$BIN/workflow-tapestry"
    fi

    # Optional import-list sync (Trakt/IMDb/Plex/Jellyfin/Radarr/Sonarr) — :9530
    if [[ "${MVP_ENABLE_MEDIA_LIST_SYNC:-0}" == "1" ]]; then
      if [[ ! -x "$BIN/media-list-sync" ]]; then
        echo "building media-list-sync"
        (cd "$WS/media-list-sync" && go build -o "$BIN/media-list-sync" ./cmd/module)
      fi
      mkdir -p "$DATA/listsync"
      maybe_start media-list-sync env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-list-sync MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        LISTSYNC_GRPC_ADDR=":9530" \
        LISTSYNC_DB_PATH="$DATA/listsync/listsync.db" \
        "$BIN/media-list-sync"
    fi

    # Optional FFmpeg transcoder (:9525) for media-transcode workflow DAG
    if [[ "${MVP_ENABLE_MEDIA_TRANSCODER:-0}" == "1" ]]; then
      if [[ ! -x "$BIN/media-transcoder" ]]; then
        echo "building media-transcoder"
        (cd "$WS/media-transcoder" && go build -o "$BIN/media-transcoder" ./cmd/module)
      fi
      mkdir -p "$DATA/transcoder"
      maybe_start media-transcoder env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-transcoder MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        TRANSCODER_GRPC_ADDR=":9525" \
        TRANSCODER_DB_PATH="$DATA/transcoder/transcoder.db" \
        TRANSCODER_MAX_CONCURRENT="${TRANSCODER_MAX_CONCURRENT:-2}" \
        "$BIN/media-transcoder"
    fi

    # Optional Apprise notification peer (:9445). Soft sink via WEBHOOK_URL when no Apprise URLs set
    # (health requires ≥1 channel). Does not replace notification-default.
    if [[ "${MVP_ENABLE_NOTIFICATION_APPRISE:-0}" == "1" ]]; then
      if [[ ! -x "$BIN/notification-apprise" ]]; then
        echo "building notification-apprise"
        (cd "$WS/notification-apprise" && go build -o "$BIN/notification-apprise" ./cmd/module)
      fi
      maybe_start notification-apprise env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=notification-apprise MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        NOTIFY_GRPC_ADDR=":9445" \
        APPRISE_URL="${APPRISE_URL:-}" \
        APPRISE_URLS="${APPRISE_URLS:-}" \
        APPRISE_TOKEN="${APPRISE_TOKEN:-}" \
        DISCORD_WEBHOOK="${DISCORD_WEBHOOK:-}" \
        SLACK_WEBHOOK="${SLACK_WEBHOOK:-}" \
        WEBHOOK_URL="${APPRISE_WEBHOOK_URL:-${NOTIFY_WEBHOOK_URL:-http://127.0.0.1:9/muxcore-apprise-sink}}" \
        "$BIN/notification-apprise"
    fi

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
        maybe_start media-ui env \
          MEDIA_UI_LISTEN="${MEDIA_UI_LISTEN:-:5173}" \
          MEDIA_UI_DIST="$UI_DIST" \
          MEDIA_UI_REQUIRE_AUTH="${MEDIA_UI_REQUIRE_AUTH:-1}" \
          MEDIA_UI_PUBLIC_URL="${MEDIA_UI_PUBLIC_URL:-https://media.gringotts}" \
          AUTH_HTTP_URL="${AUTH_HTTP_URL:-https://auth.gringotts}" \
          AUTH_HTTP_INTERNAL_URL="${AUTH_HTTP_INTERNAL_URL:-http://127.0.0.1:9401}" \
          MOVIES_GRPC_CLIENT_ADDR="127.0.0.1:9420" \
          TVSHOWS_GRPC_CLIENT_ADDR="127.0.0.1:9440" \
          MOVIES_HTTP_URL="http://127.0.0.1:9430" \
          TVSHOWS_HTTP_URL="http://127.0.0.1:9450" \
          REQUEST_MEDIA_HTTP_URL="http://127.0.0.1:9380" \
          "$BIN/mediauiprox" \
            -listen "${MEDIA_UI_LISTEN:-:5173}" \
            -dist "$UI_DIST" \
            -request-http "http://127.0.0.1:9380" \
            -auth-http "${AUTH_HTTP_URL:-https://auth.gringotts}" \
            -auth-http-internal "${AUTH_HTTP_INTERNAL_URL:-http://127.0.0.1:9401}" \
            -public-url "${MEDIA_UI_PUBLIC_URL:-https://media.gringotts}"
      fi
    fi

    if [[ -n "${START_ONLY:-}" ]]; then
      echo "restarted $START_ONLY. logs in $RUN/$START_ONLY.log"
    else
      echo "started. logs in $RUN ; SMOKE_API_URL=http://127.0.0.1:18080 ./smoke.sh"
    fi
    ;;
  *)
    echo "usage: $0 {up|stop|stop-one <name>|restart <name>|unregister <id>}" >&2
    exit 2
    ;;
esac
