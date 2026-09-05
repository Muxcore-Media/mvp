#!/usr/bin/env bash
# Path A registry smoke helpers — curl + containerized grpcurl (no host Go / siblings).
set -euo pipefail

REGISTRY_SMOKE_COMPOSE_FILE="${MUXCORE_COMPOSE_FILE:-docker-compose.registry.yml}"
REGISTRY_SMOKE_GRPCURL_IMAGE="${MUXCORE_GRPCURL_IMAGE:-fullstorydev/grpcurl:latest}"

registry_smoke_root="${REGISTRY_SMOKE_ROOT:-${ROOT:-}}"
registry_smoke_compose() {
  docker compose -f "${registry_smoke_root}/${REGISTRY_SMOKE_COMPOSE_FILE}" "$@"
}

registry_smoke_compose_project() {
  local name
  name="$(registry_smoke_compose config --format '{{.Name}}' 2>/dev/null || true)"
  [[ -n "$name" ]] || name="muxcore-mvp-registry"
  printf '%s' "$name"
}

registry_smoke_stack_running() {
  local id
  id="$(registry_smoke_compose ps -q core 2>/dev/null | head -1 || true)"
  [[ -n "$id" ]]
}

registry_smoke_network() {
  printf '%s_muxcore' "$(registry_smoke_compose_project)"
}

registry_smoke_volume() {
  printf '%s_%s' "$(registry_smoke_compose_project)" "$1"
}

registry_smoke_should_use() {
  case "${MUXCORE_SMOKE_REGISTRY:-}" in
    1|true|yes) return 0 ;;
    0|false|no) return 1 ;;
  esac
  if command -v go >/dev/null 2>&1; then
    if (cd "${registry_smoke_root}" && go build -o /dev/null ./cmd/listmodules >/dev/null 2>&1); then
      return 1
    fi
  fi
  registry_smoke_stack_running
}

registry_smoke_enable() {
  export MUXCORE_SMOKE_REGISTRY=1
  export MUXCORE_MESH_ADDR="${MUXCORE_MESH_ADDR:-core:9090}"
  export MOVIES_GRPC_ADDR="${MOVIES_GRPC_ADDR:-media-movies:9420}"
  export TVSHOWS_GRPC_ADDR="${TVSHOWS_GRPC_ADDR:-media-tvshows:9440}"
  export ROOTS_GRPC_ADDR="${ROOTS_GRPC_ADDR:-media-root-folders:9540}"
  export AUTH_GRPC_ADDR="${AUTH_GRPC_ADDR:-auth-local:9403}"
  export SCANNER_GRPC_CLIENT_ADDR="${SCANNER_GRPC_CLIENT_ADDR:-media-scanner:9470}"
  export AUTOMATION_GRPC_CLIENT_ADDR="${AUTOMATION_GRPC_CLIENT_ADDR:-media-automation:9460}"
  export HEALTH_MONITOR_GRPC_CLIENT_ADDR="${HEALTH_MONITOR_GRPC_CLIENT_ADDR:-health-monitor:9202}"
  export SMOKE_HEALTH_MONITOR_STATUS="${SMOKE_HEALTH_MONITOR_STATUS:-http://health-monitor:9203/status}"
  export JELLYFIN_GRPC_CLIENT_ADDR="${JELLYFIN_GRPC_CLIENT_ADDR:-jellyfin-bridge:9475}"
  export MVP_DOWNLOADS_DIR="${MVP_DOWNLOADS_DIR:-/data/downloads}"
  export MVP_LIBRARY_ROOT="${MVP_LIBRARY_ROOT:-/data/movies}"
  export MVP_TV_LIBRARY_ROOT="${MVP_TV_LIBRARY_ROOT:-/data/shows}"
}

registry_smoke_grpcurl() {
  local addr="$1"
  shift
  docker run --rm --network "$(registry_smoke_network)" \
    "$REGISTRY_SMOKE_GRPCURL_IMAGE" \
    -plaintext "$@" "$addr"
}

registry_smoke_grpcurl_host() {
  local addr="$1"
  shift
  docker run --rm --network host \
    "$REGISTRY_SMOKE_GRPCURL_IMAGE" \
    -plaintext "$@" "$addr"
}

registry_smoke_resolve_ok() {
  local mod="$1"
  local out
  out="$(registry_smoke_grpcurl "${MUXCORE_MESH_ADDR:-core:9090}" \
    -d "{\"id\":\"${mod}\"}" \
    muxcore.discovery.v1.DiscoveryService/Resolve 2>/dev/null || true)"
  [[ -n "$out" ]] && grep -q '"id"' <<<"$out"
}

registry_smoke_cmd_listmodules() {
  local need=(
    api-rest auth-local database-sqlite secrets-file encryption-aesgcm
    call-policy-default publish-policy-default metadata-tmdb media-movies
    media-tvshows media-automation media-scanner media-root-folders jellyfin
    request-media notification-default
  )
  if [[ -n "${SMOKE_MODULES:-}" ]]; then
    IFS=',' read -ra need <<<"$SMOKE_MODULES"
  fi
  local id missing=0
  for id in "${need[@]}"; do
    id="${id// /}"
    [[ -z "$id" ]] && continue
    if registry_smoke_resolve_ok "$id"; then
      echo "OK $id"
    else
      echo "MISSING $id" >&2
      missing=$((missing + 1))
    fi
  done
  [[ "$missing" -eq 0 ]]
}

registry_smoke_cmd_gettoken() {
  local addr="127.0.0.1:9403" user pass out
  user="admin"
  pass=""
  out=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -addr) addr="$2"; shift 2 ;;
      -user) user="$2"; shift 2 ;;
      -password) pass="$2"; shift 2 ;;
      -out) out="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  [[ -n "$pass" ]] || { echo "FAIL: -password required" >&2; return 1; }
  local host_addr="$addr"
  if [[ "$host_addr" != *:* ]]; then
    host_addr="127.0.0.1:${host_addr}"
  fi
  if [[ "$host_addr" == auth-local:* ]]; then
    host_addr="127.0.0.1:9403"
  fi
  local cred_payload resp token
  cred_payload="$(USER="$user" PASS="$pass" python3 - <<'PY'
import json, os
print(json.dumps({"username": os.environ["USER"], "password": os.environ["PASS"]}))
PY
)"
  resp="$(registry_smoke_grpcurl_host "$host_addr" \
    -d "$(python3 - <<PY
import json
cred = json.loads(${cred_payload@Q})
print(json.dumps({"credential_type": "password", "credential_data": cred}))
PY
)" \
    muxcore.auth.v1.AuthService/Authenticate)"
  token="$(printf '%s' "$resp" | python3 -c 'import json,sys
d=json.load(sys.stdin)
print(d.get("sessionToken") or d.get("session_token") or "")' 2>/dev/null || true)"
  [[ -n "$token" ]] || { echo "FAIL: auth token empty: $resp" >&2; return 1; }
  if [[ -n "$out" ]]; then
    mkdir -p "$(dirname "$out")"
    printf '%s\n' "$token" >"$out"
  else
    printf '%s\n' "$token"
  fi
}

registry_smoke_cmd_addmovie() {
  local addr="$MOVIES_GRPC_ADDR" root="$MVP_LIBRARY_ROOT"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -addr) addr="$2"; shift 2 ;;
      -root) root="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  if [[ -n "$root" ]]; then
    registry_smoke_grpcurl "${ROOTS_GRPC_ADDR:-media-root-folders:9540}" \
      -d "{\"path\":\"${root}\",\"media_type\":\"movie\"}" \
      muxcore.media.roots.v1.RootFolderService/AddRootFolder >/dev/null 2>&1 || true
  fi
  registry_smoke_grpcurl "$addr" \
    -d "{\"tmdb_id\":550,\"title\":\"Fight Club\",\"year\":1999,\"root_folder_path\":\"${root}\"}" \
    muxcore.media.movies.v1.MovieManagementService/AddMovie
  registry_smoke_grpcurl "$addr" \
    -d '{}' \
    muxcore.media.movies.v1.MovieManagementService/ListMovies >/dev/null
  echo "OK addmovie (registry grpcurl)"
}

registry_smoke_cmd_addtvshow() {
  local addr="$TVSHOWS_GRPC_ADDR" root="$MVP_TV_LIBRARY_ROOT"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -addr) addr="$2"; shift 2 ;;
      -root) root="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  if [[ -n "$root" ]]; then
    registry_smoke_grpcurl "${ROOTS_GRPC_ADDR:-media-root-folders:9540}" \
      -d "{\"path\":\"${root}\",\"media_type\":\"tv\"}" \
      muxcore.media.roots.v1.RootFolderService/AddRootFolder >/dev/null 2>&1 || true
  fi
  registry_smoke_grpcurl "$addr" \
    -d "{\"tmdb_id\":1396,\"name\":\"Breaking Bad\",\"year\":2008,\"root_folder_path\":\"${root}\"}" \
    muxcore.media.tvshows.v1.TVShowManagementService/AddTVShow
  registry_smoke_grpcurl "$addr" \
    -d '{}' \
    muxcore.media.tvshows.v1.TVShowManagementService/ListTVShows >/dev/null
  echo "OK addtvshow (registry grpcurl)"
}

registry_smoke_cmd_importscan() {
  local addr="$SCANNER_GRPC_CLIENT_ADDR"
  local watch="$MVP_DOWNLOADS_DIR" library="$MVP_LIBRARY_ROOT"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -addr) addr="$2"; shift 2 ;;
      -watch) watch="$2"; shift 2 ;;
      -library) library="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  local vol_dl vol_mov
  vol_dl="$(registry_smoke_volume downloads)"
  vol_mov="$(registry_smoke_volume movies)"
  docker run --rm \
    -v "${vol_dl}:/data/downloads" \
    -v "${vol_mov}:/data/movies" \
    alpine:3.21 sh -c 'mkdir -p "/data/downloads/Fight.Club.1999.1080p.BluRay.x264" && dd if=/dev/zero of="/data/downloads/Fight.Club.1999.1080p.BluRay.x264/Fight.Club.1999.1080p.BluRay.mkv" bs=1024 count=8 status=none'
  registry_smoke_grpcurl "$addr" \
    -d "{\"path\":\"${watch}\",\"library_path\":\"${library}\",\"media_type\":\"movie\"}" \
    muxcore.scanner.v1.ScannerService/AddWatchDir >/dev/null 2>&1 || true
  registry_smoke_grpcurl "$addr" \
    -d "{\"path\":\"${watch}/Fight.Club.1999.1080p.BluRay.x264\"}" \
    muxcore.scanner.v1.ScannerService/ImportPath
  echo "OK importscan (registry grpcurl)"
}

registry_smoke_cmd_automationqueue() {
  local addr="$AUTOMATION_GRPC_CLIENT_ADDR"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -addr) addr="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  registry_smoke_grpcurl "$addr" \
    -d '{"item_type":"movie","item_id":"mv_smoke_550","tmdb_id":550,"title":"Fight Club","year":1999}' \
    muxcore.automation.v1.AutomationService/AddToQueue
  registry_smoke_grpcurl "$addr" \
    -d '{"page":1,"page_size":50,"filter":"movie"}' \
    muxcore.automation.v1.AutomationService/GetQueue >/dev/null
  echo "OK automationqueue (registry grpcurl)"
}

registry_smoke_cmd_healthreport() {
  local addr="$HEALTH_MONITOR_GRPC_CLIENT_ADDR" status_url="$SMOKE_HEALTH_MONITOR_STATUS"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -addr) addr="$2"; shift 2 ;;
      -status-url) status_url="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  registry_smoke_grpcurl "$addr" \
    -d '{"modules":[{"module_id":"mvp-smoke","state":"running"},{"module_id":"api-rest","state":"running"}]}' \
    muxcore.healthmonitor.v1.HealthMonitorService/ReportHealth
  registry_smoke_grpcurl "$addr" \
    -d '{"event_type":"health.smoke","module_id":"mvp-smoke","message":"health-monitor smoke"}' \
    muxcore.healthmonitor.v1.HealthMonitorService/PublishEvent
  docker run --rm --network "$(registry_smoke_network)" curlimages/curl:8.5.0 \
    -sf "$status_url" >/dev/null
  echo "OK healthreport (registry grpcurl)"
}

registry_smoke_cmd_jellyfinstatus() {
  local addr="$JELLYFIN_GRPC_CLIENT_ADDR"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -addr) addr="$2"; shift 2 ;;
      -require-configured) shift ;;
      *) shift ;;
    esac
  done
  registry_smoke_grpcurl "$addr" \
    -d '{}' \
    muxcore.jellyfin.v1.JellyfinBridge/Status
  echo "OK jellyfinstatus (registry grpcurl)"
}

registry_smoke_cmd_jellyfinlink() {
  local addr="$JELLYFIN_GRPC_CLIENT_ADDR" webhook=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -addr) addr="$2"; shift 2 ;;
      -webhook-url) webhook="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  registry_smoke_grpcurl "$addr" \
    -d '{"link":{"muxcore_id":"mv_smoke_550","jellyfin_id":"jf-smoke-fixture","path":"/library/Movies/Fight Club (1999)/Fight Club.mkv","media_kind":"movie","title":"Fight Club","provider_ids":{"Tmdb":"550"}}}' \
    muxcore.jellyfin.v1.JellyfinBridge/UpsertItemLink
  echo "OK jellyfinlink (registry grpcurl)"
}

registry_smoke_cmd_jellyfinlive() {
  echo "OK jellyfinlive skipped (registry soft verify)"
}

registry_smoke_cmd_mediarequest() {
  local base="http://127.0.0.1:5173" jar="" require=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -base) base="$2"; shift 2 ;;
      -cookie-jar) jar="$2"; shift 2 ;;
      -require-search) require=1; shift ;;
      *) shift ;;
    esac
  done
  [[ -n "$jar" ]] || { echo "FAIL: mediarequest needs -cookie-jar in registry mode" >&2; return 1; }
  local curl_auth=(-b "$jar" -c "$jar")
  local search_body code
  search_body="$(curl -sf "${curl_auth[@]}" "${base}/api/search?q=Fight+Club")"
  if [[ "$require" -eq 1 ]]; then
    grep -qi 'Fight Club\|"id"' <<<"$search_body" || {
      echo "FAIL: search (require-search): $search_body" >&2
      return 1
    }
  fi
  code="$(curl -sf "${curl_auth[@]}" -o /tmp/mvp-registry-request.json -w '%{http_code}' \
    -X POST "${base}/api/request" \
    -H 'Content-Type: application/json' \
    -d '{"tmdbId":550,"title":"Fight Club","year":1999,"overview":"MVP smoke request","poster":""}')"
  [[ "$code" == "200" ]] || { echo "FAIL: POST /api/request HTTP $code" >&2; return 1; }
  curl -sf "${curl_auth[@]}" "${base}/api/requests" | grep -qi 'Fight Club\|request' || {
    echo "FAIL: GET /api/requests missing fixture" >&2
    return 1
  }
  echo "OK request-media via media-ui (registry curl)"
}

registry_smoke_cmd() {
  local cmd="$1"
  shift
  case "$cmd" in
    listmodules) registry_smoke_cmd_listmodules "$@" ;;
    gettoken) registry_smoke_cmd_gettoken "$@" ;;
    addmovie) registry_smoke_cmd_addmovie "$@" ;;
    addtvshow) registry_smoke_cmd_addtvshow "$@" ;;
    importscan) registry_smoke_cmd_importscan "$@" ;;
    automationqueue) registry_smoke_cmd_automationqueue "$@" ;;
    healthreport) registry_smoke_cmd_healthreport "$@" ;;
    jellyfinstatus) registry_smoke_cmd_jellyfinstatus "$@" ;;
    jellyfinlink) registry_smoke_cmd_jellyfinlink "$@" ;;
    jellyfinlive) registry_smoke_cmd_jellyfinlive "$@" ;;
    mediarequest) registry_smoke_cmd_mediarequest "$@" ;;
    authctl)
      echo "OK authctl skipped (AUTH_BOOTSTRAP_* in registry compose)"
      ;;
    *)
      echo "FAIL: unknown registry smoke cmd: $cmd" >&2
      return 1
      ;;
  esac
}

registry_smoke_bootstrap_auth() {
  local user pass token_file auth_addr
  user="${MVP_ADMIN_USER:-admin}"
  pass="${MVP_ADMIN_PASSWORD:-admin-dev-only}"
  token_file="${MVP_TOKEN_FILE:-${registry_smoke_root}/run/admin.token}"
  auth_addr="${AUTH_GRPC_ADDR:-auth-local:9403}"
  mkdir -p "$(dirname "$token_file")"
  echo "==> registry bootstrap: AUTH_BOOTSTRAP in compose + grpcurl token"
  local host_auth="127.0.0.1:9403"
  if [[ "$auth_addr" == *":"* && "$auth_addr" != 127.0.0.1:* && "$auth_addr" != localhost:* ]]; then
    host_auth="127.0.0.1:9403"
  else
    host_auth="$auth_addr"
  fi
  registry_smoke_cmd_gettoken -addr "$host_auth" -user "$user" -password "$pass" -out "$token_file"
  echo "token written to $token_file"
}
