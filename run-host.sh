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

# WireGuard env for acquisition peers (indexer HTTP + live torrent). Empty when WG_CONF unset.
ACQ_VPN_ENV=()
if [[ -n "${WG_CONF:-}" ]]; then
  ACQ_VPN_ENV+=(WG_CONF="$WG_CONF")
  ACQ_VPN_ENV+=(WG_USE_WG_QUICK="${WG_USE_WG_QUICK:-0}")
  ACQ_VPN_ENV+=(WG_KILL_SWITCH="${WG_KILL_SWITCH:-false}")
  [[ -n "${DOWNLOADER_REQUIRE_VPN:-}" ]] && ACQ_VPN_ENV+=(DOWNLOADER_REQUIRE_VPN="$DOWNLOADER_REQUIRE_VPN")
fi

# Dev default is insecure mesh TLS unless MUXCORE_REQUIRE_TLS=1 (vault/production).
# Explicit MUXCORE_INSECURE_DISABLE_TLS=true or MUXCORE_PROFILE=dev keeps local convenience.
if [[ "${MUXCORE_REQUIRE_TLS:-}" == "1" ]] || [[ "${MUXCORE_PROFILE:-}" == "staging" ]]; then
  unset MUXCORE_INSECURE_DISABLE_TLS 2>/dev/null || true
elif [[ "${MUXCORE_INSECURE_DISABLE_TLS:-}" == "true" ]] || [[ "${MUXCORE_PROFILE:-dev}" == "dev" ]]; then
  export MUXCORE_INSECURE_DISABLE_TLS=true
fi
export MUXCORE_LOG_LEVEL="${MUXCORE_LOG_LEVEL:-info}"
export MUXCORE_CONFIG="${MUXCORE_CONFIG:-$ROOT/muxcore.json}"
MESH="${MUXCORE_MESH_ADDR:-127.0.0.1:9090}"
MODULE_CERT_ROOT="${MUXCORE_MODULE_CERT_DIR:-$ROOT/tls/module-certs}"

mkdir -p "$BIN" "$RUN" "$DATA"/{movies,tvshows,automation,scanner,roots,sqlite,secrets,library/tv,storage,auth,jellyfin,downloads,request,formats,rename,ffprobe,subtitles,backup,audiobooks,books,comics,intro-outro,transcoder-pool,graph,tagging,listsync,workflow,maintainer,playback-guard,playback-monitor,locks,schemas,userdata,feature-flags,dlna,plex,emby}

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
      case "$base" in
        caddy) continue ;;
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

# Remove pidfiles whose processes are no longer running (safe after crash or kill -9).
cleanup_stale() {
  local removed=0
  for f in "$RUN"/*.pid; do
    [[ -f "$f" ]] || continue
    local name pid
    name=$(basename "$f" .pid)
    pid=$(cat "$f")
    if kill -0 "$pid" 2>/dev/null; then
      printf '  keep %s (pid %s running)\n' "$name" "$pid"
    else
      rm -f "$f"
      printf '  removed stale %s (pid %s)\n' "$name" "$pid"
      removed=$((removed + 1))
    fi
  done
  if [[ "$removed" -eq 0 ]]; then
    echo "cleanup-stale: no stale pidfiles"
  else
    echo "cleanup-stale: removed $removed stale pidfile(s)"
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

# Origin-pinned acquisition modules (see docs/ACQUISITION.md). Set MVP_ALLOW_ADHOC_BUILD=1 for local go build fallback.
ensure_origin_module() {
  local name="$1"
  if [[ "${MVP_ALLOW_ADHOC_BUILD:-0}" == "1" ]]; then
    if [[ ! -x "$BIN/$name" ]]; then
      echo "building $name (MVP_ALLOW_ADHOC_BUILD=1)"
      (cd "$WS/$name" && go build -ldflags="-s -w" -o "$BIN/$name" ./cmd/module)
    fi
    return 0
  fi
  if [[ ! -x "$BIN/$name" ]]; then
    echo "installing origin-pinned $name"
    "$ROOT/scripts/install-origin-module.sh" "$name" --dest "$BIN"
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
  status)
    echo "MVP: $ROOT"
    echo "mesh: $MESH  profile=${MUXCORE_PROFILE:-dev}  tls_insecure=${MUXCORE_INSECURE_DISABLE_TLS:-}"
    echo "--- processes (pidfiles) ---"
    found=0
    stale=0
    for f in "$RUN"/*.pid; do
      [[ -f "$f" ]] || continue
      name=$(basename "$f" .pid)
      pid=$(cat "$f")
      if kill -0 "$pid" 2>/dev/null; then
        printf '  %-28s pid=%s running\n' "$name" "$pid"
        found=1
      else
        printf '  %-28s pid=%s stale\n' "$name" "$pid"
        stale=$((stale + 1))
      fi
    done
    [[ "$found" -eq 1 ]] || echo "  (no running pidfiles)"
    if [[ "$stale" -gt 0 ]]; then
      echo "  hint: ./run-host.sh cleanup-stale  ($stale stale pidfile(s))"
    fi
    echo "--- health (HTTP) ---"
    probe() {
      local label="$1" url="$2"
      local code
      code=$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 2 "$url" 2>/dev/null || echo 000)
      printf '  %-12s %s → %s\n' "$label" "$url" "$code"
    }
    if [[ "${MUXCORE_PROFILE:-}" == "staging" ]]; then
      probe core 'https://127.0.0.1:8080/health'
    else
      probe core 'http://127.0.0.1:8080/health'
    fi
    probe admin 'http://127.0.0.1:8082/health'
    probe media 'http://127.0.0.1:5173/'
    probe auth 'http://127.0.0.1:9401/login'
    probe api-rest 'http://127.0.0.1:18080/health'
    if [[ -x "$BIN/muxcorectl" ]]; then
      echo "--- mesh (muxcorectl) ---"
      env MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MESH_DIAL_LOCAL=true MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-true}" \
        "$BIN/muxcorectl" modules list 2>/dev/null | head -20 || echo "  (muxcorectl modules list failed)"
    fi
    exit 0
    ;;
  cleanup-stale|cleanup_stale)
    cleanup_stale
    exit 0
    ;;
  help|-h|--help)
    cat <<EOF
MuxCore MVP host runner — $ROOT

Commands:
  up                 Start full stack (or one service when START_ONLY is set)
  stop               Stop all sidecars
  stop-one <name>    Stop one service (preferred before binary swap)
  restart <name>     stop-one + up for a single service
  status             Pidfiles, HTTP health, muxcorectl modules (when available)
  cleanup-stale      Remove pidfiles for processes that are no longer running
  unregister <id>    Remove module from mesh registry
  help               This text

Paths:
  bin/     $BIN
  run/     $RUN  (logs, pidfiles, admin.token, VIEW-ME.txt)
  data/    $DATA

Vault deploy (from umbrella workspace):
  scripts/deploy-module-to-vault.sh --list
  scripts/deploy-module-to-vault.sh <module> --verify-public
  scripts/muxcorectl-vault.sh health status
  scripts/smoke-vault-health.sh
  scripts/smoke-vault-public.sh
  scripts/smoke-vault-all.sh
  scripts/preflight-vault-ssh.sh

Docs: ../AGENTS.md, PORTS.md, docs/ACQUISITION.md
EOF
    exit 0
    ;;
  up)
    if [[ -z "${START_ONLY:-}" ]]; then
      stop_all
    fi
    # Core is started here, not via maybe_start; restart core must re-launch muxcored after stop-one.
    if [[ -z "${START_ONLY:-}" || "$START_ONLY" == "core" ]]; then
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
    elif [[ -n "${START_ONLY:-}" ]]; then
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

    call_policy_dev_file=""
    if [[ "${MUXCORE_PROFILE:-dev}" == "dev" && "${MUXCORE_REQUIRE_TLS:-}" != "1" ]]; then
      call_policy_dev_file="$WS/call-policy-default/policies-dev.yaml"
    fi
    maybe_start call-policy-default env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=call-policy-default MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      CALL_POLICY_FILE="$WS/call-policy-default/policies.yaml" \
      CALL_POLICY_DEV_FILE="$call_policy_dev_file" \
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
    if [[ "${MUXCORE_REQUIRE_TLS:-}" == "1" ]] || [[ "${MUXCORE_PROFILE:-}" == "staging" ]]; then
      RATELIMIT_ENABLED="${RATELIMIT_ENABLED:-true}"
    else
      RATELIMIT_ENABLED="${RATELIMIT_ENABLED:-false}"
    fi
    maybe_start ratelimit-tokenbucket env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=ratelimit-tokenbucket MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      RATELIMIT_ENABLED="$RATELIMIT_ENABLED" \
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
      ADMIN_UI_DATA_DIR="${ADMIN_UI_DATA_DIR:-$DATA/media-ui}" \
      ADMIN_UI_AUTH_ADDR="${ADMIN_UI_AUTH_ADDR:-https://auth.gringotts}" \
      ADMIN_UI_AUTH_INTERNAL_ADDR="${ADMIN_UI_AUTH_INTERNAL_ADDR:-http://127.0.0.1:9401}" \
      ADMIN_UI_PUBLIC_URL="${ADMIN_UI_PUBLIC_URL:-https://admin.gringotts}" \
      ADMIN_UI_TRUSTED_PROXIES="${ADMIN_UI_TRUSTED_PROXIES:-127.0.0.1/32,::1/128}" \
      ADMIN_UI_HEALTH_MONITOR_URL="${ADMIN_UI_HEALTH_MONITOR_URL:-http://127.0.0.1:9203}" \
      ADMIN_UI_LIVETV_FILE="${ADMIN_UI_LIVETV_FILE:-$DATA/media-ui/livetv.json}" \
      ADMIN_UI_BRANDING_FILE="${ADMIN_UI_BRANDING_FILE:-$DATA/media-ui/branding.json}" \
      ADMIN_UI_NETWORKING_FILE="${ADMIN_UI_NETWORKING_FILE:-$DATA/media-ui/networking.json}" \
      ADMIN_UI_PARENTAL_FILE="${ADMIN_UI_PARENTAL_FILE:-$DATA/media-ui/parental.json}" \
      ADMIN_UI_PLAYBACK_FILE="${ADMIN_UI_PLAYBACK_FILE:-$DATA/media-ui/playback.json}" \
      ADMIN_UI_PASSWORD_RESET_FILE="${ADMIN_UI_PASSWORD_RESET_FILE:-$DATA/media-ui/password-resets.json}" \
      ADMIN_UI_SESSION_FILE="${ADMIN_UI_SESSION_FILE:-$DATA/media-ui/sessions.json}" \
      ADMIN_UI_USERDATA_URL="${ADMIN_UI_USERDATA_URL:-http://127.0.0.1:9672}" \
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
      METADATA_GRPC_ADDR=":9413" \
      "$BIN/metadata-musicbrainz"

    maybe_start media-movies env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-movies MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      MUXCORE_MESH_DIAL_LOCAL=true \
      MOVIES_DB_PATH="$DATA/movies/movies.db" MOVIES_IMAGE_DIR="$DATA/movies/images" \
      MOVIES_HTTP_ADDR="127.0.0.1:9430" \
      "$BIN/media-movies"

    maybe_start media-tvshows env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-tvshows MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      MUXCORE_MESH_DIAL_LOCAL=true \
      TVSHOWS_DB_PATH="$DATA/tvshows/tvshows.db" TVSHOWS_IMAGE_DIR="$DATA/tvshows/images" \
      TVSHOWS_GRPC_ADDR=":9440" TVSHOWS_HTTP_ADDR="127.0.0.1:9450" \
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

    ensure_origin_module media-automation
    maybe_start media-automation env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-automation MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      MUXCORE_MESH_DIAL_LOCAL=true \
      AUTOMATION_DB_PATH="$DATA/automation/automation.db" \
      AUTOMATION_GRPC_ADDR=":9460" \
      AUTOMATION_EVENT_SUBSCRIBE_DELAY=1s \
      AUTOMATION_KEEP_STALLED_PARTIALS="${AUTOMATION_KEEP_STALLED_PARTIALS:-false}" \
      AUTOMATION_DOWNLOAD_DIR="${MVP_DOWNLOADS_DIR:-$DATA/downloads}" \
      MVP_DOWNLOADS_DIR="${MVP_DOWNLOADS_DIR:-$DATA/downloads}" \
      DOWNLOADER_ENGINE="${DOWNLOADER_ENGINE:-fixture}" \
      AUTOMATION_DOWNLOADER_TORRENT="${AUTOMATION_DOWNLOADER_TORRENT:-downloader-native-torrent}" \
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

    ensure_origin_module media-scanner
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
      REQUEST_HTTP_ADDR="127.0.0.1:9380" \
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

    # Optional Jellyfin bridge — off by default; standalone Jellyfin at media.zem.systems is separate.
    if [[ "${MVP_ENABLE_JELLYFIN:-0}" == "1" ]]; then
      maybe_start jellyfin env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=jellyfin MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        JELLYFIN_GRPC_ADDR=":9475" JELLYFIN_HTTP_ADDR=":8475" \
        JELLYFIN_DATA_DIR="$DATA/jellyfin" \
        JELLYFIN_BASE_URL="${JELLYFIN_BASE_URL:-}" \
        JELLYFIN_API_KEY="${JELLYFIN_API_KEY:-}" \
        JELLYFIN_WEBHOOK_SECRET="${JELLYFIN_WEBHOOK_SECRET:-}" \
        USERDATA_SYNC="${USERDATA_SYNC:-1}" \
        USERDATA_LOCAL_URL="${USERDATA_LOCAL_URL:-http://127.0.0.1:9672}" \
        USERDATA_PUSH_TO_JELLYFIN="${USERDATA_PUSH_TO_JELLYFIN:-0}" \
        "$BIN/jellyfin"
    fi

    # Optional household userdata mesh (HTTP :9672) — default on; BFF + jellyfin prefer USERDATA_LOCAL_URL.
    if [[ "${MVP_ENABLE_USERDATA_LOCAL:-1}" == "1" ]]; then
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

    # Optional native torrent peer (:9461) — fixture by default; VPN required for live engine.
    if [[ "${MVP_ENABLE_DOWNLOADER_TORRENT:-0}" == "1" ]]; then
      ensure_origin_module downloader-native-torrent
      maybe_start downloader-native-torrent env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=downloader-native-torrent MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        DOWNLOADER_GRPC_ADDR=":9461" MUXCORE_HTTP_ADDR=":9464" \
        DOWNLOADER_ENGINE="${DOWNLOADER_ENGINE:-fixture}" \
        DOWNLOAD_DIR="${MVP_DOWNLOADS_DIR:-$DATA/downloads}" \
        "${ACQ_VPN_ENV[@]}" \
        "$BIN/downloader-native-torrent"
    fi

    # Optional native usenet peer (:9622) — fixture by default; NNTP required for live engine.
    if [[ "${MVP_ENABLE_DOWNLOADER_USENET:-0}" == "1" ]]; then
      if [[ ! -x "$BIN/downloader-native-usenet" ]]; then
        echo "building downloader-native-usenet"
        (cd "$WS/downloader-native-usenet" && go build -o "$BIN/downloader-native-usenet" ./cmd/module)
      fi
      maybe_start downloader-native-usenet env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=downloader-native-usenet MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        USENET_GRPC_ADDR=":9622" MUXCORE_HTTP_ADDR=":9623" \
        USENET_ENGINE="${USENET_ENGINE:-fixture}" \
        USENET_DOWNLOAD_DIR="${MVP_DOWNLOADS_DIR:-$DATA/downloads}/usenet" \
        USENET_PAR2="${USENET_PAR2:-auto}" USENET_UNPACK="${USENET_UNPACK:-auto}" \
        NNTP_HOST="${NNTP_HOST:-}" NNTP_PORT="${NNTP_PORT:-}" NNTP_USER="${NNTP_USER:-}" NNTP_PASS="${NNTP_PASS:-}" NNTP_SSL="${NNTP_SSL:-}" \
        "${ACQ_VPN_ENV[@]}" \
        "$BIN/downloader-native-usenet"
    fi

    # Optional SABnzbd usenet bridge (:9620) — requires SABNZBD_URL + SABNZBD_API_KEY.
    if [[ "${MVP_ENABLE_DOWNLOADER_SABNZBD:-0}" == "1" ]]; then
      if [[ ! -x "$BIN/downloader-sabnzbd" ]]; then
        echo "building downloader-sabnzbd"
        (cd "$WS/downloader-sabnzbd" && go build -o "$BIN/downloader-sabnzbd" ./cmd/module)
      fi
      maybe_start downloader-sabnzbd env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=downloader-sabnzbd MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        MUXCORE_GRPC_ADDR_OVERRIDE=":9620" MUXCORE_HTTP_ADDR=":9621" \
        SABNZBD_URL="${SABNZBD_URL:-}" SABNZBD_API_KEY="${SABNZBD_API_KEY:-}" \
        "${ACQ_VPN_ENV[@]}" \
        "$BIN/downloader-sabnzbd"
    fi

    # Optional indexer-piratebay (:9485)
    if [[ "${MVP_ENABLE_INDEXER_PIRATEBAY:-0}" == "1" ]]; then
      if [[ ! -x "$BIN/indexer-piratebay" ]]; then
        echo "building indexer-piratebay"
        (cd "$WS/indexer-piratebay" && go build -o "$BIN/indexer-piratebay" ./cmd/module)
      fi
      maybe_start indexer-piratebay env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=indexer-piratebay MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        PIRATEBAY_GRPC_ADDR=":9485" PIRATEBAY_HTTP_ADDR=":9487" \
        INDEXER_FIXTURE="${INDEXER_FIXTURE:-1}" \
        PIRATEBAY_API_BASE="${PIRATEBAY_API_BASE:-}" \
        "${ACQ_VPN_ENV[@]}" \
        "$BIN/indexer-piratebay"
    fi

    # Optional indexer-torznab / newznab (:9486)
    if [[ "${MVP_ENABLE_INDEXER_TORZNAB:-0}" == "1" ]]; then
      if [[ ! -x "$BIN/indexer-torznab" ]]; then
        echo "building indexer-torznab"
        (cd "$WS/indexer-torznab" && go build -o "$BIN/indexer-torznab" ./cmd/module)
      fi
      maybe_start indexer-torznab env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=indexer-torznab MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        TORZNAB_GRPC_ADDR=":9486" \
        TORZNAB_URL="${TORZNAB_URL:-}" TORZNAB_API_KEY="${TORZNAB_API_KEY:-}" \
        PROWLARR_URL="${PROWLARR_URL:-}" PROWLARR_API_KEY="${PROWLARR_API_KEY:-}" \
        "${ACQ_VPN_ENV[@]}" \
        "$BIN/indexer-torznab"
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
        TRANSCODER_HTTP_ADDR="127.0.0.1:9526" \
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

    # Optional content tagging (:9740)
    if [[ "${MVP_ENABLE_MEDIA_TAGGING:-0}" == "1" ]]; then
      if [[ ! -x "$BIN/media-tagging" ]]; then
        echo "building media-tagging"
        (cd "$WS/media-tagging" && go build -o "$BIN/media-tagging" ./cmd/module)
      fi
      maybe_start media-tagging env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-tagging MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        TAGGING_DATA_DIR="$DATA/tagging" \
        TAGGING_GRPC_ADDR="127.0.0.1:9740" \
        MUXCORE_HTTP_ADDR="127.0.0.1:9741" \
        "$BIN/media-tagging"
    fi

    # ── Optional peers (env-gated; vault soak enables non-acquisition set via muxcore-test.nix) ──

    if [[ "${MVP_ENABLE_BACKUP_LOCAL:-0}" == "1" ]]; then
      mkdir -p "$DATA/backup"
      maybe_start backup-local env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=backup-local MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        BACKUP_DIR="$DATA/backup" \
        BACKUP_SOURCE_DIRS="$DATA" \
        "$BIN/backup-local"
    fi

    if [[ "${MVP_ENABLE_MEDIA_AUDIOBOOKS:-0}" == "1" ]]; then
      mkdir -p "$DATA/audiobooks"
      maybe_start media-audiobooks env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-audiobooks MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        AUDIOBOOKS_DATA_DIR="$DATA/audiobooks" \
        MUXCORE_HTTP_ADDR=":9671" \
        "$BIN/media-audiobooks"
    fi

    if [[ "${MVP_ENABLE_MEDIA_BOOKS:-0}" == "1" ]]; then
      mkdir -p "$DATA/books"
      maybe_start media-books env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-books MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        BOOKS_DATA_DIR="$DATA/books" \
        MUXCORE_HTTP_ADDR=":9651" \
        "$BIN/media-books"
    fi

    if [[ "${MVP_ENABLE_MEDIA_COMICS:-0}" == "1" ]]; then
      mkdir -p "$DATA/comics"
      maybe_start media-comics env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-comics MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        COMICS_DATA_DIR="$DATA/comics" \
        MUXCORE_HTTP_ADDR=":9661" \
        "$BIN/media-comics"
    fi

    if [[ "${MVP_ENABLE_MEDIA_INTRO_OUTRO:-0}" == "1" ]]; then
      maybe_start media-intro-outro env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-intro-outro MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        INTRO_OUTRO_DATA_DIR="$DATA/intro-outro" \
        MUXCORE_HTTP_ADDR=":9711" \
        "$BIN/media-intro-outro"
    fi

    if [[ "${MVP_ENABLE_MEDIA_TRANSCODER_POOL:-0}" == "1" ]]; then
      mkdir -p "$DATA/transcoder-pool"
      maybe_start media-transcoder-pool env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-transcoder-pool MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        POOL_DB_PATH="$DATA/transcoder-pool/pool.db" \
        MUXCORE_HTTP_ADDR=":9721" \
        PATH="$BIN:${PATH}" \
        "$BIN/media-transcoder-pool"
    fi

    if [[ "${MVP_ENABLE_STORAGE_S3:-0}" == "1" ]]; then
      maybe_start storage-s3 env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=storage-s3 MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        STORAGE_S3_GRPC_ADDR="127.0.0.1:9610" \
        STORAGE_S3_HTTP_ADDR="127.0.0.1:9611" \
        "$BIN/storage-s3"
    fi

    if [[ "${MVP_ENABLE_STORAGE_CEPH:-0}" == "1" ]]; then
      maybe_start storage-ceph env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=storage-ceph MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        STORAGE_CEPH_GRPC_ADDR=":9680" \
        STORAGE_CEPH_HTTP_ADDR=":9681" \
        "$BIN/storage-ceph"
    fi

    if [[ "${MVP_ENABLE_STORAGE_OVERLAY:-0}" == "1" ]]; then
      maybe_start storage-overlay env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=storage-overlay MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        MUXCORE_MESH_DIAL_LOCAL=true \
        STORAGE_OVERLAY_GRPC_ADDR=":9690" \
        STORAGE_OVERLAY_HTTP_ADDR=":9691" \
        OVERLAY_BACKEND="${OVERLAY_BACKEND:-minio}" \
        OVERLAY_AES_KEY_HEX="${OVERLAY_AES_KEY_HEX:?MVP_ENABLE_STORAGE_OVERLAY requires OVERLAY_AES_KEY_HEX}" \
        OVERLAY_ENCRYPT="${OVERLAY_ENCRYPT:-true}" \
        OVERLAY_COMPRESS="${OVERLAY_COMPRESS:-true}" \
        OVERLAY_DEDUP="${OVERLAY_DEDUP:-true}" \
        OVERLAY_S3_ENDPOINT="${OVERLAY_S3_ENDPOINT:-${S3_ENDPOINT:-127.0.0.1:9000}}" \
        OVERLAY_S3_BUCKET="${OVERLAY_S3_BUCKET:-${S3_BUCKET:-muxcore}}" \
        OVERLAY_S3_ACCESS_KEY="${OVERLAY_S3_ACCESS_KEY:-${S3_ACCESS_KEY:-minioadmin}}" \
        OVERLAY_S3_SECRET_KEY="${OVERLAY_S3_SECRET_KEY:-${S3_SECRET_KEY:-minioadmin}}" \
        OVERLAY_S3_PATH_STYLE="${OVERLAY_S3_PATH_STYLE:-true}" \
        OVERLAY_S3_USE_SSL="${OVERLAY_S3_USE_SSL:-false}" \
        "$BIN/storage-overlay"
    fi

    if [[ "${MVP_ENABLE_MEDIA_LIBRARY_MAINTAINER:-0}" == "1" ]]; then
      mkdir -p "$DATA/maintainer"
      maybe_start media-library-maintainer env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=media-library-maintainer MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        MAINTAINER_DB_PATH="$DATA/maintainer/maintainer.db" \
        MAINTAINER_GRPC_ADDR=":9545" \
        MAINTAINER_USERDATA_DIR="${USERDATA_LOCAL_DATA_DIR:-$DATA/userdata}" \
        "$BIN/media-library-maintainer"
    fi

    if [[ "${MVP_ENABLE_PLAYBACK_GUARD:-0}" == "1" ]]; then
      mkdir -p "$DATA/playback-guard"
      maybe_start playback-guard env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=playback-guard MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        PLAYBACK_GUARD_GRPC_ADDR=":9561" \
        PLAYBACK_GUARD_DB_PATH="$DATA/playback-guard/guard.db" \
        "$BIN/playback-guard"
    fi

    if [[ "${MVP_ENABLE_PLAYBACK_MONITOR:-0}" == "1" ]]; then
      mkdir -p "$DATA/playback-monitor"
      maybe_start playback-monitor env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=playback-monitor MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        PLAYBACK_MONITOR_GRPC_ADDR=":9560" \
        PLAYBACK_MONITOR_HTTP_ADDR=":8560" \
        PLAYBACK_MONITOR_DB_PATH="$DATA/playback-monitor/monitor.db" \
        "$BIN/playback-monitor"
    fi

    if [[ "${MVP_ENABLE_CIRCUITBREAKER_SIMPLE:-0}" == "1" ]]; then
      maybe_start circuitbreaker-simple env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=circuitbreaker-simple MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        CB_GRPC_ADDR=":9645" \
        "$BIN/circuitbreaker-simple"
    fi

    if [[ "${MVP_ENABLE_CONFIG_WATCHER:-0}" == "1" ]]; then
      maybe_start config-watcher env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=config-watcher MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        CONFIG_WATCHER_GRPC_ADDR=":9614" \
        "$BIN/config-watcher"
    fi

    if [[ "${MVP_ENABLE_DATA_REDACTION:-0}" == "1" ]]; then
      maybe_start data-redaction-pattern env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=data-redaction-pattern MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        REDACTION_GRPC_ADDR=":9655" \
        "$BIN/data-redaction-pattern"
    fi

    if [[ "${MVP_ENABLE_DISTRIBUTED_LOCK:-0}" == "1" ]]; then
      mkdir -p "$DATA/locks"
      maybe_start distributed-lock-sqlite env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=distributed-lock-sqlite MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        LOCK_GRPC_ADDR=":9604" \
        LOCK_DB_PATH="$DATA/locks/locks.db" \
        "$BIN/distributed-lock-sqlite"
    fi

    if [[ "${MVP_ENABLE_EXECUTOR_SHELL:-0}" == "1" ]]; then
      maybe_start executor-shell env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=executor-shell MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        EXECUTOR_GRPC_ADDR=":9605" \
        "$BIN/executor-shell"
    fi

    if [[ "${MVP_ENABLE_FEATURE_FLAGS:-0}" == "1" ]]; then
      mkdir -p "$DATA/feature-flags"
      _flags_file="$DATA/feature-flags/flags.yaml"
      [[ -f "$_flags_file" ]] || printf '{}\n' >"$_flags_file"
      maybe_start feature-flags-file env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=feature-flags-file MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        FEATURE_FLAGS_FILE="$_flags_file" \
        "$BIN/feature-flags-file"
    fi

    if [[ "${MVP_ENABLE_INPUT_VALIDATE:-0}" == "1" ]]; then
      mkdir -p "$DATA/schemas"
      maybe_start input-validate-jsonschema env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=input-validate-jsonschema MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        VALIDATE_GRPC_ADDR=":9665" \
        VALIDATE_DATA_DIR="$DATA/schemas" \
        "$BIN/input-validate-jsonschema"
    fi

    if [[ "${MVP_ENABLE_LOGGING_FILE:-0}" == "1" ]]; then
      maybe_start logging-file env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=logging-file MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        LOG_GRPC_ADDR=":9625" \
        LOG_FILE_PATH="$RUN/logging-file.log" \
        "$BIN/logging-file"
    fi

    if [[ "${MVP_ENABLE_METRICS_PROMETHEUS:-0}" == "1" ]]; then
      maybe_start metrics-prometheus env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=metrics-prometheus MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        METRICS_GRPC_ADDR=":9900" \
        METRICS_HTTP_ADDR=":9901" \
        "$BIN/metrics-prometheus"
    fi

    if [[ "${MVP_ENABLE_SCHEDULER_CRON:-0}" == "1" ]]; then
      mkdir -p "$DATA/scheduler-cron"
      maybe_start scheduler-cron env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=scheduler-cron MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        SCHEDULER_HTTP_ADDR="127.0.0.1:9204" \
        SCHEDULER_STORE_PATH="$DATA/scheduler-cron/tasks.json" \
        "$BIN/scheduler-cron"
    fi

    if [[ "${MVP_ENABLE_SERIALIZATION_SAFE:-0}" == "1" ]]; then
      maybe_start serialization-safe env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=serialization-safe MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        SERIALIZATION_GRPC_ADDR=":9635" \
        "$BIN/serialization-safe"
    fi

    if [[ "${MVP_ENABLE_SPOOL_RESOLVER:-0}" == "1" ]]; then
      maybe_start spool-resolver-http env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=spool-resolver-http MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        SPOOL_RESOLVER_GRPC_ADDR="127.0.0.1:9675" \
        SPOOL_RESOLVER_ALLOWED_HOSTS="${SPOOL_RESOLVER_ALLOWED_HOSTS:-github.com,raw.githubusercontent.com}" \
        SPOOL_RESOLVER_ALLOW_HTTP="${SPOOL_RESOLVER_ALLOW_HTTP:-false}" \
        SPOOL_RESOLVER_ALLOW_PRIVATE="${SPOOL_RESOLVER_ALLOW_PRIVATE:-false}" \
        "$BIN/spool-resolver-http"
    fi

    if [[ "${MVP_ENABLE_TRACING_OTLP:-0}" == "1" ]]; then
      maybe_start tracing-otlp env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=tracing-otlp MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        TRACING_GRPC_ADDR=":9613" \
        "$BIN/tracing-otlp"
    fi

    if [[ "${MVP_ENABLE_WORKER_POOL:-0}" == "1" ]]; then
      maybe_start worker-pool-memory env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=worker-pool-memory MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        WORKER_POOL_HTTP_ADDR="127.0.0.1:9300" \
        "$BIN/worker-pool-memory"
    fi

    if [[ "${MVP_ENABLE_PLEX:-0}" == "1" ]]; then
      maybe_start plex env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=plex MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        PLEX_GRPC_ADDR=":9476" PLEX_HTTP_ADDR=":8476" \
        PLEX_URL="${PLEX_URL:-}" PLEX_TOKEN="${PLEX_TOKEN:-}" \
        "$BIN/plex"
    fi

    if [[ "${MVP_ENABLE_EMBY:-0}" == "1" ]]; then
      maybe_start emby env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=emby MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        EMBY_GRPC_ADDR=":9477" EMBY_HTTP_ADDR=":8477" \
        EMBY_URL="${EMBY_URL:-}" EMBY_TOKEN="${EMBY_TOKEN:-}" \
        "$BIN/emby"
    fi

    # Alternate infrastructure slots — binaries deployed; leave disabled unless migrating off defaults.
    if [[ "${MVP_ENABLE_AUTH_OIDC:-0}" == "1" ]]; then
      mkdir -p "$DATA/auth-oidc"
      maybe_start auth-oidc env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=auth-oidc MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        AUTH_OIDC_GRPC_ADDR=":9410" AUTH_OIDC_HTTP_ADDR=":9412" \
        AUTH_OIDC_DB_PATH="$DATA/auth-oidc/auth.db" \
        "$BIN/auth-oidc"
    fi

    if [[ "${MVP_ENABLE_CACHE_LOCAL:-0}" == "1" ]]; then
      maybe_start cache-local env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=cache-local MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        CACHE_LOCAL_GRPC_ADDR=":9602" \
        "$BIN/cache-local"
    fi

    if [[ "${MVP_ENABLE_DATABASE_POSTGRES:-0}" == "1" ]]; then
      maybe_start database-postgres env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=database-postgres MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        DATABASE_GRPC_ADDR=":9701" \
        "$BIN/database-postgres"
    fi

    if [[ "${MVP_ENABLE_SECRETS_VAULT:-0}" == "1" ]]; then
      maybe_start secrets-vault env \
        MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=secrets-vault MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
        SECRETS_GRPC_ADDR=":9551" \
        "$BIN/secrets-vault"
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
          USERDATA_LOCAL_URL="${USERDATA_LOCAL_URL:-http://127.0.0.1:9672}" \
          MOVIES_GRPC_CLIENT_ADDR="127.0.0.1:9420" \
          TVSHOWS_GRPC_CLIENT_ADDR="127.0.0.1:9440" \
          JELLYFIN_GRPC_CLIENT_ADDR="127.0.0.1:9475" \
          MOVIES_HTTP_URL="http://127.0.0.1:9430" \
          TVSHOWS_HTTP_URL="http://127.0.0.1:9450" \
          REQUEST_MEDIA_HTTP_URL="http://127.0.0.1:9380" \
          SUBTITLES_GRPC_CLIENT_ADDR="127.0.0.1:9520" \
          SUBTITLES_HTTP_URL="http://127.0.0.1:9521" \
          FFPROBE_GRPC_CLIENT_ADDR="127.0.0.1:9480" \
          TRANSCODER_HTTP_URL="http://127.0.0.1:9526" \
          LISTSYNC_GRPC_CLIENT_ADDR="${LISTSYNC_GRPC_CLIENT_ADDR:-127.0.0.1:9530}" \
          GRAPH_HTTP_URL="${GRAPH_HTTP_URL:-http://127.0.0.1:9731}" \
          GRAPH_MODULE_TOKEN="${GRAPH_MODULE_TOKEN:-}" \
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
            -ffprobe-grpc "127.0.0.1:9480" \
            -transcoder-http "http://127.0.0.1:9526" \
            -graph-http "${GRAPH_HTTP_URL:-http://127.0.0.1:9731}"
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
    echo "usage: $0 {up|stop|stop-one <name>|restart <name>|status|unregister <id>|help}" >&2
    exit 2
    ;;
esac
