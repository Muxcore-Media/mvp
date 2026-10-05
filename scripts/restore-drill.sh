#!/usr/bin/env bash
# Restore drill (T-M2-05, FR-BAK-004, ADR-0013): backup -> fresh install ->
# restore -> smoke, on fixture data. Procedure and operator notes: docs/RESTORE.md.
#
#   1. Up the stack (backup-local enabled), run smoke.sh to seed fixture data, seed
#      a userdata entry, snapshot /api/me, /api/movies, /api/tv, /api/requests and
#      the userdata entry through the BFF (logged in with the ORIGINAL admin password).
#   2. Quiesce: stop every service except core and backup-local. CreateBackup over
#      the manifest state dirs, check the archive covers them, VerifyBackup
#      {restore_test: true}, copy the archive out. Escrow `backup: false` state
#      (key material) separately, as an operator must.
#   3. Stop the stack and wipe ONLY the state dirs/volumes (plus backup-local's own
#      archive and restore dirs). Library media and downloads are kept.
#   4. Fresh install: start only core + backup-local, copy the archive into the new
#      backup dir (backup-local re-indexes it), RestoreBackup into a staging dir,
#      stop, then copy data/<prefix>/... into each module's state dir/volume using
#      the household-manifest.yaml mapping (scripts/lib/state-map.sh).
#   5. Start everything with a DECOY bootstrap admin password, compare against the
#      snapshot, log in with the ORIGINAL password (and check the decoy is refused:
#      auth-local did not re-bootstrap), then run smoke.sh again.
#
# Destructive: wipes the stack's module state. Fixture/test installs only;
# requires --confirm-wipe. Host mode drives ./run-host.sh; compose mode drives
# docker-compose.registry.yml (MUXCORE_COMPOSE / MUXCORE_CONTAINER_CLI as in smoke).
set -euo pipefail

DRILL_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="${RESTORE_DRILL_ROOT:-$(cd "$DRILL_SCRIPT_DIR/.." && pwd)}"

usage() {
  cat <<'EOF'
usage: scripts/restore-drill.sh --mode host|compose --confirm-wipe [options]
       scripts/restore-drill.sh --mode host|compose --dry-run [--manifest FILE]

Backup -> wipe -> fresh install -> restore -> smoke drill on fixture data
(FR-BAK-004). DESTROYS the stack's module state: fixture/test installs only.

  --mode host|compose   host: ./run-host.sh + data/; compose: docker-compose.registry.yml
  --confirm-wipe        required for a real run
  --dry-run             print the state mapping and steps, change nothing
  --manifest FILE       household manifest (default: household-manifest.yaml)
  --workdir DIR         drill work dir (default: mktemp under ${TMPDIR:-/tmp}); holds
                        the archive copy, escrow and snapshots (0700)
  --keep-workdir        keep the work dir after a successful run
  -h, --help            this text

Env: SMOKE_MEDIA_UI_URL (http://127.0.0.1:5173), RESTORE_DRILL_AUTH_URL
(http://127.0.0.1:9401), MVP_ADMIN_USER (admin), MVP_ADMIN_PASSWORD (else
data/auth/admin.password / .env), RESTORE_DRILL_TIMEOUT_SEC (240),
RESTORE_DRILL_VOLUME_OWNER (compose fallback owner, 1000:1000),
RESTORE_DRILL_SETTLE_SEC (15, wait after the stack answers). smoke.sh
variables (SMOKE_CATALOG, SMOKE_REQUIRE_ACQUISITION, MVP_ENABLE_*) pass through.
EOF
}

MODE=""
CONFIRM=0
DRY_RUN=0
KEEP_WORKDIR=0
WORK=""
MANIFEST="$ROOT/household-manifest.yaml"
DATA="${RESTORE_DRILL_DATA:-$ROOT/data}"
MEDIA_URL="${SMOKE_MEDIA_UI_URL:-http://127.0.0.1:5173}"
AUTH_URL="${RESTORE_DRILL_AUTH_URL:-http://127.0.0.1:9401}"
ADMIN_USER="${MVP_ADMIN_USER:-admin}"
TIMEOUT="${RESTORE_DRILL_TIMEOUT_SEC:-240}"
DRILL_KEY="restore-drill"

# parse_args "$@": sets MODE/CONFIRM/DRY_RUN/...; returns 2 on usage errors, 3 for --help.
parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --mode) [[ $# -ge 2 ]] || { echo "restore-drill: --mode needs a value" >&2; return 2; }; MODE="$2"; shift 2 ;;
      --mode=*) MODE="${1#*=}"; shift ;;
      --confirm-wipe) CONFIRM=1; shift ;;
      --dry-run) DRY_RUN=1; shift ;;
      --manifest) [[ $# -ge 2 ]] || { echo "restore-drill: --manifest needs a value" >&2; return 2; }; MANIFEST="$2"; shift 2 ;;
      --workdir) [[ $# -ge 2 ]] || { echo "restore-drill: --workdir needs a value" >&2; return 2; }; WORK="$2"; shift 2 ;;
      --keep-workdir) KEEP_WORKDIR=1; shift ;;
      -h|--help) return 3 ;;
      *) echo "restore-drill: unknown argument: $1" >&2; return 2 ;;
    esac
  done
  case "$MODE" in
    host|compose) ;;
    "") echo "restore-drill: --mode host|compose is required" >&2; return 2 ;;
    *) echo "restore-drill: --mode must be host or compose (got: $MODE)" >&2; return 2 ;;
  esac
  if [[ "$DRY_RUN" -eq 0 && "$CONFIRM" -ne 1 ]]; then
    echo "restore-drill: refusing to run without --confirm-wipe (the drill wipes all module state; use --dry-run to preview)" >&2
    return 2
  fi
  [[ -f "$MANIFEST" ]] || { echo "restore-drill: manifest not found: $MANIFEST" >&2; return 2; }
}

log() { printf '==> [restore-drill %s] %s\n' "$(date +%H:%M:%S)" "$*"; }
fail() { echo "FAIL: restore drill: $*" >&2; exit 1; }

# shellcheck disable=SC1091
source "$ROOT/scripts/lib/state-map.sh"

# MAP rows: id \t prefix \t location \t backup (scripts/lib/state-map.sh).
load_map() {
  MAP="$(state_map_tsv "$MANIFEST" "$MODE")" || fail "cannot read state mapping from $MANIFEST"
  [[ -n "$MAP" ]] || fail "no $MODE state entries in $MANIFEST"
}

# Unique locations of backed-up entries (manifest order).
backed_locations() { awk -F'\t' '$4 == "true" && !seen[$3]++ { print $3 }' <<<"$MAP"; }
escrow_locations() { awk -F'\t' '$4 == "false" && !seen[$3]++ { print $3 }' <<<"$MAP"; }
prefix_of_location() { awk -F'\t' -v l="$1" '$3 == l { print $2; exit }' <<<"$MAP"; }
ids_of_location() { awk -F'\t' -v l="$1" '$3 == l { print $1 }' <<<"$MAP" | paste -sd, -; }

print_plan() {
  echo "restore drill plan (mode=$MODE, manifest=$MANIFEST)"
  if [[ "$MODE" == "host" ]]; then
    echo "  data dir: $DATA"
    printf '  %-24s %-24s %s\n' MODULE ARCHIVE "STATE DIR"
  else
    printf '  %-24s %-24s %s\n' MODULE ARCHIVE VOLUME
  fi
  local id prefix loc backup
  while IFS=$'\t' read -r id prefix loc backup; do
    [[ -n "$id" ]] || continue
    if [[ "$backup" == "true" ]]; then
      printf '  %-24s %-24s %s\n' "$id" "data/$prefix/" "$loc"
    else
      printf '  %-24s %-24s %s\n' "$id" "(escrow, not archived)" "$loc"
    fi
  done <<<"$MAP"
  echo "  backed-up locations: $(backed_locations | wc -l)  escrowed: $(escrow_locations | wc -l)"
  echo "steps: up+smoke+seed+snapshot -> quiesce (all but core, backup-local) -> CreateBackup + archive coverage + VerifyBackup{restore_test} + copy out"
  echo "       -> stop + wipe state -> core + backup-local only -> RestoreBackup to staging -> stop -> copy into state locations"
  echo "       -> up (decoy bootstrap password) -> compare snapshot -> original password login, decoy refused -> smoke"
}

# ---------- BFF helpers (both modes) ----------

wait_http() {
  local url="$1" label="$2" deadline=$((SECONDS + TIMEOUT)) code
  until code="$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 2 "$url" 2>/dev/null)" && [[ "$code" =~ ^[23] ]]; do
    (( SECONDS < deadline )) || fail "$label not ready at $url (last HTTP ${code:-000})"
    sleep 2
  done
}

# wait_stack_http: auth + BFF answer, then a settle delay (modules register with
# core before their gRPC listeners are up; smoke.sh probes the ports).
wait_stack_http() {
  wait_http "$AUTH_URL/login" "auth-local"
  wait_http "$MEDIA_URL/healthz" "media-ui BFF"
  sleep "${RESTORE_DRILL_SETTLE_SEC:-15}"
}

# drill_login <password>: prints a BFF session token; returns 1 when the login fails.
drill_login() {
  local pass="$1" jar hdr redir csrf code loc tok
  jar="$(mktemp "$WORK/jar.XXXXXX")"
  hdr="$(mktemp "$WORK/hdr.XXXXXX")"
  redir="$(jq -rn --arg u "$MEDIA_URL/auth/callback" '$u | @uri')"
  curl -s -c "$jar" -b "$jar" -o /dev/null "$AUTH_URL/login?redirect=$redir" || true
  csrf="$(awk -F'\t' '($6=="muxcore-auth-csrf" || $6=="csrf-token"){print $7}' "$jar" | tr -d '\r' | tail -1)"
  if [[ -z "$csrf" ]]; then
    rm -f "$jar" "$hdr"
    echo "restore-drill: no CSRF cookie from $AUTH_URL/login" >&2
    return 1
  fi
  code="$(curl -s -c "$jar" -b "$jar" -D "$hdr" -o /dev/null -w '%{http_code}' \
    -X POST "$AUTH_URL/login/password" \
    --data-urlencode "username=$ADMIN_USER" \
    --data-urlencode "password=$pass" \
    --data-urlencode "csrf_token=$csrf" \
    --data-urlencode "redirect=$MEDIA_URL/auth/callback" || true)"
  loc="$(awk -F': ' 'tolower($1)=="location"{gsub(/\r/,"",$2); print $2; exit}' "$hdr")"
  if [[ ! "$code" =~ ^30[23]$ || "$loc" != *"code="* ]]; then
    rm -f "$jar" "$hdr"
    return 1
  fi
  curl -s -c "$jar" -b "$jar" -o /dev/null "$loc" || true
  tok="$(awk -F'\t' '$6=="session"{print $7}' "$jar" | tr -d '\r' | tail -1)"
  rm -f "$jar" "$hdr"
  [[ -n "$tok" ]] || return 1
  printf '%s' "$tok"
}

# bff_get <token> <path>: GET through the BFF as a bearer (non-browser) client.
bff_get() {
  curl -sf --max-time 30 -H "X-MuxCore-Session: $1" "$MEDIA_URL$2"
}

seed_userdata() {
  local tok="$1" now body
  now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  body="$(jq -cn --arg k "$DRILL_KEY" --arg now "$now" \
    '{progress: {($k): {id: $k, positionSec: 4242, durationSec: 7200, updatedAt: $now}},
      favorites: {($k): {id: $k, addedAt: $now}}}')"
  curl -sf --max-time 30 -X PUT -H "X-MuxCore-Session: $tok" -H 'Content-Type: application/json' \
    --data "$body" "$MEDIA_URL/api/userdata" >/dev/null || fail "seeding userdata via PUT /api/userdata"
}

# normalize_snapshot <me> <movies> <tv> <requests> <userdata>: stable JSON on stdout.
normalize_snapshot() {
  jq -nS --arg k "$DRILL_KEY" \
    --slurpfile me "$1" --slurpfile mv "$2" --slurpfile tv "$3" --slurpfile rq "$4" --slurpfile ud "$5" '
    def items: if type == "array" then . else (.items // .requests // []) end;
    {
      me: ($me[0] | {user_id, username}),
      movies: ([$mv[0] | items[] | {id, title, tmdb_id, year, has_file}] | sort_by(.id)),
      tv: ([$tv[0] | items[] | {id, title: (.title // .name), tmdb_id}] | sort_by(.id)),
      requests: ([$rq[0] | items[] | {id, title, status, itemId: (.itemId // .item_id), tmdbId: (.tmdbId // .tmdb_id), requestedBy: (.requestedBy // .requested_by)}] | sort_by(.id)),
      userdata: ($ud[0] | {progress: (.progress[$k] // null), favorite: (.favorites[$k] // null)})
    }'
}

# take_snapshot <token> <out.json>
take_snapshot() {
  local tok="$1" out="$2" d
  d="$(mktemp -d "$WORK/snap.XXXXXX")"
  bff_get "$tok" /api/me >"$d/me.json" || fail "GET /api/me"
  bff_get "$tok" "/api/movies?page=1&page_size=500" >"$d/movies.json" || fail "GET /api/movies"
  bff_get "$tok" "/api/tv?page=1&page_size=500" >"$d/tv.json" || fail "GET /api/tv"
  bff_get "$tok" /api/requests >"$d/requests.json" || fail "GET /api/requests"
  bff_get "$tok" /api/userdata >"$d/userdata.json" || fail "GET /api/userdata"
  normalize_snapshot "$d/me.json" "$d/movies.json" "$d/tv.json" "$d/requests.json" "$d/userdata.json" >"$out"
  rm -rf "$d"
}

# check_snapshot_seeded <snapshot.json>: the drill only proves something with data in it.
check_snapshot_seeded() {
  jq -e '(.movies | length) > 0 and (.requests | length) > 0 and .userdata.progress != null and (.me.user_id // "") != ""' "$1" >/dev/null \
    || fail "snapshot is missing seeded data (need >=1 movie, >=1 request, the $DRILL_KEY userdata entry): $(jq -c '{movies: (.movies|length), requests: (.requests|length), userdata, me}' "$1")"
}

# Modules whose state step 1 seeds and the snapshot reads: their archive entries
# must exist (catches a module writing its state outside the mapped location).
SEEDED_MODULES="auth-local media-movies media-tvshows request-media userdata-local"
seeded_prefixes() {
  local id
  for id in $SEEDED_MODULES; do
    # Host stacks without userdata-local keep userdata in the BFF dir (media-ui).
    [[ "$id" == userdata-local && "$MODE" == host && "${MVP_ENABLE_USERDATA_LOCAL:-1}" != "1" ]] && continue
    awk -F'\t' -v id="$id" '$1 == id && $4 == "true" { print $2; exit }' <<<"$MAP"
  done
}

# archive_prefixes <tar -t listing on stdin>: unique data/<prefix> names.
archive_prefixes() { awk -F/ '$1 == "data" && NF >= 3 && $2 != "" { print $2 }' | sort -u; }

# check_archive_coverage <prefix list file> <required prefixes (one per line)>
check_archive_coverage() {
  local have="$1" want="$2" p missing=() unknown=()
  while IFS= read -r p; do
    [[ -n "$p" ]] || continue
    grep -qxF "$p" "$have" || missing+=("$p")
  done <<<"$want"
  while IFS= read -r p; do
    [[ -n "$p" ]] || continue
    awk -F'\t' -v p="$p" '$4 == "true" && $2 == p { f = 1 } END { exit !f }' <<<"$MAP" || unknown+=("$p")
  done <"$have"
  ((${#unknown[@]} == 0)) || fail "archive has entries with no manifest mapping (would not be restored): ${unknown[*]} (BACKUP_SOURCE_DIRS drifted from household-manifest.yaml?)"
  ((${#missing[@]} == 0)) || fail "archive is missing non-empty state: ${missing[*]}"
}

# ---------- host mode ----------

RUN_HOST="$ROOT/run-host.sh"

host_build_backupctl() {
  (cd "$ROOT" && go build -o bin/backupctl ./cmd/backupctl) || fail "building cmd/backupctl"
}

host_backupctl() { "$ROOT/bin/backupctl" "$@"; }

host_up() {
  log "host: ./run-host.sh up (backup-local enabled)"
  MVP_ENABLE_BACKUP_LOCAL=1 "$RUN_HOST" up
  wait_http "http://127.0.0.1:9303/health" "backup-local"
}

host_smoke() {
  log "host: smoke.sh"
  (cd "$ROOT" && MVP_ADMIN_PASSWORD="$ORIG_PASS" ./smoke.sh) || fail "smoke.sh failed"
}

host_quiesce() {
  local f name stopped=()
  for f in "$ROOT"/run/*.pid; do
    [[ -f "$f" ]] || continue
    name="$(basename "$f" .pid)"
    case "$name" in core|backup-local) continue ;; esac
    "$RUN_HOST" stop-one "$name" >/dev/null
    stopped+=("$name")
  done
  log "host: quiesced ${#stopped[@]} services (core + backup-local left running)"
}

# host_nonempty_prefixes: archive prefixes whose host dir holds at least one file now.
host_nonempty_prefixes() {
  local loc
  while IFS= read -r loc; do
    [[ -d "$DATA/$loc" ]] || continue
    if [[ -n "$(find "$DATA/$loc" -type f -print -quit)" ]]; then
      prefix_of_location "$loc"
    fi
  done < <(backed_locations)
}

host_backup() {
  local want resp
  want="$(host_nonempty_prefixes; seeded_prefixes)"
  resp="$(host_backupctl create)" || fail "CreateBackup"
  BACKUP_ID="$(jq -r '.backup.id // empty' <<<"$resp")"
  [[ "$BACKUP_ID" =~ ^backup_[0-9]+$ ]] || fail "CreateBackup returned no id: $resp"
  local archive="$DATA/backup/$BACKUP_ID.tar.gz"
  [[ -f "$archive" ]] || fail "archive not found at $archive"
  log "host: created $BACKUP_ID ($(jq -r '.backup.sizeBytes // 0' <<<"$resp") bytes)"
  tar -tzf "$archive" | archive_prefixes >"$WORK/archive-prefixes.txt"
  check_archive_coverage "$WORK/archive-prefixes.txt" "$want"
  log "host: archive covers $(wc -l <"$WORK/archive-prefixes.txt") state dirs: $(paste -sd' ' "$WORK/archive-prefixes.txt")"
  resp="$(host_backupctl verify -id "$BACKUP_ID" -restore-test)" || fail "VerifyBackup"
  jq -e '.valid == true and .restoreTestOk == true' <<<"$resp" >/dev/null || fail "VerifyBackup not valid: $resp"
  log "host: VerifyBackup ok ($(jq -r '.filesInArchive // 0' <<<"$resp") files, restore_test ok)"
  mkdir -p "$WORK/backup"
  cp -p "$archive" "$WORK/backup/"
  [[ -f "$DATA/backup/index.json" ]] && cp -p "$DATA/backup/index.json" "$WORK/backup/"
  local loc
  while IFS= read -r loc; do
    [[ -n "$loc" && -d "$DATA/$loc" ]] || continue
    mkdir -p "$WORK/escrow"
    cp -a "$DATA/$loc" "$WORK/escrow/$loc"
    log "host: escrowed $loc (backup: false; key material kept outside the archive)"
  done < <(escrow_locations)
}

host_wipe() {
  local loc
  [[ -n "$DATA" && "$DATA" != "/" ]] || fail "refusing to wipe with DATA=$DATA"
  while IFS=$'\t' read -r _ _ loc _; do
    [[ -n "$loc" ]] || continue
    rm -rf -- "${DATA:?}/$loc"
  done <<<"$MAP"
  rm -rf -- "${DATA:?}/backup" "${DATA:?}/restore"
  log "host: wiped state dirs ($(awk -F'\t' '!seen[$3]++' <<<"$MAP" | wc -l)) + backup/ + restore/ under $DATA (library media and downloads kept)"
}

# host_apply_restore <staging dir>: copy staging/data/<prefix>/ into each state dir.
# Sets RESTORED_MODULES (count of module ids) and RESTORED_LIST.
host_apply_restore() {
  local staging="$1" loc prefix src ids
  RESTORED_MODULES=0
  RESTORED_LIST=""
  while IFS= read -r loc; do
    [[ -n "$loc" ]] || continue
    prefix="$(prefix_of_location "$loc")"
    src="$staging/data/$prefix"
    [[ -d "$src" ]] || continue
    rm -rf -- "${DATA:?}/$loc"
    mkdir -p "$DATA/$loc"
    cp -a "$src/." "$DATA/$loc/"
    ids="$(ids_of_location "$loc")"
    RESTORED_MODULES=$((RESTORED_MODULES + $(tr ',' '\n' <<<"$ids" | grep -c .)))
    RESTORED_LIST+="${RESTORED_LIST:+ }$ids"
  done < <(backed_locations)
  while IFS= read -r loc; do
    [[ -n "$loc" && -d "$WORK/escrow/$loc" ]] || continue
    rm -rf -- "${DATA:?}/$loc"
    cp -a "$WORK/escrow/$loc" "$DATA/$loc"
  done < <(escrow_locations)
}

host_fresh_restore() {
  log "host: fresh install: core only, then backup-local with the copied archive"
  MVP_ADMIN_PASSWORD="$DECOY_PASS" "$RUN_HOST" restart core >/dev/null
  mkdir -p "$DATA/backup"
  cp -p "$WORK/backup/$BACKUP_ID.tar.gz" "$DATA/backup/"
  MVP_ENABLE_BACKUP_LOCAL=1 MVP_ADMIN_PASSWORD="$DECOY_PASS" "$RUN_HOST" restart backup-local >/dev/null
  wait_http "http://127.0.0.1:9303/health" "backup-local (fresh)"
  local resp staging
  resp="$(host_backupctl list)" || fail "ListBackups on the fresh install"
  jq -e --arg id "$BACKUP_ID" '[.backups[]? | .id] | index($id) != null' <<<"$resp" >/dev/null \
    || fail "fresh backup-local did not index the copied archive $BACKUP_ID: $resp"
  log "host: fresh backup-local re-indexed $BACKUP_ID"
  staging="$DATA/restore/restore-drill-$BACKUP_ID"
  resp="$(host_backupctl restore -id "$BACKUP_ID" -target "$staging")" || fail "RestoreBackup"
  log "host: RestoreBackup -> $staging ($(jq -r '.filesRestored // 0' <<<"$resp") files)"
  "$RUN_HOST" stop >/dev/null
  host_apply_restore "$staging"
  rm -rf -- "$staging"
  ((RESTORED_MODULES > 0)) || fail "nothing restored from $staging"
  log "host: restored $RESTORED_MODULES modules: $RESTORED_LIST"
}

host_start_all() {
  log "host: ./run-host.sh up on restored state (decoy bootstrap password)"
  MVP_ENABLE_BACKUP_LOCAL=1 MVP_ADMIN_PASSWORD="$DECOY_PASS" "$RUN_HOST" up >/dev/null
}

# ---------- compose mode ----------

compose_init() {
  # shellcheck disable=SC1091
  source "$ROOT/scripts/lib/registry-smoke.sh"
  registry_smoke_root="$ROOT"
  registry_smoke_enable
  PROJECT="$(registry_smoke_compose_project)"
}

# backup-local plus every profile in COMPOSE_PROFILES (a --profile flag replaces
# the env var, so the stack's other profiles, e.g. the fixture acquisition
# peers, would drop out of the project).
dc() {
  local -a prof=(--profile backup-local)
  local p
  for p in ${COMPOSE_PROFILES//,/ }; do
    [[ "$p" == backup-local ]] || prof+=(--profile "$p")
  done
  registry_smoke_compose "${prof[@]}" "$@"
}

# Services that share core's network namespace (network_mode: service:core, the
# dev-profile security sidecars). Podman refuses to remove core while they exist,
# so compose cannot recreate core (and podman 4.x's compat API makes compose
# recreate on every `up`). Remove them before an `up` that may touch core; the
# next `up` recreates them. Harmless with docker.
free_core_netns() {
  local -a svcs=()
  mapfile -t svcs < <(dc config --format json | jq -r '.services | to_entries[] | select(.value.network_mode == "service:core") | .key')
  ((${#svcs[@]} == 0)) || dc rm -s -f "${svcs[@]}" >/dev/null 2>&1 || true
}
vol() { registry_smoke_volume "$1"; }
alpine() { registry_smoke_cli run --rm "$@"; }
compose_backupctl() { registry_smoke_cmd_backupctl "$@"; }

compose_up() {
  log "compose: up -d (project $PROJECT, profile backup-local)"
  free_core_netns
  dc up -d
  wait_stack_http
}

compose_smoke() {
  log "compose: smoke.sh (registry mode)"
  # A session token from before the wipe/restore is not evidence; log in again.
  rm -f "${MVP_TOKEN_FILE:-$ROOT/run/admin.token}"
  (cd "$ROOT" && MUXCORE_SMOKE_REGISTRY=1 MVP_ADMIN_PASSWORD="$ORIG_PASS" ./smoke.sh) || fail "smoke.sh failed"
}

compose_quiesce() {
  local -a svcs=()
  local s
  while IFS= read -r s; do
    case "$s" in ""|core|backup-local) ;; *) svcs+=("$s") ;; esac
  done < <(dc ps --services --status running)
  ((${#svcs[@]} == 0)) || dc stop "${svcs[@]}"
  log "compose: quiesced ${#svcs[@]} services (core + backup-local left running)"
}

compose_backup() {
  local resp loc owner
  # Record volume owners (restored files are re-owned to them).
  : >"$WORK/owners.tsv"
  while IFS=$'\t' read -r _ _ loc _; do
    [[ -n "$loc" ]] || continue
    grep -q "^$loc"$'\t' "$WORK/owners.tsv" && continue
    owner="$(alpine -v "$(vol "$loc"):/v:ro" "$REGISTRY_SMOKE_ALPINE_IMAGE" stat -c '%u:%g' /v 2>/dev/null || true)"
    [[ -n "$owner" ]] && printf '%s\t%s\n' "$loc" "$owner" >>"$WORK/owners.tsv"
  done <<<"$MAP"
  resp="$(compose_backupctl create)" || fail "CreateBackup"
  BACKUP_ID="$(jq -r '.backup.id // empty' <<<"$resp")"
  [[ "$BACKUP_ID" =~ ^backup_[0-9]+$ ]] || fail "CreateBackup returned no id: $resp"
  log "compose: created $BACKUP_ID"
  mkdir -p "$WORK/backup"
  alpine -v "$(vol backup-data):/b:ro" "$REGISTRY_SMOKE_ALPINE_IMAGE" cat "/b/$BACKUP_ID.tar.gz" >"$WORK/backup/$BACKUP_ID.tar.gz" \
    || fail "copying $BACKUP_ID out of the backup-data volume"
  tar -tzf "$WORK/backup/$BACKUP_ID.tar.gz" | archive_prefixes >"$WORK/archive-prefixes.txt"
  check_archive_coverage "$WORK/archive-prefixes.txt" "$(seeded_prefixes)"
  log "compose: archive covers $(wc -l <"$WORK/archive-prefixes.txt") volumes: $(paste -sd' ' "$WORK/archive-prefixes.txt")"
  resp="$(compose_backupctl verify -id "$BACKUP_ID" -restore-test)" || fail "VerifyBackup"
  jq -e '.valid == true and .restoreTestOk == true' <<<"$resp" >/dev/null || fail "VerifyBackup not valid: $resp"
  log "compose: VerifyBackup ok (restore_test)"
  while IFS= read -r loc; do
    [[ -n "$loc" ]] || continue
    mkdir -p "$WORK/escrow"
    alpine -v "$(vol "$loc"):/v:ro" "$REGISTRY_SMOKE_ALPINE_IMAGE" tar -C /v -czf - . >"$WORK/escrow/$loc.tgz" \
      || fail "escrowing volume $loc"
    log "compose: escrowed volume $loc (backup: false)"
  done < <(escrow_locations)
}

compose_wipe() {
  local loc
  dc down --remove-orphans
  while IFS= read -r loc; do
    [[ -n "$loc" ]] || continue
    registry_smoke_cli volume rm -f "$(vol "$loc")" >/dev/null || fail "removing volume $(vol "$loc") (still in use?)"
  done < <({ awk -F'\t' '!seen[$3]++ { print $3 }' <<<"$MAP"; printf '%s\n' backup-data backup-restore-data; })
  log "compose: removed state volumes + backup-data + backup-restore-data (media volumes kept)"
}

compose_apply_restore() {
  local staging="$1" loc prefix owner ids rc
  RESTORED_MODULES=0
  RESTORED_LIST=""
  while IFS= read -r loc; do
    [[ -n "$loc" ]] || continue
    prefix="$(prefix_of_location "$loc")"
    owner="$(awk -F'\t' -v l="$loc" '$1 == l { print $2; exit }' "$WORK/owners.tsv")"
    owner="${owner:-${RESTORE_DRILL_VOLUME_OWNER:-1000:1000}}"
    rc=0
    # shellcheck disable=SC2016 # expanded by the container shell ($1..$3)
    alpine -v "$(vol backup-restore-data):/restore:ro" -v "$(vol "$loc"):/target" "$REGISTRY_SMOKE_ALPINE_IMAGE" \
      sh -c 'set -e; src="/restore/$1/data/$2"; [ -d "$src" ] || exit 3; find /target -mindepth 1 -delete; cp -a "$src/." /target/; chown -R "$3" /target' \
      sh "$staging" "$prefix" "$owner" || rc=$?
    case "$rc" in
      0) ids="$(ids_of_location "$loc")"
         RESTORED_MODULES=$((RESTORED_MODULES + $(tr ',' '\n' <<<"$ids" | grep -c .)))
         RESTORED_LIST+="${RESTORED_LIST:+ }$ids" ;;
      3) ;;
      *) fail "restoring volume $loc (rc=$rc)" ;;
    esac
  done < <(backed_locations)
  while IFS= read -r loc; do
    [[ -n "$loc" && -f "$WORK/escrow/$loc.tgz" ]] || continue
    alpine -i -v "$(vol "$loc"):/v" "$REGISTRY_SMOKE_ALPINE_IMAGE" sh -c 'find /v -mindepth 1 -delete; tar -C /v -xzf -' \
      <"$WORK/escrow/$loc.tgz" || fail "restoring escrowed volume $loc"
  done < <(escrow_locations)
}

compose_fresh_restore() {
  local resp staging
  log "compose: fresh install: create volumes, start core + backup-local only"
  dc create
  free_core_netns
  dc up -d --no-deps core backup-local
  alpine -i -v "$(vol backup-data):/b" "$REGISTRY_SMOKE_ALPINE_IMAGE" sh -c "cat > /b/$BACKUP_ID.tar.gz" \
    <"$WORK/backup/$BACKUP_ID.tar.gz" || fail "copying the archive into backup-data"
  local deadline=$((SECONDS + TIMEOUT))
  until resp="$(compose_backupctl list 2>/dev/null)" && jq -e --arg id "$BACKUP_ID" '[.backups[]? | .id] | index($id) != null' <<<"$resp" >/dev/null; do
    (( SECONDS < deadline )) || fail "fresh backup-local did not index $BACKUP_ID: ${resp:-no response}"
    sleep 3
  done
  log "compose: fresh backup-local re-indexed $BACKUP_ID"
  staging="restore-drill-$BACKUP_ID"
  resp="$(compose_backupctl restore -id "$BACKUP_ID" -target "/data/restore/$staging")" || fail "RestoreBackup"
  log "compose: RestoreBackup -> /data/restore/$staging"
  dc stop core backup-local
  compose_apply_restore "$staging"
  alpine -v "$(vol backup-restore-data):/r" "$REGISTRY_SMOKE_ALPINE_IMAGE" rm -rf "/r/$staging" || true
  ((RESTORED_MODULES > 0)) || fail "nothing restored from $staging"
  log "compose: restored $RESTORED_MODULES modules: $RESTORED_LIST"
}

compose_start_all() {
  log "compose: up -d on restored volumes (decoy bootstrap password)"
  free_core_netns
  MVP_ADMIN_PASSWORD="$DECOY_PASS" dc up -d
}

# ---------- driver ----------

cleanup() {
  local rc=$?
  if [[ -n "$WORK" && -d "$WORK" ]]; then
    if [[ "$rc" -eq 0 && "$KEEP_WORKDIR" -eq 0 && "$DRY_RUN" -eq 0 ]]; then
      rm -rf -- "$WORK"
    elif [[ "$DRY_RUN" -eq 0 ]]; then
      echo "restore-drill: work dir kept: $WORK (archive copy, escrow, snapshots; contains secrets)" >&2
    fi
  fi
}

main() {
  local rc=0
  parse_args "$@" || rc=$?
  if [[ "$rc" -eq 3 ]]; then usage; return 0; fi
  if [[ "$rc" -ne 0 ]]; then usage >&2; return "$rc"; fi
  load_map
  if [[ "$DRY_RUN" -eq 1 ]]; then
    print_plan
    return 0
  fi
  for t in curl jq tar; do command -v "$t" >/dev/null || fail "$t not found"; done
  if [[ -z "$WORK" ]]; then
    WORK="$(mktemp -d "${TMPDIR:-/tmp}/muxcore-restore-drill.XXXXXX")"
  fi
  mkdir -p "$WORK"
  chmod 700 "$WORK"
  trap cleanup EXIT
  print_plan
  if [[ "$MODE" == "compose" ]]; then compose_init; else host_build_backupctl; fi

  # shellcheck disable=SC1091
  if [[ -f "$ROOT/.env" ]]; then source "$ROOT/.env"; fi
  # shellcheck disable=SC1091
  source "$ROOT/scripts/lib/admin-secret.sh"

  # 1. up + seed + snapshot
  log "step 1/5: up, smoke (seed), userdata seed, snapshot"
  if [[ "$MODE" == "host" ]]; then host_up; else compose_up; fi
  wait_stack_http
  mvp_admin_password_load "$ROOT"
  ORIG_PASS="${MVP_ADMIN_PASSWORD:-}"
  [[ -n "$ORIG_PASS" ]] || fail "no admin password (MVP_ADMIN_PASSWORD / data/auth/admin.password)"
  (umask 077 && printf '%s\n' "$ORIG_PASS" >"$WORK/original.password")
  DECOY_PASS="drill-decoy-$(mvp_gen_secret 12)"
  if [[ "$MODE" == "host" ]]; then host_smoke; else compose_smoke; fi
  local tok
  tok="$(drill_login "$ORIG_PASS")" || fail "admin login with the original password failed before backup"
  seed_userdata "$tok"
  take_snapshot "$tok" "$WORK/snapshot.json"
  check_snapshot_seeded "$WORK/snapshot.json"
  log "snapshot: $(jq -c '{user: .me.username, movies: (.movies|length), tv: (.tv|length), requests: (.requests|length), userdata: (.userdata.progress.positionSec)}' "$WORK/snapshot.json")"

  # 2. quiesce + backup + verify + copy out
  log "step 2/5: quiesce, CreateBackup, VerifyBackup{restore_test}, copy out"
  if [[ "$MODE" == "host" ]]; then host_quiesce; host_backup; else compose_quiesce; compose_backup; fi

  # 3. wipe
  log "step 3/5: stop and wipe module state"
  if [[ "$MODE" == "host" ]]; then "$RUN_HOST" stop >/dev/null; host_wipe; else compose_wipe; fi

  # 4. fresh install + restore
  log "step 4/5: fresh install, RestoreBackup, map archive back into state locations"
  if [[ "$MODE" == "host" ]]; then host_fresh_restore; else compose_fresh_restore; fi

  # 5. start, compare, original-password login, smoke
  log "step 5/5: start everything, compare snapshot, original-password login, smoke"
  if [[ "$MODE" == "host" ]]; then host_start_all; else compose_start_all; fi
  wait_stack_http
  if drill_login "$DECOY_PASS" >/dev/null; then
    fail "decoy bootstrap password logged in: auth-local re-bootstrapped instead of using the restored database"
  fi
  tok="$(drill_login "$ORIG_PASS")" || fail "admin login with the ORIGINAL password failed after restore"
  log "auth: original password accepted, decoy refused (auth-local did not re-bootstrap)"
  take_snapshot "$tok" "$WORK/snapshot-restored.json"
  if ! diff -u "$WORK/snapshot.json" "$WORK/snapshot-restored.json"; then
    fail "restored snapshot differs (see diff above; files in $WORK)"
  fi
  log "snapshot equal: $(jq -c '{movies: [.movies[].title], requests: [.requests[].id]}' "$WORK/snapshot-restored.json")"
  if [[ "$MODE" == "host" ]]; then host_smoke; else compose_smoke; fi
  echo "PASS: restore drill ($RESTORED_MODULES modules restored, snapshot equal)"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
