#!/usr/bin/env bash
# shellcheck disable=SC2317,SC2034 # case_* run indirectly via run_case; vars feed the sourced drill
# Offline tests for scripts/restore-drill.sh and scripts/lib/state-map.sh
# (T-M2-05, FR-BAK-004): argument parsing, manifest -> state-location mapping,
# archive coverage, the host restore copy, snapshot normalization and the
# registry (grpcurl) backupctl translation. No stack, network or containers.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DRILL="$SCRIPT_DIR/restore-drill.sh"
MANIFEST="$ROOT/household-manifest.yaml"
failed=0
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fail() { echo "FAIL: $*" >&2; failed=1; }
ok() { echo "ok: $*"; }

# run_case <name> <fn>: run fn in a subshell with errexit really on (it is
# ignored inside `if`/`&&` conditions), report ok/FAIL.
run_case() {
  local name="$1" rc
  set +e
  ( set -e; "$2" )
  rc=$?
  set -e
  if [[ "$rc" -eq 0 ]]; then ok "$name"; else fail "$name"; fi
}

for t in yq jq; do
  command -v "$t" >/dev/null || { echo "skip: $t not found (restore-drill tests need mikefarah yq v4 + jq)"; exit 0; }
done

# expect_rc <name> <want-rc> <output-pattern|-> <args...>
expect_rc() {
  local name="$1" want="$2" pattern="$3" out rc=0
  shift 3
  out="$(bash "$DRILL" "$@" 2>&1)" || rc=$?
  if [[ "$rc" != "$want" ]]; then
    fail "$name: rc=$rc want $want"$'\n'"$out"
  elif [[ "$pattern" != "-" ]] && ! grep -qE -- "$pattern" <<<"$out"; then
    fail "$name: output does not match /$pattern/"$'\n'"$out"
  else
    ok "$name"
  fi
}

# --- argument parsing ---
expect_rc "help" 0 "^usage: scripts/restore-drill.sh" --help
expect_rc "no args" 2 "--mode host\|compose is required"
expect_rc "bad mode" 2 "--mode must be host or compose \(got: vm\)" --mode vm --dry-run
expect_rc "missing mode value" 2 "--mode needs a value" --mode
expect_rc "unknown flag" 2 "unknown argument: --wipe" --mode host --wipe
expect_rc "real run needs --confirm-wipe" 2 "refusing to run without --confirm-wipe" --mode host
expect_rc "missing manifest" 2 "manifest not found" --mode host --dry-run --manifest "$TMP/nope.yaml"

# --- dry-run plans on the real manifest ---
expect_rc "host plan: shared media-ui dir" 0 "admin-ui +data/media-ui/ +media-ui" --mode host --dry-run
expect_rc "host plan: key material escrowed" 0 "encryption-aesgcm +\(escrow, not archived\) +encryption" --mode=host --dry-run
expect_rc "host plan: counts" 0 "backed-up locations: 24  escrowed: 1" --mode host --dry-run
out="$(bash "$DRILL" --mode host --dry-run)"
if grep -qE '^  (plex|emby) ' <<<"$out"; then fail "host plan lists plex/emby (no host-mode state)"; else ok "host plan skips entries without host:"; fi
expect_rc "compose plan: module-id prefixes on volumes" 0 "auth-local +data/auth-local/ +auth-data" --mode compose --dry-run
expect_rc "compose plan: counts" 0 "backed-up locations: 27  escrowed: 1" --mode compose --dry-run

# --- state-map lib ---
# shellcheck disable=SC1091
source "$ROOT/scripts/lib/state-map.sh"
src="$(state_map_host_sources "$MANIFEST" /D)"
n="$(tr ',' '\n' <<<"$src" | wc -l)"
uniq_n="$(tr ',' '\n' <<<"$src" | sort -u | wc -l)"
if [[ "$n" == 24 && "$uniq_n" == 24 ]] && grep -q '^/D/storage,/D/auth,' <<<"$src" && ! grep -q '/D/encryption' <<<"$src"; then
  ok "host backup sources: 24 unique dirs, manifest order, key dir excluded"
else
  fail "host backup sources: $src"
fi

cat >"$TMP/bad-host.yaml" <<'EOF'
state:
  auth-local: {volume: auth-data, mount: /data, host: ../etc, kind: sqlite, personal: true}
EOF
if state_map_tsv "$TMP/bad-host.yaml" host >/dev/null 2>&1; then fail "host: ../etc accepted"; else ok "host must be one path segment"; fi

# Every manifest host dir must be the one run-host.sh actually uses.
while IFS=$'\t' read -r id _ loc _; do
  if grep -qF "\$DATA/$loc" "$ROOT/run-host.sh"; then continue; fi
  fail "manifest state.$id.host=$loc not referenced as \$DATA/$loc in run-host.sh"
done < <(state_map_tsv "$MANIFEST" host)
ok "manifest host dirs match run-host.sh"

# Vendored proto (grpcurl in compose mode) must match backup-local's.
upstream="$ROOT/../backup-local/proto/muxcore/backup/v1/backup.proto"
if [[ -f "$upstream" ]]; then
  if diff -q "$upstream" "$ROOT/proto/muxcore/backup/v1/backup.proto" >/dev/null; then
    ok "vendored backup.proto matches backup-local"
  else
    fail "proto/muxcore/backup/v1/backup.proto drifted from $upstream"
  fi
else
  echo "skip: $upstream not found (outside the umbrella workspace)"
fi

# --- drill internals (sourced; main does not run) ---
cat >"$TMP/manifest.yaml" <<'EOF'
state:
  auth-local: {volume: auth-data, mount: /data, host: auth, kind: sqlite, personal: true}
  encryption-aesgcm: {volume: enc-data, mount: /data, host: encryption, kind: files, personal: false, backup: false}
  admin-ui: {volume: admin-ui-data, mount: /data, host: media-ui, kind: json, personal: true}
  media-ui: {volume: media-ui-data, mount: /data/media-ui, host: media-ui, kind: json, personal: true}
  media-movies: {volume: movies-data, mount: /data, host: movies, kind: sqlite, personal: false}
  plex: {volume: plex-data, mount: /data, kind: files, personal: true}
EOF

case_1() {
  # shellcheck disable=SC1090
  source "$DRILL"
  MODE=host MANIFEST="$TMP/manifest.yaml" DATA="$TMP/data" WORK="$TMP/work"
  load_map
  mkdir -p "$DATA/auth" "$DATA/media-ui" "$DATA/movies" "$DATA/encryption" "$DATA/library" \
    "$WORK/escrow/encryption" "$TMP/staging/data/auth" "$TMP/staging/data/media-ui/sub"
  echo stale >"$DATA/auth/stale.db"
  echo bootstrap >"$DATA/auth/admin.password"
  echo fresh-key >"$DATA/encryption/master.key"
  echo media >"$DATA/library/film.mkv"
  echo keep-empty >"$DATA/movies/movies.db"
  echo restored >"$TMP/staging/data/auth/auth.db"
  echo original >"$TMP/staging/data/auth/admin.password"
  echo sessions >"$TMP/staging/data/media-ui/sub/sessions.json"
  echo escrowed-key >"$WORK/escrow/encryption/master.key"
  host_apply_restore "$TMP/staging"
  [[ "$RESTORED_MODULES" == 3 ]] || { echo "RESTORED_MODULES=$RESTORED_MODULES want 3 ($RESTORED_LIST)"; exit 1; }
  [[ "$RESTORED_LIST" == "auth-local admin-ui,media-ui" ]] || { echo "RESTORED_LIST=$RESTORED_LIST"; exit 1; }
  [[ ! -e "$DATA/auth/stale.db" && "$(cat "$DATA/auth/auth.db")" == restored ]] || { echo "auth dir not replaced"; exit 1; }
  [[ "$(cat "$DATA/auth/admin.password")" == original ]] || { echo "admin.password not restored"; exit 1; }
  [[ "$(cat "$DATA/media-ui/sub/sessions.json")" == sessions ]] || { echo "nested media-ui file missing"; exit 1; }
  [[ "$(cat "$DATA/encryption/master.key")" == escrowed-key ]] || { echo "escrow not restored"; exit 1; }
  [[ "$(cat "$DATA/movies/movies.db")" == keep-empty ]] || { echo "dir absent from archive was touched"; exit 1; }
  [[ -f "$DATA/library/film.mkv" ]] || { echo "library media touched"; exit 1; }
}
run_case "host restore copy: replace mapped dirs, shared dir once, escrow, leave unmapped" case_1

case_2() {
  # shellcheck disable=SC1090
  source "$DRILL"
  MODE=host
  MANIFEST="$TMP/manifest.yaml"
  export MODE
  load_map
  printf 'data/auth/auth.db\ndata/media-ui/x.json\nmodules/x/state.bin\ndata/\n' | archive_prefixes >"$TMP/have.txt"
  [[ "$(paste -sd' ' "$TMP/have.txt")" == "auth media-ui" ]] || { echo "prefixes: $(cat "$TMP/have.txt")"; exit 1; }
  check_archive_coverage "$TMP/have.txt" "$(printf 'auth\nmedia-ui\n')" >/dev/null 2>&1 || { echo "coverage rejected a complete archive"; exit 1; }
  if (check_archive_coverage "$TMP/have.txt" "$(printf 'auth\nmovies\n')") >/dev/null 2>&1; then echo "missing movies accepted"; exit 1; fi
  printf 'auth\ndata\n' >"$TMP/odd.txt"
  if (check_archive_coverage "$TMP/odd.txt" auth) >/dev/null 2>&1; then echo "unmapped prefix accepted"; exit 1; fi
  printf 'encryption\n' >"$TMP/key.txt"
  if (check_archive_coverage "$TMP/key.txt" "") >/dev/null 2>&1; then echo "archived backup:false dir accepted"; exit 1; fi
  [[ "$(seeded_prefixes | paste -sd' ')" == "auth movies" ]] || { echo "seeded: $(seeded_prefixes | paste -sd' ')"; exit 1; }
}
run_case "archive coverage: missing, unmapped and key-material prefixes fail" case_2

case_3() {
  # shellcheck disable=SC1090
  source "$DRILL"
  d="$TMP/snap"
  mkdir -p "$d"
  echo '{"user_id":"u1","username":"admin","roles":["admin"]}' >"$d/me.json"
  echo '{"items":[{"id":"mv_2","title":"B","tmdb_id":2,"year":2000,"has_file":false,"created_at":"x"},{"id":"mv_1","title":"A","tmdb_id":1,"year":1999,"has_file":true}],"total":2}' >"$d/movies.json"
  echo '{"items":[{"id":"tv_1","name":"Show","tmdb_id":9}]}' >"$d/tv.json"
  echo '[{"id":"req_1","title":"A","status":"added","itemId":"mv_1","tmdbId":1,"requestedBy":"u1","updatedAt":"t1"}]' >"$d/requests.json"
  echo '{"user_id":"u1","progress":{"restore-drill":{"positionSec":4242}},"favorites":{"restore-drill":{"id":"restore-drill"}}}' >"$d/userdata.json"
  normalize_snapshot "$d/me.json" "$d/movies.json" "$d/tv.json" "$d/requests.json" "$d/userdata.json" >"$d/a.json"
  [[ "$(jq -r '[.movies[].id] | join(",")' "$d/a.json")" == "mv_1,mv_2" ]] || { echo "movies not sorted"; exit 1; }
  [[ "$(jq -r '.tv[0].title' "$d/a.json")" == Show ]] || { echo "tv name fallback"; exit 1; }
  [[ "$(jq -r '.userdata.progress.positionSec' "$d/a.json")" == 4242 ]] || { echo "userdata"; exit 1; }
  jq -e 'has("roles") | not' "$d/a.json" >/dev/null
  # Reordered input and volatile fields (created_at, updatedAt) normalize equal.
  echo '{"items":[{"id":"mv_1","title":"A","tmdb_id":1,"year":1999,"has_file":true,"created_at":"y"},{"id":"mv_2","title":"B","tmdb_id":2,"year":2000,"has_file":false}]}' >"$d/movies.json"
  echo '[{"id":"req_1","title":"A","status":"added","itemId":"mv_1","tmdbId":1,"requestedBy":"u1","updatedAt":"t2"}]' >"$d/requests.json"
  normalize_snapshot "$d/me.json" "$d/movies.json" "$d/tv.json" "$d/requests.json" "$d/userdata.json" >"$d/b.json"
  diff -q "$d/a.json" "$d/b.json" >/dev/null || { echo "equal data normalized differently"; exit 1; }
  echo '[{"id":"req_1","title":"A","status":"denied","itemId":"mv_1","tmdbId":1,"requestedBy":"u1"}]' >"$d/requests.json"
  normalize_snapshot "$d/me.json" "$d/movies.json" "$d/tv.json" "$d/requests.json" "$d/userdata.json" >"$d/c.json"
  if diff -q "$d/a.json" "$d/c.json" >/dev/null; then echo "changed request status not detected"; exit 1; fi
  check_snapshot_seeded "$d/a.json"
  echo '{"items":[]}' >"$d/movies.json"
  normalize_snapshot "$d/me.json" "$d/movies.json" "$d/tv.json" "$d/requests.json" "$d/userdata.json" >"$d/e.json"
  if (check_snapshot_seeded "$d/e.json") >/dev/null 2>&1; then echo "empty snapshot accepted"; exit 1; fi
}
run_case "snapshot normalization: stable order, volatile fields dropped, changes detected" case_3

# --- registry (compose) backupctl -> grpcurl translation ---
case_4() {
  # shellcheck disable=SC1091
  source "$ROOT/scripts/lib/registry-smoke.sh"
  registry_smoke_root="$TMP/root"
  registry_smoke_network() { printf 'proj_muxcore'; }
  registry_smoke_cli() { printf '%s\n' "$@" >"$TMP/cli-args.txt"; echo '{"backup":{"id":"backup_1"}}'; }
  out="$(registry_smoke_cmd_backupctl create -source /source/x)"
  [[ "$(jq -r .backup.id <<<"$out")" == backup_1 ]] || { echo "create output: $out"; exit 1; }
  grep -qx -- "$TMP/root/proto:/proto:ro" "$TMP/cli-args.txt" || { echo "proto not mounted"; cat "$TMP/cli-args.txt"; exit 1; }
  grep -qx 'backup-local:9302' "$TMP/cli-args.txt" || { echo "default addr"; exit 1; }
  grep -qx 'muxcore.backup.v1.BackupService/CreateBackup' "$TMP/cli-args.txt" || { echo "method"; exit 1; }
  grep -qx '{"source_paths":\["/source/x"\]}' "$TMP/cli-args.txt" || { echo "create payload"; cat "$TMP/cli-args.txt"; exit 1; }
  registry_smoke_cmd_backupctl -addr 10.0.0.1:9302 restore -id backup_1 -target /data/restore/d >/dev/null
  grep -qx '10.0.0.1:9302' "$TMP/cli-args.txt" || { echo "-addr ignored"; exit 1; }
  grep -qx '{"backup_id":"backup_1","target_path":"/data/restore/d"}' "$TMP/cli-args.txt" || { echo "restore payload"; exit 1; }
  registry_smoke_cmd_backupctl verify -id backup_1 -restore-test >/dev/null
  grep -qx '{"backup_id":"backup_1","restore_test":true}' "$TMP/cli-args.txt" || { echo "verify payload"; exit 1; }
  if registry_smoke_cmd_backupctl restore -id backup_1 >/dev/null 2>&1; then echo "restore without -target accepted"; exit 1; fi
  if registry_smoke_cmd_backupctl frobnicate >/dev/null 2>&1; then echo "unknown cmd accepted"; exit 1; fi
}
run_case "registry backupctl: grpcurl args and JSON payloads" case_4

exit "$failed"
