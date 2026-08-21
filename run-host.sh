#!/usr/bin/env bash
# Host-process MVP runner (used when Docker is unavailable).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
WS="$(cd "$ROOT/.." && pwd)"
BIN="$ROOT/bin"
RUN="$ROOT/run"
DATA="$ROOT/data"

# Load $ROOT/.env defaults without clobbering env already set (systemd/nix on vault).
load_env_file() {
  local f="$1"
  [[ -f "$f" ]] || return 0
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%%#*}"
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    [[ -z "$line" ]] && continue
    [[ "$line" != *=* ]] && continue
    local key="${line%%=*}"
    local val="${line#*=}"
    key="${key%"${key##*[![:space:]]}"}"
    val="${val#"${val%%[![:space:]]*}"}"
    val="${val%"${val##*[![:space:]]}"}"
    val="${val%\"}"; val="${val#\"}"
    val="${val%\'}"; val="${val#\'}"
    [[ -z "$key" ]] && continue
    if [[ -z "${!key:-}" ]]; then
      export "$key=$val"
    fi
  done <"$f"
}
load_env_file "$ROOT/.env"

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
  # Skip edge/VPN peers managed by separate units (Caddy, indexer+torrent in muxcore-vpn).
  if [[ -d "$BIN" ]]; then
    for bin in "$BIN"/*; do
      [[ -x "$bin" && -f "$bin" ]] || continue
      base=$(basename "$bin")
      case "$base" in
        caddy|indexer-piratebay|downloader-native-torrent) continue ;;
      esac
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
        MUXCORE_STORAGE_DIR="${MUXCORE_STORAGE_DIR:-$DATA/storage}" \
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
      ADMIN_UI_PUBLIC_URL="${ADMIN_UI_PUBLIC_URL:-}" \
      MEDIA_UI_PUBLIC_URL="${MEDIA_UI_PUBLIC_URL:-}" \
      AUTH_ALLOWED_REDIRECT_HOSTS="${AUTH_ALLOWED_REDIRECT_HOSTS:-}" \
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

    # Spool `default` required: fail-open until RATELIMIT_ENABLED=true.
    if [[ ! -x "$BIN/ratelimit-tokenbucket" ]]; then
      echo "building ratelimit-tokenbucket"
      (cd "$WS/ratelimit-tokenbucket" && go build -o "$BIN/ratelimit-tokenbucket" ./cmd/module)
    fi
    maybe_start ratelimit-tokenbucket env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=ratelimit-tokenbucket MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      RATELIMIT_ENABLED="${RATELIMIT_ENABLED:-false}" \
      RATELIMIT_RATE="${RATELIMIT_RATE:-100}" \
      RATELIMIT_BURST="${RATELIMIT_BURST:-200}" \
      "$BIN/ratelimit-tokenbucket"

    # Spool `default` required cache peer. Needs Redis; enable with MVP_ENABLE_CACHE_REDIS=1
    # Bootstrap order when REDIS_ADDR unset: existing :6379 → podman redis:7-alpine → redis-server on PATH.
    if [[ "${MVP_ENABLE_CACHE_REDIS:-0}" == "1" || -n "${REDIS_ADDR:-}" ]]; then
      if [[ -z "${REDIS_ADDR:-}" ]]; then
        if ss -lptn 2>/dev/null | grep -q ':6379'; then
          REDIS_ADDR="127.0.0.1:6379"
        elif command -v podman >/dev/null 2>&1; then
          if ! podman ps --format '{{.Names}}' 2>/dev/null | grep -qx 'muxcore-redis'; then
            echo "starting muxcore-redis (podman redis:7-alpine)"
            podman rm -f muxcore-redis >/dev/null 2>&1 || true
            podman run -d --name muxcore-redis -p 6379:6379 redis:7-alpine >/dev/null
          fi
          REDIS_ADDR="127.0.0.1:6379"
        elif command -v redis-server >/dev/null 2>&1; then
          echo "starting redis-server on 127.0.0.1:6379"
          mkdir -p "$DATA/redis"
          if [[ ! -f "$RUN/redis-server.pid" ]] || ! kill -0 "$(cat "$RUN/redis-server.pid")" 2>/dev/null; then
            redis-server --bind 127.0.0.1 --port 6379 --dir "$DATA/redis" --daemonize yes --pidfile "$RUN/redis-server.pid" \
              --logfile "$RUN/redis-server.log" >/dev/null
          fi
          REDIS_ADDR="127.0.0.1:6379"
        else
          echo "WARN: MVP_ENABLE_CACHE_REDIS=1 but no REDIS_ADDR, no listener on :6379, no podman, no redis-server; skipping cache-redis" >&2
        fi
      fi
      if [[ -n "${REDIS_ADDR:-}" ]]; then
        if [[ ! -x "$BIN/cache-redis" ]]; then
          echo "building cache-redis"
          (cd "$WS/cache-redis" && go build -o "$BIN/cache-redis" ./cmd/module)
        fi
        maybe_start cache-redis env \
          MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=cache-redis MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
          REDIS_ADDR="$REDIS_ADDR" \
          REDIS_PASSWORD="${REDIS_PASSWORD:-}" \
          REDIS_DB="${REDIS_DB:-0}" \
          CACHE_GRPC_ADDR="${CACHE_GRPC_ADDR:-:9600}" \
          "$BIN/cache-redis"
      fi
    fi

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
    if [[ ! -x "$BIN/admin-ui" ]]; then
      echo "building admin-ui"
      ver="${ADMIN_UI_VERSION:-0.1.10}"
      (cd "$WS/admin-ui" && go build -ldflags="-s -w -X main.version=${ver}" -o "$BIN/admin-ui" .)
    fi
    mkdir -p "$DATA/media-ui"
    maybe_start admin-ui env \
      ADMIN_UI_ADDR=":8082" \
      ADMIN_UI_CORE_ADDR="$MESH" \
      ADMIN_UI_INSECURE=true \
      ADMIN_UI_AUTH_ADDR="${ADMIN_UI_AUTH_ADDR:-https://auth.gringotts}" \
      ADMIN_UI_AUTH_INTERNAL_ADDR="${ADMIN_UI_AUTH_INTERNAL_ADDR:-http://127.0.0.1:9401}" \
      ADMIN_UI_PUBLIC_URL="${ADMIN_UI_PUBLIC_URL:-https://admin.gringotts}" \
      ADMIN_UI_TRUSTED_PROXIES="${ADMIN_UI_TRUSTED_PROXIES:-127.0.0.1/32,::1/128}" \
      ADMIN_UI_HEALTH_MONITOR_URL="${ADMIN_UI_HEALTH_MONITOR_URL:-http://127.0.0.1:9203}" \
      ADMIN_UI_LIVETV_FILE="${ADMIN_UI_LIVETV_FILE:-$DATA/media-ui/livetv.json}" \
      ADMIN_UI_BRANDING_FILE="${ADMIN_UI_BRANDING_FILE:-$DATA/media-ui/branding.json}" \
      MUXCORE_MESH_DIAL_LOCAL=true \
      "$BIN/admin-ui"

    maybe_start metadata-tmdb env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=metadata-tmdb MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      TMDB_API_KEY="${TMDB_API_KEY:-}" \
      MUXCORE_CFG_TMDB_API_KEY="${MUXCORE_CFG_TMDB_API_KEY:-${TMDB_API_KEY:-}}" \
      TMDB_FIXTURE="${TMDB_FIXTURE:-}" \
      "$BIN/metadata-tmdb"

    if [[ ! -x "$BIN/metadata-musicbrainz" ]]; then
      echo "building metadata-musicbrainz"
      (cd "$WS/metadata-musicbrainz" && go build -o "$BIN/metadata-musicbrainz" ./cmd/module)
    fi
    maybe_start metadata-musicbrainz env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=metadata-musicbrainz MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      MUSICBRAINZ_FIXTURE="${MUSICBRAINZ_FIXTURE:-1}" \
      METADATA_GRPC_ADDR=":9412" \
      "$BIN/metadata-musicbrainz"

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

    MUSIC_LIBRARY_ROOT="${MVP_MUSIC_LIBRARY_ROOT:-$DATA/library/music}"
    mkdir -p "$DATA/music" "$MUSIC_LIBRARY_ROOT"
    if [[ ! -x "$BIN/media-music" ]]; then
      echo "building media-music"
      (cd "$WS/media-music" && go build -o "$BIN/media-music" ./cmd/module)
    fi
    maybe_start media-music env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-music MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      MUXCORE_MESH_DIAL_LOCAL=true \
      MUSIC_DATA_DIR="$DATA/music" MUSIC_LIBRARY_DIR="$MUSIC_LIBRARY_ROOT" \
      MUSIC_GRPC_ADDR=":9640" MUXCORE_HTTP_ADDR=":9641" \
      "$BIN/media-music"

    maybe_start media-automation env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-automation MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      MUXCORE_MESH_DIAL_LOCAL=true \
      AUTOMATION_DB_PATH="$DATA/automation/automation.db" \
      AUTOMATION_GRPC_ADDR=":9460" \
      AUTOMATION_EVENT_SUBSCRIBE_DELAY=1s \
      AUTOMATION_KEEP_STALLED_PARTIALS="${AUTOMATION_KEEP_STALLED_PARTIALS:-false}" \
      AUTOMATION_DOWNLOAD_DIR="${MVP_DOWNLOADS_DIR:-$DATA/downloads}" \
      MVP_DOWNLOADS_DIR="${MVP_DOWNLOADS_DIR:-$DATA/downloads}" \
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
      FORMATS_TRASH_SYNC="${FORMATS_TRASH_SYNC:-true}" \
      FORMATS_TRASH_IMPORT_PROFILES="${FORMATS_TRASH_IMPORT_PROFILES:-true}" \
      FORMATS_TRASH_CACHE_DIR="${FORMATS_TRASH_CACHE_DIR:-$DATA/formats/trash-guides}" \
      FORMATS_TRASH_GUIDES_PATH="${FORMATS_TRASH_GUIDES_PATH:-}" \
      FORMATS_TRASH_SCORE_SET="${FORMATS_TRASH_SCORE_SET:-default}" \
      FORMATS_TRASH_SERVICES="${FORMATS_TRASH_SERVICES:-radarr,sonarr}" \
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

    # Movie dest for the downloads watch dir. TV dest is separate — never nest
    # shows under SCANNER_LIBRARY_ROOT (that produced movies/TV and movies/Other).
    LIBRARY_ROOT="${MVP_LIBRARY_ROOT:-$DATA/library}"
    TV_LIBRARY_ROOT="${MVP_TV_LIBRARY_ROOT:-$DATA/library/tv}"
    MUSIC_LIBRARY_ROOT="${MVP_MUSIC_LIBRARY_ROOT:-$DATA/library/music}"
    DOWNLOADS_DIR="${MVP_DOWNLOADS_DIR:-$DATA/downloads}"
    mkdir -p "$LIBRARY_ROOT" "$TV_LIBRARY_ROOT" "$MUSIC_LIBRARY_ROOT" "$DOWNLOADS_DIR"

    maybe_start media-scanner env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-scanner MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      MUXCORE_MESH_DIAL_LOCAL=true \
      SCANNER_DB_PATH="$DATA/scanner/scanner.db" \
      SCANNER_LIBRARY_ROOT="$LIBRARY_ROOT" \
      SCANNER_TV_LIBRARY_ROOT="$TV_LIBRARY_ROOT" \
      SCANNER_MUSIC_LIBRARY_ROOT="$MUSIC_LIBRARY_ROOT" \
      SCANNER_DEFAULT_WATCH_DIR="$DOWNLOADS_DIR" \
      SCANNER_GRPC_ADDR=":9470" \
      SCANNER_IMPORT_MODE=copy \
      SCANNER_MIN_VIDEO_BYTES=0 \
      "$BIN/media-scanner"

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
      USERDATA_SYNC="${USERDATA_SYNC:-0}" \
      USERDATA_LOCAL_URL="${USERDATA_LOCAL_URL:-}" \
      USERDATA_PUSH_TO_JELLYFIN="${USERDATA_PUSH_TO_JELLYFIN:-0}" \
      "$BIN/jellyfin"

    # Optional household userdata mesh (HTTP :9672) — prefer with USERDATA_LOCAL_URL for mediauiprox/jellyfin.
    if [[ "${MVP_ENABLE_USERDATA_LOCAL:-0}" == "1" ]]; then
      if [[ ! -x "$BIN/userdata-local" ]]; then
        echo "building userdata-local"
        (cd "$WS/userdata-local" && go build -o "$BIN/userdata-local" ./cmd/module)
      fi
      mkdir -p "$DATA/userdata"
      maybe_start userdata-local env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=userdata-local MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        USERDATA_LOCAL_HTTP_ADDR=":9672" \
        USERDATA_LOCAL_GRPC_ADDR=":9673" \
        USERDATA_LOCAL_DATA_DIR="$DATA/userdata" \
        "$BIN/userdata-local"
      export USERDATA_LOCAL_URL="${USERDATA_LOCAL_URL:-http://127.0.0.1:9672}"
    fi

    # Optional qBittorrent WebUI peer (:9462) — fixture by default; set QBIT_URL for live.
    if [[ "${MVP_ENABLE_DOWNLOADER_QBITTORRENT:-0}" == "1" ]]; then
      if [[ ! -x "$BIN/downloader-qbittorrent" ]]; then
        echo "building downloader-qbittorrent"
        (cd "$WS/downloader-qbittorrent" && go build -o "$BIN/downloader-qbittorrent" ./cmd/module)
      fi
      maybe_start downloader-qbittorrent env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=downloader-qbittorrent MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        QBIT_GRPC_ADDR=":9462" QBIT_HTTP_ADDR=":9463" \
        DOWNLOADER_ENGINE="${DOWNLOADER_ENGINE:-fixture}" \
        QBIT_FIXTURE="${QBIT_FIXTURE:-1}" \
        QBIT_URL="${QBIT_URL:-}" \
        QBIT_USERNAME="${QBIT_USERNAME:-}" \
        QBIT_PASSWORD="${QBIT_PASSWORD:-}" \
        "$BIN/downloader-qbittorrent"
    fi

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

    # Optional FFmpeg transcoder (gRPC :9525, playback HTTP :9526) for library DAG + media-ui on-the-fly playback
    _enable_transcoder="${MVP_ENABLE_MEDIA_TRANSCODER:-}"
    if [[ -z "$_enable_transcoder" && "${MVP_ENABLE_MEDIA_UI:-1}" != "0" ]]; then
      _enable_transcoder=1
    fi
    if [[ "$_enable_transcoder" == "1" ]]; then
      if [[ ! -x "$BIN/media-transcoder" ]]; then
        echo "building media-transcoder"
        (cd "$WS/media-transcoder" && go build -o "$BIN/media-transcoder" ./cmd/module)
      fi
      # Health requires ffmpeg on PATH. Prefer $BIN/ffmpeg (symlink) so nix/host installs work.
      if ! command -v ffmpeg >/dev/null 2>&1 && [[ ! -x "$BIN/ffmpeg" ]] && command -v nix >/dev/null 2>&1; then
        echo "linking ffmpeg from nix into $BIN"
        ff=$(nix shell nixpkgs#ffmpeg --command bash -c 'command -v ffmpeg' 2>/dev/null || true)
        if [[ -n "${ff:-}" && -x "$ff" ]]; then
          ln -sfn "$ff" "$BIN/ffmpeg"
        fi
      fi
      mkdir -p "$DATA/transcoder"
      maybe_start media-transcoder env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-transcoder MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        PATH="$BIN:${PATH}" \
        TRANSCODER_GRPC_ADDR=":9525" \
        TRANSCODER_HTTP_ADDR=":9526" \
        TRANSCODER_DB_PATH="$DATA/transcoder/transcoder.db" \
        TRANSCODER_MAX_CONCURRENT="${TRANSCODER_MAX_CONCURRENT:-2}" \
        "$BIN/media-transcoder"
    fi

    # Optional DLNA/UPnP media server (:9750 HTTP, gRPC :9751, health :8751).
    # Serves MVP library paths to smart TVs and DLNA renderers on the LAN.
    if [[ "${MVP_ENABLE_MEDIA_DLNA:-0}" == "1" ]]; then
      if [[ ! -x "$BIN/media-dlna" ]]; then
        echo "building media-dlna"
        (cd "$WS/media-dlna" && go build -o "$BIN/media-dlna" ./cmd/module)
      fi
      mkdir -p "$DATA/dlna"
      maybe_start media-dlna env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-dlna MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        PATH="$BIN:${PATH}" \
        DLNA_GRPC_ADDR=":9751" \
        DLNA_HEALTH_HTTP_ADDR=":8751" \
        DLNA_HTTP_ADDR=":9750" \
        DLNA_MEDIA_PATH="${DLNA_MEDIA_PATH:-$LIBRARY_ROOT}" \
        DLNA_FRIENDLY_NAME="${DLNA_FRIENDLY_NAME:-MuxCore DLNA}" \
        DLNA_PROBE_CACHE_PATH="$DATA/dlna/ffprobe-cache.json" \
        "$BIN/media-dlna"
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

    # Optional unified media graph (:9730) — SQLite-backed cross-media nodes/edges
    if [[ "${MVP_ENABLE_MEDIA_GRAPH:-0}" == "1" ]]; then
      if [[ ! -x "$BIN/media-graph" ]]; then
        echo "building media-graph"
        (cd "$WS/media-graph" && go build -o "$BIN/media-graph" ./cmd/module)
      fi
      mkdir -p "$DATA/graph"
      maybe_start media-graph env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-graph MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        GRAPH_GRPC_ADDR=":9730" \
        MUXCORE_HTTP_ADDR=":9731" \
        GRAPH_DB_PATH="$DATA/graph/graph.db" \
        GRAPH_AUTO_LINK="${GRAPH_AUTO_LINK:-true}" \
        GRAPH_INGEST_ENABLED="${GRAPH_INGEST_ENABLED:-true}" \
        GRAPH_INGEST_INTERVAL="${GRAPH_INGEST_INTERVAL:-15m}" \
        MUXCORE_MESH_DIAL_LOCAL=true \
        "$BIN/media-graph"
    fi

    # Optional content tagging (:9700)
    if [[ "${MVP_ENABLE_MEDIA_TAGGING:-0}" == "1" ]]; then
      if [[ ! -x "$BIN/media-tagging" ]]; then
        echo "building media-tagging"
        (cd "$WS/media-tagging" && go build -o "$BIN/media-tagging" ./cmd/module)
      fi
      maybe_start media-tagging env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-tagging MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        TAGGING_GRPC_ADDR=":9740" \
        MUXCORE_HTTP_ADDR=":9741" \
        "$BIN/media-tagging"
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
          MEDIA_UI_USERDATA_DIR="${MEDIA_UI_USERDATA_DIR:-$DATA/media-ui}" \
          MEDIA_UI_LIVETV_FILE="${MEDIA_UI_LIVETV_FILE:-$DATA/media-ui/livetv.json}" \
          MEDIA_UI_LIBRARY_PATHS_FILE="${MEDIA_UI_LIBRARY_PATHS_FILE:-$DATA/media-ui/library-paths.json}" \
          AUTH_HTTP_URL="${AUTH_HTTP_URL:-https://auth.gringotts}" \
          AUTH_HTTP_INTERNAL_URL="${AUTH_HTTP_INTERNAL_URL:-http://127.0.0.1:9401}" \
          USERDATA_LOCAL_URL="${USERDATA_LOCAL_URL:-}" \
          MOVIES_GRPC_CLIENT_ADDR="127.0.0.1:9420" \
          TVSHOWS_GRPC_CLIENT_ADDR="127.0.0.1:9440" \
          JELLYFIN_GRPC_CLIENT_ADDR="127.0.0.1:9475" \
          MOVIES_HTTP_URL="http://127.0.0.1:9430" \
          TVSHOWS_HTTP_URL="http://127.0.0.1:9450" \
          REQUEST_MEDIA_HTTP_URL="http://127.0.0.1:9380" \
          SUBTITLES_GRPC_CLIENT_ADDR="127.0.0.1:9520" \
          SUBTITLES_HTTP_URL="http://127.0.0.1:9521" \
          TRANSCODER_HTTP_URL="http://127.0.0.1:9526" \
          "$BIN/mediauiprox" \
            -listen "${MEDIA_UI_LISTEN:-:5173}" \
            -dist "$UI_DIST" \
            -userdata-dir "${MEDIA_UI_USERDATA_DIR:-$DATA/media-ui}" \
            -livetv-file "${MEDIA_UI_LIVETV_FILE:-$DATA/media-ui/livetv.json}" \
            -library-paths-file "${MEDIA_UI_LIBRARY_PATHS_FILE:-$DATA/media-ui/library-paths.json}" \
            -request-http "http://127.0.0.1:9380" \
            -auth-http "${AUTH_HTTP_URL:-https://auth.gringotts}" \
            -auth-http-internal "${AUTH_HTTP_INTERNAL_URL:-http://127.0.0.1:9401}" \
            -public-url "${MEDIA_UI_PUBLIC_URL:-https://media.gringotts}" \
            -transcoder-http "http://127.0.0.1:9526"
      fi
    fi

    if [[ -n "${START_ONLY:-}" ]]; then
      echo "restarted $START_ONLY. logs in $RUN/$START_ONLY.log"
    else
      "$ROOT/scripts/write-view-me.sh" || true
      echo "started. logs in $RUN ; see $RUN/VIEW-ME.txt ; SMOKE_API_URL=http://127.0.0.1:18080 ./smoke.sh"
    fi
    ;;
  *)
    echo "usage: $0 {up|stop|stop-one <name>|restart <name>|unregister <id>}" >&2
    exit 2
    ;;
esac
