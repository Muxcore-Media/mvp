#!/usr/bin/env bash
# Interactive first-run setup for the MuxCore media MVP host stack.
# Writes .env, starts the host mesh, and bootstraps local admin auth.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
WS="$(cd "$ROOT/.." && pwd)"
ENVF="$ROOT/.env"
EXAMPLE="$ROOT/.env.example"

die() { echo "error: $*" >&2; exit 1; }
info() { echo "==> $*"; }
ok() { echo "    $*"; }

prompt() {
  # prompt VAR "Question" [default]
  local var="$1" q="$2" def="${3:-}" ans
  if [[ -n "$def" ]]; then
    read -r -p "$q [$def]: " ans || true
    ans="${ans:-$def}"
  else
    read -r -p "$q: " ans || true
  fi
  printf -v "$var" '%s' "$ans"
}

prompt_secret() {
  local var="$1" q="$2" def="${3:-}" ans
  if [[ -n "$def" ]]; then
    read -r -s -p "$q [stored/default]: " ans || true
    echo
    ans="${ans:-$def}"
  else
    read -r -s -p "$q: " ans || true
    echo
  fi
  printf -v "$var" '%s' "$ans"
}

yesno() {
  local q="$1" def="${2:-y}" ans
  read -r -p "$q [$def]: " ans || true
  ans="${ans:-$def}"
  [[ "$ans" =~ ^[Yy] ]]
}

# Set or replace KEY=value in .env (creates file from example if needed).
env_set() {
  local key="$1" val="$2"
  local tmp
  tmp="$(mktemp)"
  touch "$ENVF"
  if rg -q "^${key}=" "$ENVF" 2>/dev/null; then
    # shellcheck disable=SC2016
    awk -v k="$key" -v v="$val" 'BEGIN{FS=OFS="="} $1==k{$0=k"="v} {print}' "$ENVF" >"$tmp"
    mv "$tmp" "$ENVF"
  else
    printf '%s=%s\n' "$key" "$val" >>"$ENVF"
    rm -f "$tmp"
  fi
}

env_get() {
  local key="$1"
  [[ -f "$ENVF" ]] || { echo ""; return; }
  # shellcheck disable=SC1090
  ( set -a; source "$ENVF" >/dev/null 2>&1; eval "printf '%s' \"\${$key:-}\"" )
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

check_siblings() {
  local missing=()
  local req=(
    core api-rest auth-local admin-ui metadata-tmdb request-media
    media-movies media-tvshows media-scanner media-automation
    media-root-folders jellyfin health-monitor notification-default
    secrets-file encryption-aesgcm call-policy-default publish-policy-default
    media-ui-app
  )
  local r
  for r in "${req[@]}"; do
    [[ -d "$WS/$r" ]] || missing+=("$r")
  done
  if ((${#missing[@]})); then
    echo "Missing sibling repos under $WS:" >&2
    printf '  - %s\n' "${missing[@]}" >&2
    die "clone the Muxcore-Media siblings next to mvp/, then re-run setup"
  fi
}

write_view_me() {
  "$ROOT/scripts/write-view-me.sh"
}

main() {
  cd "$ROOT"
  need_cmd go
  need_cmd curl
  need_cmd rg
  check_siblings

  echo
  echo "MuxCore MVP setup"
  echo "Workspace: $WS"
  echo

  if [[ ! -f "$ENVF" ]]; then
    [[ -f "$EXAMPLE" ]] || die "missing .env.example"
    cp "$EXAMPLE" "$ENVF"
    ok "created .env from .env.example"
  else
    ok "using existing .env"
  fi

  # shellcheck disable=SC1090
  set -a
  # shellcheck disable=SC1091
  source "$ENVF" >/dev/null 2>&1 || true
  set +a

  local tmdb_mode tmdb_key
  echo "Metadata (TMDB)"
  echo "  1) Live API key (recommended)"
  echo "  2) Offline fixtures (no key; Fight Club / Breaking Bad only)"
  prompt tmdb_mode "Choose metadata mode" "1"
  case "$tmdb_mode" in
    2)
      env_set TMDB_FIXTURE 1
      env_set TMDB_API_KEY ""
      ok "offline TMDB fixtures enabled"
      ;;
    *)
      prompt_secret tmdb_key "TMDB v3 API key" "${TMDB_API_KEY:-}"
      [[ -n "$tmdb_key" ]] || die "TMDB API key required (or choose mode 2)"
      env_set TMDB_API_KEY "$tmdb_key"
      env_set TMDB_FIXTURE ""
      ok "TMDB API key saved"
      ;;
  esac

  local admin_user admin_pass
  echo
  echo "Admin account (auth-local)"
  prompt admin_user "Admin username" "${MVP_ADMIN_USER:-admin}"
  prompt_secret admin_pass "Admin password" "${MVP_ADMIN_PASSWORD:-admin-dev-only}"
  [[ -n "$admin_pass" ]] || die "admin password required"
  env_set MVP_ADMIN_USER "$admin_user"
  env_set MVP_ADMIN_PASSWORD "$admin_pass"
  MVP_ADMIN_USER="$admin_user"

  local library_dir download_dir tv_dir
  echo
  echo "Media paths (created if missing)"
  prompt library_dir "Movie library directory" "${MVP_LIBRARY_ROOT:-$ROOT/data/library}"
  prompt tv_dir "TV library directory" "${MVP_TV_LIBRARY_ROOT:-$ROOT/data/library/tv}"
  prompt download_dir "Incoming / downloads directory" "${MVP_DOWNLOADS_DIR:-$ROOT/data/downloads}"
  library_dir="$(cd / && realpath -m "$library_dir")"
  tv_dir="$(cd / && realpath -m "$tv_dir")"
  download_dir="$(cd / && realpath -m "$download_dir")"
  mkdir -p "$library_dir" "$tv_dir" "$download_dir"
  env_set MVP_LIBRARY_ROOT "$library_dir"
  env_set MVP_TV_LIBRARY_ROOT "$tv_dir"
  env_set MVP_DOWNLOADS_DIR "$download_dir"
  LIBRARY_DIR="$library_dir"
  DOWNLOAD_DIR="$download_dir"

  local jf_url jf_key
  echo
  if yesno "Configure a Jellyfin server now?" "n"; then
    prompt jf_url "Jellyfin base URL" "${JELLYFIN_BASE_URL:-http://127.0.0.1:8096}"
    prompt_secret jf_key "Jellyfin API key" "${JELLYFIN_API_KEY:-}"
    env_set JELLYFIN_BASE_URL "$jf_url"
    env_set JELLYFIN_API_KEY "$jf_key"
    ok "Jellyfin bridge will use $jf_url"
  else
    ok "Jellyfin left unconfigured (soft bridge still starts)"
  fi

  env_set MVP_ENABLE_MEDIA_UI 1
  env_set MUXCORE_INSECURE_DISABLE_TLS true

  echo
  if yesno "Start the host stack now (./run-host.sh up)?" "y"; then
    info "starting host stack"
    ./run-host.sh up
  else
    ok "skipped start — run ./run-host.sh up when ready"
    write_view_me
    echo
    cat "$ROOT/run/VIEW-ME.txt"
    exit 0
  fi

  info "waiting for core health"
  local deadline=$((SECONDS + 120)) code
  until code=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/health || echo 000); [[ "$code" == "200" ]]; do
    (( SECONDS < deadline )) || die "core /health did not become ready"
    sleep 1
  done
  ok "core healthy"

  info "bootstrapping admin auth"
  ./bootstrap-auth.sh

  write_view_me
  echo
  cat "$ROOT/run/VIEW-ME.txt"
  echo
  if yesno "Run smoke tests now?" "y"; then
    ./smoke.sh
  else
    ok "skipped smoke — run ./smoke.sh when ready"
  fi
}

main "$@"
