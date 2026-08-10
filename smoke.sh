#!/usr/bin/env bash
# Smoke-test the MVP stack: health, discovery, auth API, AddMovie, AddTVShow.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
# shellcheck disable=SC1091
[[ -f "$ROOT/.env" ]] && source "$ROOT/.env" || true

CORE_URL="${SMOKE_CORE_URL:-http://127.0.0.1:8080}"
API_URL="${SMOKE_API_URL:-http://127.0.0.1:18080}"
MESH="${MUXCORE_MESH_ADDR:-127.0.0.1:9090}"
MOVIES_ADDR="${MOVIES_GRPC_ADDR:-127.0.0.1:9420}"
TVSHOWS_ADDR="${TVSHOWS_GRPC_ADDR:-127.0.0.1:9440}"
TOKEN_FILE="${MVP_TOKEN_FILE:-$ROOT/run/admin.token}"
TIMEOUT="${SMOKE_TIMEOUT_SEC:-180}"
BIN="$ROOT/bin"

echo "==> waiting for core ${CORE_URL}/health (timeout ${TIMEOUT}s)"
deadline=$((SECONDS + TIMEOUT))
until code=$(curl -s -o /tmp/muxcore-core-health.json -w '%{http_code}' "${CORE_URL}/health" || echo 000); \
  [[ "$code" == "200" ]]; do
  if (( SECONDS >= deadline )); then
    echo "FAIL: core health not HTTP 200 (last $code)" >&2
    cat /tmp/muxcore-core-health.json 2>/dev/null || true
    exit 1
  fi
  sleep 2
done
echo "OK core health (HTTP $code)"

echo "==> waiting for api-rest ${API_URL}/api/v1/health"
until curl -sf "${API_URL}/api/v1/health" >/dev/null 2>&1; do
  if (( SECONDS >= deadline )); then
    echo "FAIL: api-rest health not ready" >&2
    exit 1
  fi
  sleep 2
done
echo "OK api-rest health"

build_if_missing() {
  local name="$1" pkg="$2"
  if [[ ! -x "$BIN/$name" ]]; then
    echo "==> building $name"
    (cd "$ROOT" && go build -o "$BIN/$name" "$pkg")
  fi
}
build_if_missing listmodules ./cmd/listmodules
build_if_missing gettoken ./cmd/gettoken
build_if_missing addmovie ./cmd/addmovie
build_if_missing addtvshow ./cmd/addtvshow

echo "==> resolving modules via core discovery (${MESH})"
MUXCORE_GRPC_ADDR="$MESH" "$BIN/listmodules"

if [[ ! -f "$TOKEN_FILE" ]]; then
  echo "==> no token file; running bootstrap-auth.sh"
  "$ROOT/bootstrap-auth.sh"
fi
TOKEN="$(tr -d '\n' <"$TOKEN_FILE")"
if [[ -z "$TOKEN" ]]; then
  echo "FAIL: empty token in $TOKEN_FILE" >&2
  exit 1
fi

echo "==> GET ${API_URL}/api/v1/modules (bearer)"
mods="$(curl -sf -H "Authorization: Bearer ${TOKEN}" "${API_URL}/api/v1/modules")"
echo "$mods" | head -c 500
echo
echo "$mods" | grep -qi 'api-rest\|media-movies\|auth-local\|media-tvshows' || {
  echo "FAIL: authenticated modules response missing expected ids" >&2
  exit 1
}
echo "OK authenticated modules list"

echo "==> AddMovie / ListMovies via ${MOVIES_ADDR}"
movie_root="${MVP_LIBRARY_ROOT:-$ROOT/data/library}"
[[ "$movie_root" != /* ]] && movie_root="$ROOT/${movie_root#./}"
"$BIN/addmovie" -addr "$MOVIES_ADDR" -root "$movie_root"

echo "==> AddTVShow / ListTVShows via ${TVSHOWS_ADDR}"
tv_root="${MVP_TV_LIBRARY_ROOT:-$ROOT/data/library/tv}"
[[ "$tv_root" != /* ]] && tv_root="$ROOT/${tv_root#./}"
"$BIN/addtvshow" -addr "$TVSHOWS_ADDR" -root "$tv_root"

ADMIN_URL="${SMOKE_ADMIN_URL:-http://localhost:8082}"
AUTH_HTTP="${AUTH_HTTP_URL:-http://127.0.0.1:9401}"
ADMIN_USER="${MVP_ADMIN_USER:-admin}"
ADMIN_PASS="${MVP_ADMIN_PASSWORD:-admin-dev-only}"

echo "==> admin-ui health ${ADMIN_URL}/health"
deadline_admin=$((SECONDS + 60))
until code=$(curl -s -o /tmp/muxcore-admin-health.json -w '%{http_code}' "${ADMIN_URL}/health" || echo 000); \
  [[ "$code" == "200" ]]; do
  if (( SECONDS >= deadline_admin )); then
    echo "FAIL: admin-ui health not HTTP 200 (last $code)" >&2
    cat /tmp/muxcore-admin-health.json 2>/dev/null || true
    exit 1
  fi
  sleep 1
done
echo "OK admin-ui health (HTTP $code)"

echo "==> admin-ui login redirect"
loc=$(curl -s -o /dev/null -w '%{redirect_url}' "${ADMIN_URL}/login" || true)
echo "$loc" | grep -qi 'login' || {
  echo "FAIL: /login did not redirect toward auth login (got: $loc)" >&2
  exit 1
}
echo "OK login redirects to auth ($loc)"

echo "==> admin-ui password login via auth-local"
jar=$(mktemp)
trap 'rm -f "$jar"' EXIT
redir_enc=$(printf '%s' "${ADMIN_URL}/auth/callback" | sed 's/:/%3A/g; s/\//%2F/g')
curl -s -c "$jar" -b "$jar" "${AUTH_HTTP}/login?redirect=${redir_enc}" >/dev/null
csrf=$(awk -F'\t' '$6=="csrf-token"{print $7}' "$jar" | tr -d '\r')
if [[ -z "$csrf" ]]; then
  echo "FAIL: no csrf-token cookie from auth login page" >&2
  cat "$jar" >&2
  exit 1
fi
# Do not use curl -L after POST — it can re-POST to /auth/callback and trip CSRF.
hdr=$(mktemp)
trap 'rm -f "$jar" "$hdr"' EXIT
code=$(curl -s -c "$jar" -b "$jar" -D "$hdr" -o /dev/null -w '%{http_code}' \
  -X POST "${AUTH_HTTP}/login/password" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "username=${ADMIN_USER}" \
  --data-urlencode "password=${ADMIN_PASS}" \
  --data-urlencode "csrf_token=${csrf}" \
  --data-urlencode "redirect=${ADMIN_URL}/auth/callback")
loc=$(awk -F': ' 'tolower($1)=="location"{gsub(/\r/,"",$2); print $2; exit}' "$hdr")
echo "auth login HTTP $code -> $loc"
[[ "$code" == "303" || "$code" == "302" ]] || {
  echo "FAIL: expected redirect from auth login, got $code" >&2
  exit 1
}
[[ -n "$loc" ]] || { echo "FAIL: missing Location from auth login" >&2; exit 1; }
cb_code=$(curl -s -c "$jar" -b "$jar" -o /dev/null -w '%{http_code}' -D "$hdr" "$loc")
echo "callback HTTP $cb_code"
[[ "$cb_code" == "303" || "$cb_code" == "302" || "$cb_code" == "200" ]] || {
  echo "FAIL: auth callback failed ($cb_code)" >&2
  exit 1
}
grep -qi '[[:space:]]session[[:space:]]' "$jar" || {
  echo "FAIL: no admin-ui session cookie after login" >&2
  cat "$jar" >&2
  exit 1
}
home_code=$(curl -s -c "$jar" -b "$jar" -o /tmp/muxcore-admin-home.html -w '%{http_code}' "${ADMIN_URL}/")
echo "home HTTP $home_code"
[[ "$home_code" == "200" ]] || {
  echo "FAIL: admin home not HTTP 200" >&2
  head -c 400 /tmp/muxcore-admin-home.html 2>/dev/null || true
  exit 1
}
grep -qi 'health-grid\|Module Health\|Dashboard' /tmp/muxcore-admin-home.html || {
  echo "FAIL: admin home missing dashboard shell" >&2
  head -c 400 /tmp/muxcore-admin-home.html >&2 || true
  exit 1
}
mod_code=$(curl -s -c "$jar" -b "$jar" -o /tmp/muxcore-admin-modules.html -w '%{http_code}' "${ADMIN_URL}/modules")
[[ "$mod_code" == "200" ]] || {
  echo "FAIL: /modules HTTP $mod_code" >&2
  exit 1
}
grep -qi 'media-movies\|metadata-tmdb\|Module' /tmp/muxcore-admin-modules.html || {
  echo "FAIL: /modules missing expected module names" >&2
  head -c 400 /tmp/muxcore-admin-modules.html >&2 || true
  exit 1
}
mon_code=$(curl -s -c "$jar" -b "$jar" -o /tmp/muxcore-admin-monitor.html -w '%{http_code}' "${ADMIN_URL}/dashboard/monitor")
[[ "$mon_code" == "200" ]] || {
  echo "FAIL: /dashboard/monitor HTTP $mon_code" >&2
  exit 1
}
grep -qi 'monitor-summary\|Aggregate\|Health monitor' /tmp/muxcore-admin-monitor.html || {
  echo "FAIL: /dashboard/monitor missing monitor summary" >&2
  head -c 400 /tmp/muxcore-admin-monitor.html >&2 || true
  exit 1
}
auto_code=$(curl -s -c "$jar" -b "$jar" -o /tmp/muxcore-admin-automation.html -w '%{http_code}' "${ADMIN_URL}/automation")
[[ "$auto_code" == "200" ]] || {
  echo "FAIL: /automation HTTP $auto_code" >&2
  exit 1
}
grep -qi 'automation-page\|Wanted queue\|Fight Club\|Queue is empty' /tmp/muxcore-admin-automation.html || {
  echo "FAIL: /automation missing queue surface" >&2
  head -c 400 /tmp/muxcore-admin-automation.html >&2 || true
  exit 1
}
jf_code=$(curl -s -c "$jar" -b "$jar" -o /tmp/muxcore-admin-jellyfin.html -w '%{http_code}' "${ADMIN_URL}/jellyfin")
[[ "$jf_code" == "200" ]] || {
  echo "FAIL: /jellyfin HTTP $jf_code" >&2
  exit 1
}
grep -qi 'jellyfin-page\|Configured\|Item links\|Soft OK' /tmp/muxcore-admin-jellyfin.html || {
  echo "FAIL: /jellyfin missing status surface" >&2
  head -c 400 /tmp/muxcore-admin-jellyfin.html >&2 || true
  exit 1
}
# Optional import-list peer (MVP_ENABLE_MEDIA_LIST_SYNC=1)
list_sync_ok=""
if SMOKE_MODULES=media-list-sync MUXCORE_GRPC_ADDR="$MESH" "$BIN/listmodules" >/dev/null 2>&1; then
  ls_code=$(curl -s -c "$jar" -b "$jar" -o /tmp/muxcore-admin-list-sync.html -w '%{http_code}' "${ADMIN_URL}/list-sync")
  [[ "$ls_code" == "200" ]] || {
    echo "FAIL: /list-sync HTTP $ls_code (media-list-sync registered)" >&2
    exit 1
  }
  grep -qi 'list-sync-page\|List Sync\|Add source' /tmp/muxcore-admin-list-sync.html || {
    echo "FAIL: /list-sync missing operator surface" >&2
    head -c 400 /tmp/muxcore-admin-list-sync.html >&2 || true
    exit 1
  }
  echo "OK admin-ui /list-sync (media-list-sync present)"
  list_sync_ok=" + /list-sync"
fi
# Soft SyncLibrary via admin-ui (csrf via cookie + header from csrf.js not available in curl; call gRPC helper already covers Sync).
grep -qi 'Sync library\|jellyfin-sync' /tmp/muxcore-admin-jellyfin.html || {
  echo "FAIL: /jellyfin missing Sync library action" >&2
  exit 1
}
grep -qi 'Fixture\|Search\|automation/dispatch' /tmp/muxcore-admin-automation.html || {
  echo "FAIL: /automation missing Dispatch actions" >&2
  exit 1
}
css_code=$(curl -s -c "$jar" -b "$jar" -o /tmp/muxcore-admin-styles.css -w '%{http_code}' "${ADMIN_URL}/static/dist/styles.css")
[[ "$css_code" == "200" ]] || {
  echo "FAIL: /static/dist/styles.css HTTP $css_code (rebuild admin-ui CSS)" >&2
  exit 1
}
[[ "$(wc -c </tmp/muxcore-admin-styles.css)" -gt 1000 ]] || {
  echo "FAIL: styles.css too small / empty" >&2
  exit 1
}
health_frag=$(curl -s -c "$jar" -b "$jar" -o /tmp/muxcore-admin-health.html -w '%{http_code}' "${ADMIN_URL}/dashboard/health")
[[ "$health_frag" == "200" ]] || {
  echo "FAIL: /dashboard/health HTTP $health_frag" >&2
  exit 1
}
grep -qi 'media-movies\|auth-local\|Module' /tmp/muxcore-admin-health.html || {
  echo "FAIL: /dashboard/health missing module cards (Members modules empty?)" >&2
  head -c 400 /tmp/muxcore-admin-health.html >&2 || true
  exit 1
}
sse_hdr=$(mktemp)
# SSE is long-lived; read a short window for the initial cluster-update event.
sse_code=$(curl -s -N -m 2 -c "$jar" -b "$jar" -D "$sse_hdr" -o /tmp/muxcore-admin-sse.txt -w '%{http_code}' \
  "${ADMIN_URL}/cluster/sse" || true)
sse_ctype=$(awk -F': ' 'tolower($1)=="content-type"{gsub(/\r/,"",$2); print $2; exit}' "$sse_hdr")
rm -f "$sse_hdr"
[[ "$sse_code" == "200" ]] || {
  echo "FAIL: /cluster/sse HTTP $sse_code (Watch allowlist / core discovery)" >&2
  exit 1
}
echo "$sse_ctype" | grep -qi 'text/event-stream' || {
  echo "FAIL: /cluster/sse Content-Type=$sse_ctype" >&2
  exit 1
}
grep -qi 'cluster-update' /tmp/muxcore-admin-sse.txt || {
  echo "FAIL: /cluster/sse missing cluster-update event" >&2
  head -c 200 /tmp/muxcore-admin-sse.txt >&2 || true
  exit 1
}
echo "OK admin-ui session established (+ /modules + health + CSS + cluster SSE + /dashboard/monitor + /automation + /jellyfin${list_sync_ok})"

JELLYFIN_HTTP="${SMOKE_JELLYFIN_URL:-http://127.0.0.1:8475}"
build_if_missing jellyfinstatus ./cmd/jellyfinstatus

echo "==> jellyfin healthz ${JELLYFIN_HTTP}/healthz"
deadline_jf=$((SECONDS + 60))
until code=$(curl -s -o /tmp/muxcore-jellyfin-health.json -w '%{http_code}' "${JELLYFIN_HTTP}/healthz" || echo 000); \
  [[ "$code" == "200" ]]; do
  if (( SECONDS >= deadline_jf )); then
    echo "FAIL: jellyfin healthz not HTTP 200 (last $code)" >&2
    cat /tmp/muxcore-jellyfin-health.json 2>/dev/null || true
    exit 1
  fi
  sleep 1
done
echo "OK jellyfin healthz (HTTP $code)"

jf_grpc="${JELLYFIN_GRPC_CLIENT_ADDR:-}"
if [[ -z "$jf_grpc" ]]; then
  jf_grpc="${JELLYFIN_GRPC_ADDR:-127.0.0.1:9475}"
  [[ "$jf_grpc" == :* ]] && jf_grpc="127.0.0.1${jf_grpc}"
fi
echo "==> jellyfin Status via ${jf_grpc}"
"$BIN/jellyfinstatus" -addr "$jf_grpc"

build_if_missing jellyfinlink ./cmd/jellyfinlink
echo "==> jellyfin soft UpsertItemLink + webhook (no live Jellyfin)"
"$BIN/jellyfinlink" \
  -addr "$jf_grpc" \
  -webhook-url "${SMOKE_JELLYFIN_WEBHOOK:-${JELLYFIN_HTTP}/webhook}"

# Live Jellyfin path: opt-in via SMOKE_LIVE_JELLYFIN=1, or auto when both server creds are set
# (disable with SMOKE_LIVE_JELLYFIN=0). Requires jellyfin module env JELLYFIN_BASE_URL + JELLYFIN_API_KEY.
live_jellyfin=0
if [[ "${SMOKE_LIVE_JELLYFIN:-}" == "1" ]]; then
  live_jellyfin=1
elif [[ "${SMOKE_LIVE_JELLYFIN:-}" != "0" && -n "${JELLYFIN_BASE_URL:-}" && -n "${JELLYFIN_API_KEY:-}" ]]; then
  live_jellyfin=1
fi
if [[ "$live_jellyfin" == "1" ]]; then
  build_if_missing jellyfinlive ./cmd/jellyfinlive
  echo "==> jellyfin LIVE (Status configured + RefreshLibrary + SyncLibrary)"
  "$BIN/jellyfinstatus" -addr "$jf_grpc" -require-configured
  "$BIN/jellyfinlive" -addr "$jf_grpc"
  echo "OK jellyfin live smoke"
fi

SCANNER_ADDR="${SCANNER_GRPC_CLIENT_ADDR:-127.0.0.1:9470}"
[[ "$SCANNER_ADDR" == :* ]] && SCANNER_ADDR="127.0.0.1${SCANNER_ADDR}"
build_if_missing importscan ./cmd/importscan

echo "==> scanner ImportPath fixture"
"$BIN/importscan" \
  -addr "$SCANNER_ADDR" \
  -watch "${MVP_DOWNLOADS_DIR:-$ROOT/data/downloads}" \
  -library "${MVP_LIBRARY_ROOT:-$ROOT/data/library}"

AUTOMATION_ADDR="${AUTOMATION_GRPC_CLIENT_ADDR:-127.0.0.1:9460}"
[[ "$AUTOMATION_ADDR" == :* ]] && AUTOMATION_ADDR="127.0.0.1${AUTOMATION_ADDR}"
build_if_missing automationqueue ./cmd/automationqueue

echo "==> automation queue soft"
"$BIN/automationqueue" -addr "$AUTOMATION_ADDR"

HM_ADDR="${HEALTH_MONITOR_GRPC_CLIENT_ADDR:-127.0.0.1:9202}"
[[ "$HM_ADDR" == :* ]] && HM_ADDR="127.0.0.1${HM_ADDR}"
HM_STATUS="${SMOKE_HEALTH_MONITOR_STATUS:-http://127.0.0.1:9203/status}"
build_if_missing healthreport ./cmd/healthreport

echo "==> health-monitor ReportHealth + /status"
deadline_hm=$((SECONDS + 60))
until curl -sf "http://127.0.0.1:9203/health" >/dev/null 2>&1; do
  if (( SECONDS >= deadline_hm )); then
    echo "FAIL: health-monitor HTTP not ready" >&2
    exit 1
  fi
  sleep 1
done
"$BIN/healthreport" -addr "$HM_ADDR" -status-url "$HM_STATUS"

echo "==> admin-ui /events health filter (mesh module.degraded)"
# Give admin-ui subscription a moment to ingest the fan-out from healthreport.
sleep 1
ev_code=$(curl -s -c "$jar" -b "$jar" -o /tmp/muxcore-admin-events.html -w '%{http_code}' "${ADMIN_URL}/events?filter=health")
[[ "$ev_code" == "200" ]] || {
  echo "FAIL: /events?filter=health HTTP $ev_code" >&2
  exit 1
}
grep -qi 'module.degraded\|health.smoke\|events-page\|events-table' /tmp/muxcore-admin-events.html || {
  echo "FAIL: /events health filter missing degraded/health events" >&2
  head -c 500 /tmp/muxcore-admin-events.html >&2 || true
  exit 1
}
grep -qi 'module.degraded' /tmp/muxcore-admin-events.html || {
  echo "FAIL: expected module.degraded in admin-ui events ring" >&2
  head -c 500 /tmp/muxcore-admin-events.html >&2 || true
  exit 1
}
echo "OK admin-ui events show module.degraded"

MEDIA_UI_URL="${SMOKE_MEDIA_UI_URL:-http://127.0.0.1:5173}"
if curl -sf "${MEDIA_UI_URL}/healthz" >/dev/null 2>&1; then
  echo "==> media-ui SPA ${MEDIA_UI_URL}"
  unauth=$(curl -s -o /dev/null -w '%{http_code}' "${MEDIA_UI_URL}/api/movies?page=1")
  [[ "$unauth" == "401" ]] || {
    echo "FAIL: media-ui API expected 401 without session, got $unauth" >&2
    exit 1
  }
  echo "==> media-ui password login via auth-local"
  media_cj=$(mktemp)
  media_hdr=$(mktemp)
  media_redir_enc=$(printf '%s' "${MEDIA_UI_URL}/auth/callback" | sed 's/:/%3A/g; s/\//%2F/g')
  curl -s -c "$media_cj" -b "$media_cj" "${AUTH_HTTP}/login?redirect=${media_redir_enc}" >/dev/null
  media_csrf=$(awk -F'\t' '$6=="csrf-token"{print $7}' "$media_cj" | tr -d '\r')
  [[ -n "$media_csrf" ]] || { echo "FAIL: no csrf-token for media-ui login" >&2; exit 1; }
  media_code=$(curl -s -c "$media_cj" -b "$media_cj" -D "$media_hdr" -o /dev/null -w '%{http_code}' \
    -X POST "${AUTH_HTTP}/login/password" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    --data-urlencode "username=${ADMIN_USER}" \
    --data-urlencode "password=${ADMIN_PASS}" \
    --data-urlencode "csrf_token=${media_csrf}" \
    --data-urlencode "redirect=${MEDIA_UI_URL}/auth/callback")
  media_loc=$(awk -F': ' 'tolower($1)=="location"{gsub(/\r/,"",$2); print $2; exit}' "$media_hdr")
  echo "media-ui auth login HTTP $media_code -> $media_loc"
  [[ "$media_code" == "303" || "$media_code" == "302" ]] || {
    echo "FAIL: media-ui auth login HTTP $media_code" >&2
    exit 1
  }
  [[ -n "$media_loc" ]] || { echo "FAIL: missing Location from media-ui auth login" >&2; exit 1; }
  media_cb=$(curl -s -c "$media_cj" -b "$media_cj" -o /dev/null -w '%{http_code}' "$media_loc")
  [[ "$media_cb" == "303" || "$media_cb" == "302" || "$media_cb" == "200" ]] || {
    echo "FAIL: media-ui auth callback failed ($media_cb)" >&2
    exit 1
  }
  grep -qi '[[:space:]]session[[:space:]]' "$media_cj" || {
    echo "FAIL: no media-ui session cookie after login" >&2
    cat "$media_cj" >&2
    exit 1
  }
  code=$(curl -s -c "$media_cj" -b "$media_cj" -o /tmp/muxcore-media-ui.html -w '%{http_code}' "${MEDIA_UI_URL}/")
  [[ "$code" == "200" ]] || { echo "FAIL: media-ui index HTTP $code" >&2; exit 1; }
  grep -qi '<div id="root"\|media-ui\|<script' /tmp/muxcore-media-ui.html || {
    echo "FAIL: media-ui index does not look like SPA shell" >&2
    head -c 200 /tmp/muxcore-media-ui.html >&2 || true
    exit 1
  }
  movies_json=$(curl -sf -c "$media_cj" -b "$media_cj" "${MEDIA_UI_URL}/api/movies?page=1&page_size=10")
  echo "$movies_json" | grep -q '"items"' || {
    echo "FAIL: /api/movies missing items" >&2
    echo "$movies_json" >&2
    exit 1
  }
  echo "$movies_json" | grep -qi 'Fight Club\|mv_' || {
    echo "FAIL: /api/movies expected Fight Club / movie id from library" >&2
    echo "$movies_json" | head -c 400 >&2
    exit 1
  }
  echo "$movies_json" | grep -Eq '"has_file"[[:space:]]*:[[:space:]]*true' || {
    echo "FAIL: expected Fight Club has_file=true for playback" >&2
    echo "$movies_json" | head -c 400 >&2
    exit 1
  }
  stream_path=""
  if command -v jq >/dev/null 2>&1; then
    stream_path=$(echo "$movies_json" | jq -r '.items[]? | select(.has_file==true) | .stream_url // empty' | head -1)
  elif command -v python3 >/dev/null 2>&1; then
    stream_path=$(echo "$movies_json" | python3 -c 'import json,sys
d=json.load(sys.stdin)
for it in d.get("items") or []:
  if it.get("has_file") and it.get("stream_url"):
    print(it["stream_url"]); break')
  else
    stream_path=$(echo "$movies_json" | tr '{' '\n' | awk '/"has_file"[[:space:]]*:[[:space:]]*true/{f=1} f&&/"stream_url"/{match($0,/"stream_url"[[:space:]]*:[[:space:]]*"([^"]*)"/,a); print a[1]; exit}')
  fi
  [[ -n "$stream_path" && "$stream_path" != null ]] || { echo "FAIL: missing stream_url for has_file item" >&2; exit 1; }
  stream_code=$(curl -s -c "$media_cj" -b "$media_cj" -o /dev/null -w '%{http_code}' -r 0-1023 "${MEDIA_UI_URL}${stream_path}")
  [[ "$stream_code" == "200" || "$stream_code" == "206" ]] || {
    echo "FAIL: stream ${stream_path} HTTP $stream_code" >&2
    exit 1
  }
  tv_json=$(curl -sf -c "$media_cj" -b "$media_cj" "${MEDIA_UI_URL}/api/tv?page=1&page_size=10")
  echo "$tv_json" | grep -q '"items"' || {
    echo "FAIL: /api/tv missing items" >&2
    exit 1
  }
  echo "OK media-ui auth + shell + /api/movies + stream + /api/tv"
  # Soft Jellyfin play deep-link (404 unlinked / not configured; 200 when URL available)
  jf_play_code=$(curl -s -c "$media_cj" -b "$media_cj" -o /tmp/muxcore-jellyfin-play.json -w '%{http_code}' \
    "${MEDIA_UI_URL}/api/jellyfin/play?mux_id=mv_smoke_550")
  case "$jf_play_code" in
    200|404) echo "OK /api/jellyfin/play (HTTP $jf_play_code)" ;;
    *)
      echo "FAIL: /api/jellyfin/play HTTP $jf_play_code (expected 200 or 404)" >&2
      head -c 200 /tmp/muxcore-jellyfin-play.json >&2 || true
      echo >&2
      exit 1
      ;;
  esac
  build_if_missing mediarequest ./cmd/mediarequest
  echo "==> media-ui → request-media search/request"
  mr_flags=(-base "$MEDIA_UI_URL" -cookie-jar "$media_cj")
  if [[ "${TMDB_FIXTURE:-}" == "1" || "${SMOKE_REQUIRE_TMDB_SEARCH:-}" == "1" || -n "${TMDB_API_KEY:-}" ]]; then
    mr_flags+=(-require-search)
  fi
  "$BIN/mediarequest" "${mr_flags[@]}"
  rm -f "$media_cj" "$media_hdr"
else
  echo "==> media-ui not running (set MVP_ENABLE_MEDIA_UI=1 / build dist-app); skipping"
fi

echo "PASS: MVP smoke (auth + movies + tv + admin-ui + jellyfin + scanner + automation + health-monitor + media-ui + request-media)"
