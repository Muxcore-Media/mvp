#!/usr/bin/env bash
# Offline tests for the restricted-account journey (scripts/lib/parental-journey.sh,
# T-M4-01 / FR-PLAY-007, ADR-0031) against a fake auth-local + admin-ui + BFF
# (scripts/testdata/parental_journey_fake.py), with real curl. Each mutation
# breaks one product behaviour in the fake and requires the journey to FAIL.
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
export SMOKE_PARENTAL_JOURNEY_TIMEOUT_SEC=1 SMOKE_PARENTAL_JOURNEY_POLL_SEC=0.2 SMOKE_PARENTAL_JOURNEY_OUTAGE_TIMEOUT_SEC=4
# A stand-in container CLI: `pause|unpause <id>` toggles the fake provider outage.
cat >"$work/cli" <<SH
#!/usr/bin/env bash
[[ "\${*: -1}" == fake-userdata ]] || { echo "unknown container \${*: -1}" >&2; exit 1; }
case "\$1" in
  pause) [[ -z "\${CLI_PAUSE_FAILS:-}" ]] || exit 1; v=true ;;
  unpause) v=false ;;
  inspect) echo "\${CLI_HEALTH:-healthy}"; exit 0 ;;
  *) exit 2 ;;
esac
curl -sf -X POST --data-binary "{\"pause\": \$v}" "${BASE}/_control" >/dev/null
SH
chmod +x "$work/cli"
export MUXCORE_CONTAINER_CLI="$work/cli" SMOKE_PARENTAL_JOURNEY_PROVIDER_CONTAINER=fake-userdata

control() { curl -sf -X POST -H 'X-Reset: 1' --data-binary "$1" "${BASE}/_control" >/dev/null; }
fakelog() { curl -sf "${BASE}/_log"; }
logq() { fakelog | python3 -c "import json, sys; d = json.load(sys.stdin); print($1)"; }

login="$(parental_smoke_device_login "$BASE" admin "$ADMIN_PASS")"
token="${login%%$'\t'*}"

# journey → runs the journey against the fake; output in $work/out (stdout+stderr).
journey() {
  parental_journey_run "$BASE" "$BASE" "$BASE" admin "$ADMIN_PASS" "$token" >"$work/out" 2>&1
}
# expect_fail NAME FLAGS_JSON PATTERN — the mutation must make the journey fail
# with PATTERN, and the cleanup must still run (rating cleared, member deleted).
expect_fail() {
  local name="$1" flags="$2" pattern="$3"
  control "$flags"
  if journey; then
    cat "$work/out" >&2
    fail "mutation ${name}: the journey passed"
  fi
  grep -qE -- "$pattern" "$work/out" || { cat "$work/out" >&2; fail "mutation ${name}: no '${pattern}' in the failure"; }
  if [[ "$name" != cleanup_fails && "$(logq '"smoke-kid" in d["users"]')" != False ]]; then
    fail "mutation ${name}: smoke-kid not cleaned up"
  fi
  [[ "$(logq 'd["paused"]')" == False ]] || fail "mutation ${name}: userdata-local left paused"
  if [[ "$(logq 'd["rating"]')" != "" && "$name" != rating_unconfirmed ]]; then
    fail "mutation ${name}: rating not restored to cleared"
  fi
  cat "$work/out" >>"$work/all-out"
  echo "OK mutation ${name} fails the journey"
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

# ---- the happy path ----
control '{}'
journey || { cat "$work/out" >&2; fail "journey failed against a correct fake"; }
cat "$work/out" >>"$work/all-out"
for want in "created member smoke-kid" "restricted (kids mode, max PG, unrated off) via admin-ui at revision 1 (was 0)" \
  "phase A (R): member hidden" "prefs.parental blob (PUT /api/userdata HTTP 200) and resolve query inputs unlock nothing" \
  "phase B (G): member listed" "phase B2 (PG): member listed" "max G, unrated off) via admin-ui at revision 2 (was 1)" \
  "phase T (policy max G, item PG): the running BFF denies the member" "phase C (clear): member hidden" "phase D (unrated): member hidden" \
  "403 operator.forbidden (DELETE movie, grab, session stop)" "503 parental.policy_unavailable after" \
  "OK parental journey: restricted member smoke-kid vs admin"; do
  grep -qF -- "$want" "$work/out" || { cat "$work/out" >&2; fail "happy path output lacks: $want"; }
done
[[ "$(logq '"smoke-kid" in d["users"]')" == False ]] || fail "smoke-kid not deleted"
[[ "$(logq 'd["rating"]')" == "" ]] || fail "rating not restored to cleared"
[[ "$(logq 'all(p["ok"] for p in d["csrf_posts"]) and len(d["csrf_posts"]) >= 6')" == True ]] || fail "admin-ui writes without the CSRF double-submit: $(logq 'd["csrf_posts"]')"
[[ "$(logq 'all(r["auth"] == 1 for r in d["kid_requests"]) and len(d["kid_requests"]) > 10')" == True ]] || fail "member requests not single-bearer"
[[ "$(logq 'd["module_calls"]')" == "[]" ]] || fail "an operator route reached a module for the member"
[[ "$(logq 'd["policy_puts"]')" == 2 ]] || fail "policy not written exactly twice (PG, then the stricter G)"
[[ "$(logq 'd["pause_calls"]')" == "[True, False]" && "$(logq 'd["paused"]')" == False ]] || fail "outage did not pause then unpause: $(logq 'd.get("pause_calls")')"
echo "OK happy path: phases A-D, blob, stricter policy, RBAC, outage; CSRF on every admin-ui write, one bearer per member request, cleaned up"

# ---- re-runnable: a leftover member (other role, a policy at revision 3) is reused ----
control '{"leftover_kid": true, "set_rating": "R"}'
journey || { cat "$work/out" >&2; fail "re-run with a leftover member failed"; }
cat "$work/out" >>"$work/all-out"
grep -qF "reusing member smoke-kid (u-leftover); password reset, role user" "$work/out" || fail "leftover member not reused: $(cat "$work/out")"
grep -qF "at revision 4 (was 3)" "$work/out" || fail "policy not written at the current revision: $(cat "$work/out")"
[[ "$(logq '"smoke-kid" in d["users"]')" == False ]] || fail "reused smoke-kid not deleted"
echo "OK re-run reuses the member, writes at the current revision and cleans up"

# ---- a stale C-PLAY cache within the deadline converges ----
control '{"play_delay": 0.5}'
SMOKE_PARENTAL_JOURNEY_TIMEOUT_SEC=3 journey || { cat "$work/out" >&2; fail "propagation within the deadline failed"; }
echo "OK propagation inside the deadline is polled, not failed"

# ---- a policy conflict re-reads the revision once ----
control '{"conflict_once": true}'
journey || { cat "$work/out" >&2; fail "conflict retry failed"; }
grep -qF "conflicted, re-reading" "$work/out" || fail "conflict not reported"
echo "OK a 409-style conflict re-reads the revision and saves"

# ---- mutations: each product misbehaviour must fail the journey ----
expect_fail list_leak '{"list_leak": true}' 'phase A .*lists the blocked movie'
expect_fail total_leak '{"total_leak": true}' 'phase A .*total counts hidden items'
expect_fail detail_open '{"detail_open": true}' 'phase A .*GET /api/movies/mv_550.*HTTP 200, want 403 parental.blocked'
expect_fail detail_code '{"detail_code": "parental.policy_unconfigured"}' 'phase A .*code=parental.policy_unconfigured'
expect_fail title_leak '{"title_leak": true}' 'phase A .*leaks the title "Fight Club"'
expect_fail resolve_open '{"resolve_open": true}' 'phase A .*resolve: HTTP 200, want 403 playback.parental_blocked'
expect_fail resolve_no_alias '{"resolve_no_alias": true}' 'phase A .*resolve: 403 code=parental.blocked'
expect_fail stream_open '{"stream_open": true}' 'phase A .*Range /stream/movies/.*HTTP 206, want 403'
expect_fail admin_denied '{"admin_stream_denied": true}' 'phase A .*admin: Range .*HTTP 403, want 206'
expect_fail allowed_denied '{"allowed_stream_denied": true}' 'phase B .*member: Range .*HTTP 403, want 206'
expect_fail stale_cache '{"play_delay": 30}' 'phase B .*not reached within 1s'
expect_fail unavailable_visible '{"unavailable_visible": true}' 'phase C .*lists the blocked movie'
expect_fail nr_allowed '{"nr_allowed": true}' 'phase D .*lists the blocked movie'
expect_fail list_503 '{"list_503": true}' 'GET /api/movies: HTTP 503'
expect_fail rbac_open '{"rbac_open": true}' 'DELETE /api/movies/.*HTTP 200, want 403 operator.forbidden'
expect_fail rbac_code '{"rbac_code": "parental.blocked"}' 'DELETE /api/movies/.*code=parental.blocked'
expect_fail cdeny_open '{"cdeny_open": true}' 'GET /api/search: HTTP 200, want 403 parental.restricted_route'
expect_fail cdeny_code '{"cdeny_code": "operator.forbidden"}' 'GET /api/search: 403 code=operator.forbidden'
expect_fail policy_not_saved '{"policy_not_saved": true}' 'read-back of .* is not restricted'
expect_fail policy_wrong '{"store_allow_unrated": true}' 'read-back of .* is not restricted'
expect_fail rating_unconfirmed '{"rating_unconfirmed": true}' 'content-rating R for .*HTTP 502'
expect_fail admin_ui_down '{"admin_down": true}' 'admin-ui .*/health HTTP 503: a household run needs admin-ui'
expect_fail blob_authority '{"blob_authority": true}' 'after the member wrote an unrestricted prefs.parental blob .*lists the blocked movie'
expect_fail policy_stale '{"policy_stale": true}' 'phase T .*not reached within 1s'
expect_fail outage_open '{"outage_open": true}' 'outage: not failed closed within 4s; last: member: GET /api/movies during the outage still HTTP 200'
expect_fail outage_stream_open '{"outage_stream_open": true}' 'Range stream during the outage: HTTP 206 .*fail-open'
CLI_HEALTH=unhealthy expect_fail stays_unhealthy '{}' 'not healthy again after the outage \(unhealthy\)'
CLI_PAUSE_FAILS=1 expect_fail pause_fails '{}' 'pause userdata-local'
(unset SMOKE_PARENTAL_JOURNEY_PROVIDER_CONTAINER; expect_fail no_container '{}' 'userdata-local container not found') || exit 1
expect_fail cleanup_fails '{"delete_user_fails": true}' 'cleanup .* did not complete'
control '{}'
# the delete_user_fails mutation left the member behind; a clean run removes it.
journey || { cat "$work/out" >&2; fail "recovery run failed"; }

# ---- no secret reaches the output ----
fakelog | python3 -c 'import json, sys
d = json.load(sys.stdin)
for s in d["passwords"] + d["secrets"]:
    print(s)' >"$work/secrets"
echo "$ADMIN_PASS" >>"$work/secrets"
[[ "$(wc -l <"$work/secrets")" -gt 10 ]] || fail "secret list too short"
if grep -qFf "$work/secrets" "$work/all-out"; then
  grep -oFf "$work/secrets" "$work/all-out" | head -3 >&2
  fail "a password, bearer, session or CSRF token reached the journey output"
fi
echo "OK no password, bearer, session or CSRF token in any journey output"

# ---- smoke.sh wiring ----
smoke="$ROOT/smoke.sh"
grep -q 'source "\$ROOT/scripts/lib/parental-journey.sh"' "$smoke" || fail "smoke.sh does not source the journey"
acq_line="$(grep -n '==> fixture acquisition' "$smoke" | cut -d: -f1)"
neg_line="$(grep -n 'parental_smoke_expect_unconfigured "\$MEDIA_UI_URL"' "$smoke" | cut -d: -f1)"
run_line="$(grep -n 'parental_journey_run "\$MEDIA_UI_URL" "\$ADMIN_URL" "\$AUTH_HTTP"' "$smoke" | cut -d: -f1)"
[[ -n "$acq_line" && -n "$neg_line" && -n "$run_line" ]] || fail "smoke.sh journey wiring missing"
(( run_line > acq_line && run_line > neg_line )) || fail "the journey must follow acquisition and the unconfigured check"
grep -q 'parental_journey_run .* || exit 1' "$smoke" || fail "a journey failure must fail the smoke"
grep -q '^echo "PASS: MVP smoke .*+ \${journey_label} +' "$smoke" || fail "PASS line lacks the journey step"
echo "OK smoke.sh runs the journey after acquisition and names it in the PASS line"
echo "OK parental-journey script tests"
