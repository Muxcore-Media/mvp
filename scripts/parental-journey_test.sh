#!/usr/bin/env bash
# Offline tests for the restricted-account journey (scripts/lib/parental-journey.sh,
# T-M4-01 / FR-PLAY-007, ADR-0031) against a fake auth-local + admin-ui + BFF
# (scripts/testdata/parental_journey_fake.py), with real curl. Each mutation
# breaks one product behaviour in the fake and requires the journey to FAIL;
# further cases prove the secret handling (argv, masking), the signal/EXIT trap
# handling, the propagation bound and the cleanup.
# shellcheck disable=SC2016 # literal $ in the smoke.sh wiring grep patterns
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

command -v python3 >/dev/null || { echo "skip: python3 not found"; exit 0; }
command -v curl >/dev/null || { echo "skip: curl not found"; exit 0; }

# shellcheck disable=SC1091
source "$ROOT/scripts/lib/parental-smoke.sh"
# shellcheck disable=SC1091
source "$ROOT/scripts/lib/parental-journey.sh"

work="$(mktemp -d)"
fake_pid=""
cleanup() {
  [[ -n "$fake_pid" ]] && kill "$fake_pid" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

python3 -I "$ROOT/scripts/testdata/parental_journey_fake.py" >"$work/port" 2>"$work/fake.err" &
fake_pid=$!
for _ in $(seq 1 50); do
  [[ -s "$work/port" ]] && break
  sleep 0.1
done
port="$(head -1 "$work/port")"
[[ -n "$port" ]] || fail "fake server did not start: $(cat "$work/fake.err")"
BASE="http://127.0.0.1:${port}"
ADMIN_PASS="admin-pass-123"
# Fake caches expire after 0.2 s; the bound is 1 s, polling stops after 2 s.
export SMOKE_PARENTAL_JOURNEY_TIMEOUT_SEC=2 SMOKE_PARENTAL_JOURNEY_BOUND_SEC=1 SMOKE_PARENTAL_JOURNEY_POLL_SEC=0.1 \
  SMOKE_PARENTAL_JOURNEY_OUTAGE_TIMEOUT_SEC=2
# Journey work dirs land here, so the tests can prove they are removed.
mkdir -p "$work/jtmp"
export TMPDIR="$work/jtmp"

# A stand-in container CLI: pause/unpause toggle the fake provider outage;
# inspect reports the paused state or $CLI_HEALTH.
cat >"$work/cli" <<SH
#!/usr/bin/env bash
[[ "\${*: -1}" == fake-userdata ]] || { echo "unknown container \${*: -1}" >&2; exit 1; }
ctl() { curl -sf -X POST --data-binary "{\"pause\": \$1}" "${BASE}/_control" >/dev/null; }
case "\$1" in
  pause)
    [[ -z "\${CLI_PAUSE_FAILS:-}" ]] || exit 1
    ctl true
    [[ -z "\${CLI_PAUSE_PARTIAL:-}" ]] || exit 1 ;;
  unpause) ctl false ;;
  inspect)
    case "\$3" in
      *Paused*) curl -sf "${BASE}/_log" | python3 -c 'import json, sys; print(str(json.load(sys.stdin)["paused"]).lower())' ;;
      *) echo "\${CLI_HEALTH:-healthy}" ;;
    esac ;;
  *) exit 2 ;;
esac
SH
chmod +x "$work/cli"
export MUXCORE_CONTAINER_CLI="$work/cli" SMOKE_PARENTAL_JOURNEY_PROVIDER_CONTAINER=fake-userdata

# curl and python3 wrappers record every argv, so secrets in argv are caught.
mkdir -p "$work/bin"
for tool in curl python3; do
  real="$(command -v "$tool")"
  printf '#!/usr/bin/env bash\nprintf "%%s\\n" "%s $*" >>"%s"\nexec "%s" "$@"\n' "$tool" "$work/argv" "$real" >"$work/bin/$tool"
  chmod +x "$work/bin/$tool"
done
: >"$work/argv"

control() { command curl -sf -X POST -H 'X-Reset: 1' --data-binary "$1" "${BASE}/_control" >/dev/null; }
fakelog() { command curl -sf "${BASE}/_log"; }
logq() { fakelog | command python3 -c "import json, sys; d = json.load(sys.stdin); print($1)"; }

login="$(parental_smoke_device_login "$BASE" admin "$ADMIN_PASS")"
token="${login%%$'\t'*}"

# journey → runs the journey against the fake (curl/python3 through the argv
# recorders when RECORD_ARGV=1); output in $work/out (stdout+stderr).
journey() {
  local path="$PATH"
  [[ "${RECORD_ARGV:-0}" == 1 ]] && path="$work/bin:$PATH"
  PATH="$path" parental_journey_run "$BASE" "$BASE" "$BASE" admin "$ADMIN_PASS" "$token" >"$work/out" 2>&1
}
mutations=0
# expect_fail NAME FLAGS_JSON PATTERN — the mutation must make the journey fail
# with PATTERN, and the cleanup must still run.
expect_fail() {
  local name="$1" flags="$2" pattern="$3"
  control "$flags"
  if journey; then
    cat "$work/out" >&2
    fail "mutation ${name}: the journey passed"
  fi
  grep -qE -- "$pattern" "$work/out" || { cat "$work/out" >&2; fail "mutation ${name}: no '${pattern}' in the failure"; }
  check_cleaned "$name"
  cat "$work/out" >>"$work/all-out"
  mutations=$((mutations + 1))
  echo "OK mutation ${name} fails the journey"
}
# check_cleaned NAME → member deleted, rating restored, provider unpaused, work dir gone.
check_cleaned() {
  local name="$1"
  if [[ "$name" != cleanup_fails && "$(logq '"smoke-kid" in d["users"]')" != False ]]; then
    fail "${name}: smoke-kid not cleaned up"
  fi
  [[ "$(logq 'd["paused"]')" == False ]] || fail "${name}: userdata-local left paused"
  case "$name" in
    rating_unconfirmed|rbac_open) ;;
    *) [[ "$(logq 'd["rating"]')" == "('', '')" || "$(logq 'd["rating"]')" == "['', '']" ]] || fail "${name}: rating not restored: $(logq 'd["rating"]')" ;;
  esac
  [[ -z "$(ls -A "$work/jtmp")" ]] || fail "${name}: journey work dir not removed: $(ls -A "$work/jtmp")"
}

# ---- decision: household registry only; dev / host skip with a reason ----
(
  unset SMOKE_USERDATA_TLS SMOKE_USERDATA_URL MUXCORE_PROFILE MUXCORE_REQUIRE_TLS
  [[ "$(MUXCORE_SMOKE_REGISTRY='' parental_journey_decide)" == "skip host runner"* ]] || fail "host runner not skipped"
  [[ "$(MUXCORE_SMOKE_REGISTRY=1 SMOKE_USERDATA_TLS=0 parental_journey_decide)" == "skip dev profile"* ]] || fail "dev profile not skipped"
  [[ "$(MUXCORE_SMOKE_REGISTRY=1 SMOKE_USERDATA_TLS=1 parental_journey_decide)" == run ]] || fail "household registry not run"
  exit 0
) || exit 1
echo "OK decide: household registry runs; dev profile and host runner skip with a reason"

# ---- the happy path (argv recorded) ----
control '{}'
RECORD_ARGV=1 journey || { cat "$work/out" >&2; fail "journey failed against a correct fake"; }
cat "$work/out" >>"$work/all-out"
for want in "created member smoke-kid" "max PG, unrated off) via admin-ui at revision 1 (was 0)" \
  "phase A (R → R/operator): member deny" "unrestricted prefs.parental blob (PUT /api/userdata 200, read back) unlocks nothing" \
  "query inputs parental_rating=G&unrated=true&tags= ignored for the R/operator item" \
  "phase B (G → G/operator): member allow" "phase C (clear → unavailable): member deny" \
  "query inputs parental_rating=G&unrated=true&tags= ignored for the unavailable item" \
  "phase B2 (PG → PG/operator): member allow" "phase T (policy max G, unrated off; item PG/operator): member deny" \
  "phase K (policy max none, unrated off, fresh member session; item PG/operator): member allow" \
  "phase D (unrated → NR/operator): member deny" "phase U (policy max none, unrated on, fresh member session; item NR/operator): member allow" \
  "phase U2 (clear → unavailable): member deny" "phase K2 (PG-13 → PG-13/operator): member deny" \
  "403 parental.restricted_route (search, discover) where the admin gets 200 200" \
  "503 parental.policy_unavailable after" "member recovered with the movie still hidden" \
  "member sessions logged out (401 afterwards)" "OK parental journey: restricted member smoke-kid vs admin"; do
  grep -qF -- "$want" "$work/out" || { cat "$work/out" >&2; fail "happy path output lacks: $want"; }
done
check_cleaned happy
[[ "$(logq 'all(p["ok"] for p in d["csrf_posts"]) and len(d["csrf_posts"]) >= 12')" == True ]] || fail "admin-ui writes without the CSRF double-submit: $(logq 'd["csrf_posts"]')"
[[ "$(logq 'all(r["auth"] == 1 for r in d["kid_requests"]) and len(d["kid_requests"]) > 30')" == True ]] || fail "member requests not single-bearer"
[[ "$(logq 'd["module_calls"]')" == "[]" ]] || fail "an operator route reached a module for the member"
[[ "$(logq 'd["policy_puts"]')" == 5 ]] || fail "policy writes: $(logq 'd["policy_puts"]'), want 5 (PG, G, kids, kids+unrated, kids)"
[[ "$(logq 'd["pause_calls"]')" == "[True, False]" ]] || fail "outage did not pause then unpause: $(logq 'd["pause_calls"]')"
echo "OK happy path: 10 phases + blob/query unlock + RBAC/C-DENY control + outage + logout; CSRF on every admin-ui write, one bearer per member request, cleaned up"

# ---- S4: a TMDB fallback after a clear is evaluated, not assumed unavailable ----
control '{"tmdb_fallback": true}'
RECORD_ARGV=1 journey || { cat "$work/out" >&2; fail "journey failed with a TMDB fallback classification"; }
grep -qF "phase C (clear → R/tmdb): member deny" "$work/out" || fail "TMDB fallback not read back: $(grep 'phase C' "$work/out")"
grep -qF "phase U2 (clear → R/tmdb): member deny" "$work/out" || fail "TMDB fallback not evaluated in U2"
cat "$work/out" >>"$work/all-out"
echo "OK S4: after a clear the effective classification (R/tmdb, admin-ui readback 502) decides the expectation"

# ---- re-runnable: a leftover member (other role, policy at revision 3) and an operator rating ----
control '{"leftover_kid": true, "set_rating": "PG-13"}'
RECORD_ARGV=1 journey || { cat "$work/out" >&2; fail "re-run with a leftover member failed"; }
cat "$work/out" >>"$work/all-out"
grep -qF "reusing member smoke-kid (u-leftover); password reset, role user" "$work/out" || fail "leftover member not reused: $(cat "$work/out")"
grep -qF "at revision 4 (was 3)" "$work/out" || fail "policy not written at the current revision"
grep -qF "originally PG-13/operator" "$work/out" || fail "original rating not read"
[[ "$(logq 'd["rating"]')" == "('PG-13', 'operator')" || "$(logq 'd["rating"]')" == "['PG-13', 'operator']" ]] || fail "original operator rating not restored: $(logq 'd["rating"]')"
control '{"set_rating": ""}'
echo "OK re-run reuses the member, writes at the current revision and restores the original rating"

# ---- staleness inside the bound is polled, not failed; a conflict re-reads ----
control '{"class_ttl": 0.6, "policy_ttl": 0.6}'
journey || { cat "$work/out" >&2; fail "staleness inside the bound failed"; }
grep -q "old state seen until 0\.[0-9]*s" "$work/out" || fail "in-bound staleness not observed/reported"
control '{"conflict_once": true}'
journey || { cat "$work/out" >&2; fail "conflict retry failed"; }
grep -qF "conflicted, re-reading" "$work/out" || fail "conflict not reported"
echo "OK in-bound staleness polls; a revision conflict re-reads"

# ---- preflight unpauses a provider left paused by a crashed run ----
command curl -sf -X POST --data-binary '{"pause": true}' "${BASE}/_control" >/dev/null
journey || { cat "$work/out" >&2; fail "journey with a provider left paused failed"; }
grep -qF "preflight unpaused userdata-local fake-userdata" "$work/out" || fail "preflight did not unpause"
echo "OK preflight unpauses a userdata-local left paused"

# ---- the container lookup through registry_smoke_compose ----
(
  trap - EXIT # a subshell shows the parent's traps until it sets its own
  unset SMOKE_PARENTAL_JOURNEY_PROVIDER_CONTAINER
  # shellcheck disable=SC2329 # called by the journey library
  registry_smoke_compose() { [[ "$*" == "ps -q userdata-local" ]] && echo fake-userdata; }
  control '{}'
  journey || { cat "$work/out" >&2; fail "journey with the compose container lookup failed"; }
  grep -qF "503 parental.policy_unavailable" "$work/out" || fail "outage not run via the compose lookup"
  # shellcheck disable=SC2329 # called by the journey library
  registry_smoke_compose() { :; }
  control '{}'
  if journey; then fail "a missing userdata-local container passed"; fi
  grep -qF "userdata-local container not found" "$work/out" || fail "missing container message: $(tail -3 "$work/out")"
  exit 0
) || exit 1
echo "OK the outage finds userdata-local through registry_smoke_compose; none found is a FAIL"

# ---- mutations: each product misbehaviour must fail the journey ----
expect_fail list_leak '{"list_leak": true}' 'phase A: .*|phase A .*lists the blocked movie'
expect_fail total_leak '{"total_leak": true}' 'phase A .*total counts hidden items'
expect_fail detail_open '{"detail_open": true}' 'phase A .*GET /api/movies/mv_550.*HTTP 200, want 403 parental.blocked'
expect_fail detail_code '{"detail_code": "parental.policy_unconfigured"}' 'phase A .*code=parental.policy_unconfigured'
expect_fail title_leak '{"title_leak": true}' 'phase A: .*leaks the title "Fight Club"'
expect_fail no_store_missing '{"no_store_missing": true}' 'phase A: .*without Cache-Control: no-store'
expect_fail F5_missing_code '{"missing_code": true}' 'phase A .*403 code=<none> parental_code=parental.blocked'
expect_fail resolve_open '{"resolve_open": true}' 'phase A .*resolve: HTTP 200, want 403 playback.parental_blocked'
expect_fail resolve_no_alias '{"resolve_no_alias": true}' 'phase A .*resolve: 403 code=parental.blocked'
expect_fail stream_open '{"stream_open": true}' 'phase A .*Range /stream/movies/.*HTTP 206, want 403'
expect_fail admin_denied '{"admin_stream_denied": true}' 'phase A .*admin: Range .*HTTP 403, want 200/206'
expect_fail L5_list_garbage '{"list_garbage": true}' 'phase A: member GET /api/movies: HTTP 200 but not an \{items,total\} document'
expect_fail F3_userdata_put_rejected '{"userdata_put_400": true}' 'member PUT /api/userdata: HTTP 400, want 200'
expect_fail F3_userdata_not_saved '{"userdata_drop": true}' 'does not read back the prefs.parental it wrote'
expect_fail blob_authority '{"blob_authority": true}' 'after the member wrote and read back an unrestricted prefs.parental blob'
expect_fail F1_trust_query '{"trust_query": true}' 'GET /api/movies\?parental_rating=G.* lists the blocked movie'
expect_fail allowed_denied '{"allowed_stream_denied": true}' 'phase B .*member: Range .*HTTP 403, want 200/206'
expect_fail effective_wrong '{"effective_wrong": true}' 'after rating R, the effective classification is G/operator, want R/operator'
SMOKE_PARENTAL_JOURNEY_TIMEOUT_SEC=4 expect_fail L11_class_stale_beyond_bound '{"class_stale_for": 2.5}' 'phase [A-Z0-9]+: the member still saw the old state [12]\.[0-9]+s after the admin write \(bound 1\.00s'
expect_fail class_never '{"class_ttl": 30}' 'phase [A-Z0-9]+ \(.*not reached within 2\.00s'
expect_fail unavailable_visible '{"unavailable_visible": true}' 'phase C .*lists the blocked movie'
SMOKE_PARENTAL_JOURNEY_TIMEOUT_SEC=4 expect_fail policy_stale_beyond_bound '{"policy_stale_for": 2.5}' 'phase [A-Z0-9]+: the member still saw the old state [12]\.[0-9]+s after the admin write \(bound 1\.00s'
expect_fail kids_no_default '{"kids_no_default": true}' 'phase K2 .*lists the blocked movie'
expect_fail nr_allowed '{"nr_allowed": true}' 'phase D .*lists the blocked movie'
expect_fail F4_unavailable_as_unrated '{"unavailable_as_unrated": true}' 'phase U2 .*lists the blocked movie'
expect_fail list_503 '{"list_503": true}' 'GET /api/movies: HTTP 503'
expect_fail rbac_open '{"rbac_open": true}' 'DELETE /api/movies/.*HTTP 200, want 403 operator.forbidden'
expect_fail rbac_code '{"rbac_code": "parental.blocked"}' 'DELETE /api/movies/.*code=parental.blocked'
expect_fail cdeny_open '{"cdeny_open": true}' 'GET /api/search: HTTP 200, want 403 parental.restricted_route'
expect_fail cdeny_code '{"cdeny_code": "operator.forbidden"}' 'GET /api/search: 403 code=operator.forbidden'
expect_fail cdeny_route_missing '{"cdeny_route_missing": true}' 'admin \(unrestricted\) GET /api/discover/movie/550: HTTP 404'
expect_fail policy_not_saved '{"policy_not_saved": true}' 'read-back of .* is not restricted'
expect_fail policy_wrong '{"store_allow_unrated": true}' 'read-back of .* is not restricted'
expect_fail rating_unconfirmed '{"rating_unconfirmed": true}' 'content-rating R for .*HTTP 502'
expect_fail admin_ui_down '{"admin_down": true}' 'admin-ui .*/health HTTP 503: a household run needs admin-ui'
expect_fail outage_open '{"outage_open": true}' 'outage: not failed closed within 2\.00s; last: member: GET /api/movies during the outage still HTTP 200'
SMOKE_PARENTAL_JOURNEY_OUTAGE_TIMEOUT_SEC=4 expect_fail outage_cache_beyond_bound '{"outage_policy_ttl": 2.5}' 'outage: the member was still served from the cached policy [12]\.[0-9]+s after the pause'
expect_fail outage_stream_open '{"outage_stream_open": true}' 'Range stream during the outage: HTTP 206 \(fail-open\)'
CLI_HEALTH=unhealthy expect_fail stays_unhealthy '{}' 'not healthy again after the outage \(unhealthy\)'
CLI_PAUSE_FAILS=1 expect_fail pause_fails '{}' 'pause userdata-local'
CLI_PAUSE_PARTIAL=1 expect_fail L6_pause_partial '{}' 'pause userdata-local'
expect_fail logout_noop '{"logout_noop": true}' 'member session still answers HTTP 200 after POST /logout'
expect_fail cleanup_fails '{"delete_user_fails": true}' 'cleanup .* did not complete'
control '{}'
journey || { cat "$work/out" >&2; fail "recovery run failed"; }

# ---- secrets: never in argv, masked in every message ----
RECORD_ARGV=1 expect_fail L10_echo_bearer '{"detail_open": true, "echo_bearer": true}' 'phase A .*served to Bearer \*\*\*'
RECORD_ARGV=1 expect_fail L12_login_echo '{"tv_login_echo": true}' 'BFF /api/tv/login as smoke-kid: HTTP 401 — .*rejected password \*\*\*'
fakelog | command python3 -c 'import json, sys
d = json.load(sys.stdin)
for s in d["passwords"] + d["secrets"]:
    print(s)' >"$work/secrets"
echo "$ADMIN_PASS" >>"$work/secrets"
[[ "$(wc -l <"$work/secrets")" -gt 50 ]] || fail "secret list too short"
if grep -qFf "$work/secrets" "$work/all-out"; then
  grep -oFf "$work/secrets" "$work/all-out" | head -3 >&2
  fail "a password, bearer, session, code or CSRF token reached the journey output"
fi
echo "OK no password, bearer, session, login code or CSRF token in any journey output (masking proven by L10/L12)"
[[ "$(grep -c '^curl ' "$work/argv")" -gt 300 ]] || fail "argv recorder saw too little: $(wc -l <"$work/argv")"
if grep -qFf "$work/secrets" "$work/argv"; then
  grep -oFf "$work/secrets" "$work/argv" | head -3 >&2
  fail "L4: a password, bearer, session or CSRF token appeared in a curl/python3 argv"
fi
echo "OK L4: no secret in any curl/python3 argv ($(grep -c '^curl ' "$work/argv") curl invocations recorded over the happy, S4, re-run and L10/L12 journeys)"

# ---- L3: TERM during the journey runs the full cleanup ----
control '{"class_ttl": 30}'
(
  trap - EXIT
  PATH="$work/bin:$PATH" parental_journey_run "$BASE" "$BASE" "$BASE" admin "$ADMIN_PASS" "$token"
) >"$work/out" 2>&1 &
jpid=$!
for _ in $(seq 1 100); do
  grep -q "phase A" "$work/out" && break
  sleep 0.1
done
grep -q "phase A" "$work/out" || { kill "$jpid" 2>/dev/null; fail "L3: journey did not reach phase B"; }
kill -TERM "$jpid"
rc=0
wait "$jpid" || rc=$?
[[ "$rc" == 143 ]] || fail "L3: TERM exit status ${rc}, want 143: $(tail -3 "$work/out")"
grep -q "interrupted; cleaning up" "$work/out" || fail "L3: no interrupt cleanup message"
check_cleaned L3_term
mutations=$((mutations + 1))
echo "OK L3: TERM mid-journey deletes the member, restores the rating, unpauses and removes the work dir (exit 143)"

# ---- L3b: TERM during the outage window unpauses the provider ----
control '{"outage_policy_ttl": 30}'
(
  trap - EXIT
  PATH="$work/bin:$PATH" SMOKE_PARENTAL_JOURNEY_OUTAGE_TIMEOUT_SEC=60 parental_journey_run "$BASE" "$BASE" "$BASE" admin "$ADMIN_PASS" "$token"
) >"$work/out" 2>&1 &
jpid=$!
for _ in $(seq 1 600); do
  [[ "$(logq 'd["paused"]')" == True ]] && break
  sleep 0.1
done
[[ "$(logq 'd["paused"]')" == True ]] || { kill "$jpid" 2>/dev/null; fail "L3b: the journey never paused the provider"; }
kill -TERM "$jpid"
rc=0
wait "$jpid" || rc=$?
[[ "$rc" == 143 ]] || fail "L3b: TERM exit status ${rc}, want 143"
check_cleaned L3b_term_paused
mutations=$((mutations + 1))
echo "OK L3b: TERM while userdata-local is paused unpauses it and cleans up"

# ---- L9: the caller's traps are restored and still run ----
control '{}'
(
  trap 'echo caller-exit-ran' EXIT
  trap 'echo caller-term' TERM
  PATH="$work/bin:$PATH" parental_journey_run "$BASE" "$BASE" "$BASE" admin "$ADMIN_PASS" "$token" >/dev/null 2>&1 || exit 7
  [[ "$(trap -p EXIT)" == *"caller-exit-ran"* ]] || { echo "EXIT trap not restored: $(trap -p EXIT)"; exit 8; }
  [[ "$(trap -p TERM)" == *"caller-term"* ]] || { echo "TERM trap not restored: $(trap -p TERM)"; exit 8; }
  [[ -z "$(trap -p INT)" ]] || { echo "INT trap left behind: $(trap -p INT)"; exit 8; }
) >"$work/out" 2>&1 || fail "L9: $(cat "$work/out")"
grep -qx "caller-exit-ran" "$work/out" || fail "L9: the caller's EXIT trap did not run afterwards"
mutations=$((mutations + 1))
echo "OK L9: the caller's EXIT/TERM traps are restored and run; no INT trap left"

# ---- smoke.sh wiring ----
smoke="$ROOT/smoke.sh"
grep -q 'source "\$ROOT/scripts/lib/parental-journey.sh"' "$smoke" || fail "smoke.sh does not source the journey"
acq_line="$(grep -n '==> fixture acquisition' "$smoke" | cut -d: -f1)"
neg_line="$(grep -n 'parental_smoke_expect_unconfigured "\$MEDIA_UI_URL"' "$smoke" | cut -d: -f1)"
run_line="$(grep -n 'parental_journey_run "\$MEDIA_UI_URL" "\$ADMIN_URL" "\$AUTH_HTTP"' "$smoke" | cut -d: -f1)"
[[ -n "$acq_line" && -n "$neg_line" && -n "$run_line" ]] || fail "smoke.sh journey wiring missing"
((run_line > acq_line && run_line > neg_line)) || fail "the journey must follow acquisition and the unconfigured check"
grep -q 'parental_journey_run .* || exit 1' "$smoke" || fail "a journey failure must fail the smoke"
grep -q '^echo "PASS: MVP smoke .*+ \${journey_label} +' "$smoke" || fail "PASS line lacks the journey step"
echo "OK smoke.sh runs the journey after acquisition and names it in the PASS line"
echo "OK parental-journey script tests (${mutations} mutations and fault cases fail the journey)"
