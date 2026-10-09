#!/usr/bin/env bash
# Offline tests for the smoke's parental policy steps (scripts/lib/parental-smoke.sh,
# ADR-0030/ADR-0031) against a fake auth-local + userdata-local provider + BFF
# gate (scripts/testdata/parental_fake.py), with real curl.
# shellcheck disable=SC2016 # literal $ in the smoke.sh wiring grep patterns
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

command -v python3 >/dev/null || { echo "skip: python3 not found"; exit 0; }
command -v curl >/dev/null || { echo "skip: curl not found"; exit 0; }

# shellcheck disable=SC1091
source "$ROOT/scripts/lib/parental-smoke.sh"

work="$(mktemp -d)"
fake_pid=""
cleanup() {
  [[ -n "$fake_pid" ]] && kill "$fake_pid" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

python3 -I "$ROOT/scripts/testdata/parental_fake.py" >"$work/port" 2>"$work/fake.err" &
fake_pid=$!
for _ in $(seq 1 50); do
  [[ -s "$work/port" ]] && break
  sleep 0.1
done
port="$(head -1 "$work/port")"
[[ -n "$port" ]] || fail "fake server did not start: $(cat "$work/fake.err")"
BASE="http://127.0.0.1:${port}"

control() { curl -sf -X POST --data-binary "$1" "${BASE}/_control" >/dev/null; }
fakelog() { curl -sf "${BASE}/_log"; }
puts() { fakelog | python3 -c 'import json, sys; print(sum(1 for e in json.load(sys.stdin)["log"] if e["method"] == "PUT"))'; }

# ---- admin login + first seed: unconfigured → PUT expected_revision 0 ----
login="$(parental_smoke_device_login "$BASE" admin admin-pass)"
token="${login%%$'\t'*}"
admin_id="${login#*$'\t'}"
[[ "$admin_id" == u-admin && "$token" == tok-u-admin-* ]] || fail "device login parsed as $login"
if parental_smoke_device_login "$BASE" admin wrong >/dev/null 2>"$work/err"; then fail "bad password accepted"; fi
grep -q 'HTTP 401' "$work/err" || fail "bad login message: $(cat "$work/err")"

out="$(parental_smoke_seed_unrestricted "$BASE" "$token" "$admin_id")"
grep -q "OK parental policy u-admin: set unrestricted" <<<"$out" || fail "first seed: $out"
fakelog | python3 -c 'import json, sys
log = json.load(sys.stdin)["log"]
assert [e["method"] for e in log] == ["GET", "PUT"], log
for e in log:
    assert e["query"] == "", e
    assert len(e["auth"]) == 1 and e["auth"][0].startswith("Bearer tok-u-admin-"), e
    assert e["target"] == ["u-admin"], e
body = json.loads(log[1]["body"])
assert body == {"expected_revision": 0, "policy": {"version": 1, "mode": "unrestricted", "rules": None}}, body
' || fail "seed request shape"
echo "OK seed unconfigured → PUT expected_revision 0 (one bearer, target header, no query)"

# ---- re-run is a no-op ----
out="$(parental_smoke_seed_unrestricted "$BASE" "$token" "$admin_id")"
grep -q "unrestricted (revision 1, unchanged)" <<<"$out" || fail "re-seed: $out"
[[ "$(puts)" == 1 ]] || fail "re-seed wrote again"
echo "OK re-seed is a no-op"

# ---- a concurrent writer (409) is re-read, not overwritten ----
viewer_id="$(parental_smoke_create_user "$BASE" "$token" viewer viewer-pass-123)"
[[ -n "$viewer_id" ]] || fail "create user returned no id"
control '{"conflict_once": true}'
out="$(parental_smoke_seed_unrestricted "$BASE" "$token" "$viewer_id")"
grep -q "revision conflict, re-reading" <<<"$out" || fail "409 not reported: $out"
grep -q "unrestricted (revision 1, unchanged)" <<<"$out" || fail "409 not re-read: $out"
echo "OK 409 re-reads the current revision"

# ---- a restricted policy is never overwritten ----
control "{\"restrict\": \"${viewer_id}\"}"
before="$(puts)"
if parental_smoke_seed_unrestricted "$BASE" "$token" "$viewer_id" >/dev/null 2>"$work/err"; then
  fail "restricted policy was accepted"
fi
grep -q "restricted parental policy (revision 4)" "$work/err" || fail "restricted message: $(cat "$work/err")"
[[ "$(puts)" == "$before" ]] || fail "restricted policy was overwritten"
echo "OK restricted policy left alone and the seed fails"

# ---- a non-admin bearer cannot seed another account ----
vlogin="$(parental_smoke_device_login "$BASE" viewer viewer-pass-123)"
if parental_smoke_seed_unrestricted "$BASE" "${vlogin%%$'\t'*}" "$admin_id" >/dev/null 2>"$work/err"; then
  fail "non-admin seeded another account"
fi
grep -q "HTTP 403" "$work/err" || fail "non-admin message: $(cat "$work/err")"
echo "OK non-admin bearer refused"

# ---- the BFF gate: an account without a policy is denied ----
nopol_id="$(parental_smoke_create_user "$BASE" "$token" smoke-no-policy fresh-pass-123)"
out="$(parental_smoke_expect_unconfigured "$BASE" smoke-no-policy fresh-pass-123 /stream/movies/mv1)"
grep -q "403 parental.policy_unconfigured" <<<"$out" || fail "negative check: $out"
# A leftover account from an earlier run is replaced (new id, still no policy).
again_id="$(parental_smoke_create_user "$BASE" "$token" smoke-no-policy other-pass-456)"
[[ -n "$again_id" && "$again_id" != "$nopol_id" ]] || fail "leftover user not replaced ($nopol_id → $again_id)"
parental_smoke_expect_unconfigured "$BASE" smoke-no-policy other-pass-456 /stream/movies/mv1 >/dev/null ||
  fail "replaced user should be unconfigured"
# The check must fail when the gate lets the stream through.
control '{"bff_ungated": true}'
if parental_smoke_expect_unconfigured "$BASE" smoke-no-policy other-pass-456 /stream/movies/mv1 >/dev/null 2>"$work/err"; then
  fail "negative check passed with an ungated stream"
fi
grep -q "returned HTTP 206" "$work/err" || fail "ungated message: $(cat "$work/err")"
control '{"bff_ungated": false, "bff_code": "parental.blocked"}'
if parental_smoke_expect_unconfigured "$BASE" smoke-no-policy other-pass-456 /stream/movies/mv1 >/dev/null 2>"$work/err"; then
  fail "negative check accepted a 403 with another code"
fi
control '{"bff_code": ""}'
parental_smoke_delete_user "$BASE" "$token" smoke-no-policy
[[ -z "$(parental_smoke_user_id "$BASE" "$token" smoke-no-policy)" ]] || fail "smoke user not deleted"
parental_smoke_delete_user "$BASE" "$token" smoke-no-policy || fail "deleting an absent user must be a no-op"
echo "OK unconfigured account denied; check fails open gates; user cleaned up"

# ---- provider without the route (userdata-local < v0.1.5) ----
control '{"provider_404": true}'
if parental_smoke_seed_unrestricted "$BASE" "$token" "$admin_id" >/dev/null 2>"$work/err"; then
  fail "missing provider route accepted"
fi
grep -q "v0.1.5" "$work/err" || fail "404 message: $(cat "$work/err")"
control '{"provider_404": false}'
echo "OK missing provider route fails with a version hint"

# ---- household seed transport (ADR-0033 §4): never curl, admin-ui helper only ----
log_len() { fakelog | python3 -c 'import json, sys; print(len(json.load(sys.stdin)["log"]))'; }
(
  unset SMOKE_PARENTAL_SEED_CMD SMOKE_USERDATA_URL MUXCORE_PROFILE MUXCORE_REQUIRE_TLS MUXCORE_SMOKE_REGISTRY
  export SMOKE_USERDATA_TLS=auto
  parental_smoke_userdata_tls && fail "host default must be the explicit insecure dev seed"
  SMOKE_USERDATA_URL=https://127.0.0.1:9672 parental_smoke_userdata_tls || fail "https SMOKE_USERDATA_URL not household"
  MUXCORE_PROFILE=household parental_smoke_userdata_tls || fail "MUXCORE_PROFILE=household not household"
  MUXCORE_PROFILE=staging parental_smoke_userdata_tls || fail "MUXCORE_PROFILE=staging not household"
  MUXCORE_REQUIRE_TLS=1 parental_smoke_userdata_tls || fail "MUXCORE_REQUIRE_TLS=1 not household"
  SMOKE_USERDATA_TLS=0 MUXCORE_PROFILE=household parental_smoke_userdata_tls && fail "SMOKE_USERDATA_TLS=0 not honoured"
  exit 0
) || exit 1
before="$(log_len)"
if SMOKE_USERDATA_TLS=1 SMOKE_PARENTAL_SEED_CMD='' parental_smoke_seed "$BASE" "$token" "$admin_id" >/dev/null 2>"$work/err"; then
  fail "household seed without the admin-ui helper passed"
fi
if ! grep -q "S9c" "$work/err" || ! grep -q "mTLS-only" "$work/err"; then fail "missing-helper message: $(cat "$work/err")"; fi
[[ "$(log_len)" == "$before" ]] || fail "household seed fell back to a direct provider call"
# Whitespace-only and unknown commands are refused before anything runs (the
# target user id must never become the command).
cat >"$work/u-admin" <<'SH'
#!/usr/bin/env bash
touch "$CANARY"
SH
chmod +x "$work/u-admin"
export CANARY="$work/canary"
for bad in '   ' $'\t' "$work/no-such-helper --user"; do
  if (cd "$work" && PATH="$work:$PATH" SMOKE_USERDATA_TLS=1 SMOKE_PARENTAL_SEED_CMD="$bad" parental_smoke_seed "$BASE" "$token" u-admin) >/dev/null 2>"$work/err"; then
    fail "seed helper command '${bad}' accepted"
  fi
  [[ ! -e "$CANARY" ]] || fail "target user id was executed as the command for '${bad}'"
done
grep -q "command not found: $work/no-such-helper" "$work/err" || fail "unknown helper message: $(cat "$work/err")"
[[ "$(log_len)" == "$before" ]] || fail "rejected helper reached the provider"
# A stand-in helper records argv and stdin; it echoes the bearer to prove masking.
cat >"$work/helper" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$@" >"$HELPER_ARGS"
cat >"$HELPER_STDIN"
echo "helper saw $(cat "$HELPER_STDIN")"
exit "${HELPER_RC:-0}"
SH
chmod +x "$work/helper"
export HELPER_ARGS="$work/args" HELPER_STDIN="$work/stdin"
# The documented S9c form: `… parental-seed --bearer-file - --user <id>`.
out="$(SMOKE_USERDATA_TLS=1 SMOKE_PARENTAL_SEED_CMD="$work/helper parental-seed --bearer-file - --user" parental_smoke_seed "$BASE" "$token" "$admin_id")"
[[ "$(cat "$work/args")" == $'parental-seed\n--bearer-file\n-\n--user\nu-admin' ]] || fail "helper argv: $(cat "$work/args")"
grep -qF -- "$token" "$work/args" && fail "bearer passed in argv"
[[ "$(cat "$work/stdin")" == "$token" ]] || fail "helper stdin is not the bearer"
grep -qF -- "$token" <<<"$out" && fail "bearer not masked in helper output: $out"
grep -q 'helper saw \*\*\*' <<<"$out" || fail "masked helper output: $out"
grep -q "OK parental policy u-admin: unrestricted via the admin-ui seed helper" <<<"$out" || fail "helper success: $out"
[[ "$(log_len)" == "$before" ]] || fail "household seed called the provider directly"
if HELPER_RC=3 SMOKE_USERDATA_TLS=1 SMOKE_PARENTAL_SEED_CMD="$work/helper" parental_smoke_seed "$BASE" "$token" "$admin_id" >/dev/null 2>"$work/err"; then
  fail "failing helper accepted"
fi
grep -q "helper for u-admin exited 3" "$work/err" || fail "helper failure message: $(cat "$work/err")"
# Explicit insecure dev keeps the direct plaintext seed.
out="$(SMOKE_USERDATA_TLS=0 parental_smoke_seed "$BASE" "$token" "$admin_id")"
grep -q "unrestricted (revision 1, unchanged)" <<<"$out" || fail "dev seed via dispatcher: $out"
echo "OK household seed runs only through the admin-ui helper (bearer on stdin, masked); dev seed unchanged"

# ---- smoke.sh wiring: seed before any stream, negative check after ----
smoke="$ROOT/smoke.sh"
seed_line="$(grep -n 'parental_smoke_seed "\$USERDATA_HTTP"' "$smoke" | cut -d: -f1)"
acq_line="$(grep -n '==> fixture acquisition' "$smoke" | cut -d: -f1)"
stream_line="$(grep -n 'stream_code=\$(curl' "$smoke" | cut -d: -f1)"
neg_line="$(grep -n 'parental_smoke_expect_unconfigured "\$MEDIA_UI_URL"' "$smoke" | cut -d: -f1)"
[[ -n "$seed_line" && -n "$acq_line" && -n "$stream_line" && -n "$neg_line" ]] || fail "smoke.sh parental wiring missing"
(( seed_line < acq_line && seed_line < stream_line )) || fail "smoke.sh seeds after a stream step"
(( neg_line > stream_line )) || fail "negative check must follow the admin stream"
grep -q 'source "\$ROOT/scripts/lib/parental-smoke.sh"' "$smoke" || fail "smoke.sh does not source the library"
echo "OK smoke.sh seeds before streaming and asserts the unconfigured denial"
echo "OK parental-smoke script tests"
