# shellcheck shell=bash
# Parental policy steps for smoke.sh (ADR-0030 provider, ADR-0031 BFF gate).
#
# The BFF denies playback to every account without a configured provider
# policy (403 parental.policy_unconfigured), so before any stream step the smoke
# seeds an explicit `unrestricted` policy for the accounts it streams as. It
# uses only the real provider contract: GET/PUT {userdata}/api/parental-policy
# with the admin's auth-local bearer and the target in X-MuxCore-User-Id, a
# revision-checked PUT, never a blob or a local file. An existing restricted
# policy is never overwritten. One negative step proves the gate: a fresh
# account with no policy gets 403 parental.policy_unconfigured on a stream.
#
# Bearers and passwords go to curl through stdin / process substitution, not
# argv. Every function prints "FAIL: …" and returns non-zero on error.
#
# Seed transport (ADR-0033 §4): only explicit insecure dev serves the provider
# in plaintext, so only there does the seed call it directly with curl. In
# household/staging userdata-local is mTLS-only and admits policy writes only
# from admin-ui's verified identity, so the seed runs inside admin-ui's own
# service context through admin-ui's seed helper (slice S9c), invoked via
# SMOKE_PARENTAL_SEED_CMD. The smoke never holds a module key, never uses -k or
# plaintext against a household provider, and never seeds with the BFF identity
# (the provider denies media-ui policy writes). See docs/USERDATA-CLIENTS.md.

PARENTAL_SMOKE_UNRESTRICTED='{"version":1,"mode":"unrestricted","rules":null}'

# parental_smoke_device_login AUTH_URL USER PASSWORD
# Prints "<token>\t<user_id>" from auth-local POST /login/device.
parental_smoke_device_login() {
  local auth="${1%/}" user="$2" pass="$3" resp
  resp="$(PS_USER="$user" PS_PASS="$pass" python3 -c 'import json, os
print(json.dumps({"username": os.environ["PS_USER"], "password": os.environ["PS_PASS"]}))' |
    curl -sS -X POST -H 'Content-Type: application/json' --data-binary @- \
      -w '\n%{http_code}' "${auth}/login/device")" || {
    echo "FAIL: auth-local device login for ${user} (connection)" >&2
    return 1
  }
  python3 -c 'import json, sys
user, raw = sys.argv[1], sys.stdin.read().rstrip("\n")
body, _, code = raw.rpartition("\n")
if code != "200":
    sys.exit(f"FAIL: auth-local device login for {user}: HTTP {code}")
d = json.loads(body)
if d.get("requires_2fa"):
    sys.exit(f"FAIL: {user} requires TOTP; the smoke account must not")
if not d.get("token") or not d.get("user_id"):
    sys.exit(f"FAIL: auth-local device login for {user}: no token/user_id")
print(d["token"] + "\t" + d["user_id"])' "$user" <<<"$resp"
}

# parental_smoke_policy METHOD USERDATA_URL BEARER TARGET_USER_ID [BODY]
# Calls the policy resource; prints "<http_code>\n<body>".
parental_smoke_policy() {
  local method="$1" base="${2%/}" bearer="$3" target="$4" body="${5:-}"
  local -a args=(-sS -X "$method" -w '\n%{http_code}')
  if [[ -n "$body" ]]; then
    args+=(-H 'Content-Type: application/json' --data-binary @-)
  fi
  local out
  # The header file must be a process substitution on curl's own command line
  # (one opened earlier is closed before curl reads it).
  out="$(printf '%s' "$body" | curl "${args[@]}" \
    -H @<(printf 'Authorization: Bearer %s\nX-MuxCore-User-Id: %s\nAccept: application/json\n' "$bearer" "$target") \
    "${base}/api/parental-policy")" || {
    echo "FAIL: parental policy ${method} for ${target}: provider unreachable at ${base}" >&2
    return 1
  }
  printf '%s\n%s\n' "${out##*$'\n'}" "${out%$'\n'*}"
}

# parental_smoke_seed_unrestricted USERDATA_URL ADMIN_BEARER TARGET_USER_ID
# Ensures TARGET has a configured unrestricted policy: PUT expected_revision 0
# when unconfigured, no write when already unrestricted, FAIL when restricted.
# A 409 (another writer) re-reads the current revision and retries.
parental_smoke_seed_unrestricted() {
  local base="$1" bearer="$2" target="$3" attempt resp code doc verdict put
  for attempt in 1 2 3; do
    resp="$(parental_smoke_policy GET "$base" "$bearer" "$target")" || return 1
    code="${resp%%$'\n'*}"
    doc="${resp#*$'\n'}"
    case "$code" in
      200) ;;
      404) echo "FAIL: GET ${base}/api/parental-policy for ${target}: HTTP 404 (provider needs userdata-local >= v0.1.5, ADR-0030; or the account does not exist)" >&2; return 1 ;;
      *) echo "FAIL: GET ${base}/api/parental-policy for ${target}: HTTP ${code} ${doc}" >&2; return 1 ;;
    esac
    verdict="$(python3 -c 'import json, sys
target = sys.argv[1]
d = json.loads(sys.stdin.read())
if d.get("user_id") != target:
    sys.exit("FAIL: policy document is for %r, not %r" % (d.get("user_id"), target))
state, rev, pol = d.get("state"), d.get("revision"), d.get("policy")
if state == "unconfigured" and rev == 0 and pol is None:
    print("seed")
elif state == "configured" and isinstance(rev, int) and rev > 0 and isinstance(pol, dict):
    print("ok " + str(rev) if pol.get("mode") == "unrestricted" else "restricted " + str(rev))
else:
    sys.exit("FAIL: unexpected policy document for %s: %s" % (target, json.dumps(d)))' "$target" <<<"$doc")" || return 1
    case "$verdict" in
      ok\ *)
        echo "OK parental policy ${target}: unrestricted (revision ${verdict#ok }, unchanged)"
        return 0
        ;;
      restricted\ *)
        echo "FAIL: ${target} has a restricted parental policy (revision ${verdict#restricted }); the smoke streams as this account and will not overwrite the provider's authority" >&2
        return 1
        ;;
    esac
    put="$(parental_smoke_policy PUT "$base" "$bearer" "$target" \
      "{\"expected_revision\":0,\"policy\":${PARENTAL_SMOKE_UNRESTRICTED}}")" || return 1
    code="${put%%$'\n'*}"
    case "$code" in
      200)
        python3 -c 'import json, sys
d = json.loads(sys.stdin.read())
ok = d.get("state") == "configured" and d.get("revision", 0) >= 1 and (d.get("policy") or {}).get("mode") == "unrestricted"
sys.exit(0 if ok else "FAIL: PUT did not return a configured unrestricted policy: " + json.dumps(d))' <<<"${put#*$'\n'}" || return 1
        echo "OK parental policy ${target}: set unrestricted (attempt ${attempt})"
        return 0
        ;;
      409) echo "parental policy ${target}: revision conflict, re-reading" ;;
      403) echo "FAIL: PUT parental policy for ${target}: HTTP 403 (the smoke bearer must belong to an admin in the same tenant)" >&2; return 1 ;;
      *) echo "FAIL: PUT parental policy for ${target}: HTTP ${code} ${put#*$'\n'}" >&2; return 1 ;;
    esac
  done
  echo "FAIL: parental policy for ${target}: still conflicting after ${attempt} attempts" >&2
  return 1
}

# parental_smoke_admin_request METHOD AUTH_URL ADMIN_BEARER PATH [BODY]
# Prints "<http_code>\n<body>" for an auth-local admin API call.
parental_smoke_admin_request() {
  local method="$1" auth="${2%/}" bearer="$3" path="$4" body="${5:-}" out
  local -a args=(-sS -X "$method" -w '\n%{http_code}')
  if [[ -n "$body" ]]; then
    args+=(-H 'Content-Type: application/json' --data-binary @-)
  fi
  out="$(printf '%s' "$body" | curl "${args[@]}" \
    -H @<(printf 'Authorization: Bearer %s\nAccept: application/json\n' "$bearer") "${auth}${path}")" || {
    echo "FAIL: auth-local ${method} ${path} (connection)" >&2
    return 1
  }
  printf '%s\n%s\n' "${out##*$'\n'}" "${out%$'\n'*}"
}

# parental_smoke_user_id AUTH_URL ADMIN_BEARER USERNAME → prints the id or nothing.
parental_smoke_user_id() {
  local resp
  resp="$(parental_smoke_admin_request GET "$1" "$2" /api/users)" || return 1
  [[ "${resp%%$'\n'*}" == 200 ]] || { echo "FAIL: auth-local GET /api/users HTTP ${resp%%$'\n'*}" >&2; return 1; }
  python3 -c 'import json, sys
name = sys.argv[1]
for u in json.loads(sys.stdin.read()).get("users") or []:
    if u.get("username") == name:
        print(u.get("id", ""))
        break' "$3" <<<"${resp#*$'\n'}"
}

# parental_smoke_delete_user AUTH_URL ADMIN_BEARER USERNAME (no-op when absent)
parental_smoke_delete_user() {
  local auth="$1" bearer="$2" name="$3" id resp
  id="$(parental_smoke_user_id "$auth" "$bearer" "$name")" || return 1
  [[ -n "$id" ]] || return 0
  resp="$(parental_smoke_admin_request DELETE "$auth" "$bearer" "/api/users/${id}")" || return 1
  [[ "${resp%%$'\n'*}" == 200 ]] || { echo "FAIL: delete smoke user ${name}: HTTP ${resp%%$'\n'*}" >&2; return 1; }
}

# parental_smoke_create_user AUTH_URL ADMIN_BEARER USERNAME PASSWORD
# Creates a plain `user` account, replacing a leftover from an earlier run, and
# prints its id. A fresh id has no provider policy.
parental_smoke_create_user() {
  local auth="$1" bearer="$2" name="$3" pass="$4" body resp
  parental_smoke_delete_user "$auth" "$bearer" "$name" || return 1
  body="$(PS_USER="$name" PS_PASS="$pass" python3 -c 'import json, os
print(json.dumps({"username": os.environ["PS_USER"], "password": os.environ["PS_PASS"], "role": "user"}))')"
  resp="$(parental_smoke_admin_request POST "$auth" "$bearer" /api/users "$body")" || return 1
  [[ "${resp%%$'\n'*}" == 201 || "${resp%%$'\n'*}" == 200 ]] || {
    echo "FAIL: create smoke user ${name}: HTTP ${resp%%$'\n'*} ${resp#*$'\n'}" >&2
    return 1
  }
  python3 -c 'import json, sys
print((json.loads(sys.stdin.read()).get("user") or {}).get("id", ""))' <<<"${resp#*$'\n'}"
}

# parental_smoke_expect_unconfigured MEDIA_UI_URL USERNAME PASSWORD STREAM_PATH
# Signs in through the BFF (POST /api/tv/login, which keeps the auth-local
# bearer) and requires 403 parental.policy_unconfigured on STREAM_PATH.
parental_smoke_expect_unconfigured() {
  local base="${1%/}" user="$2" pass="$3" stream="$4" resp session out code
  resp="$(PS_USER="$user" PS_PASS="$pass" python3 -c 'import json, os
print(json.dumps({"username": os.environ["PS_USER"], "password": os.environ["PS_PASS"]}))' |
    curl -sS -X POST -H 'Content-Type: application/json' --data-binary @- -w '\n%{http_code}' "${base}/api/tv/login")" || {
    echo "FAIL: BFF login for ${user} (connection)" >&2
    return 1
  }
  session="$(python3 -c 'import json, sys
body, _, code = sys.stdin.read().rstrip("\n").rpartition("\n")
if code != "200":
    sys.exit("FAIL: BFF /api/tv/login HTTP " + code)
print(json.loads(body).get("session_token") or "")' <<<"$resp")" || return 1
  [[ -n "$session" ]] || { echo "FAIL: BFF /api/tv/login returned no session for ${user}" >&2; return 1; }
  out="$(curl -sS -r 0-1023 -w '\n%{http_code}' \
    -H @<(printf 'Authorization: Bearer %s\nAccept: application/json\n' "$session") "${base}${stream}")" || {
    echo "FAIL: stream ${stream} as ${user} (connection)" >&2
    return 1
  }
  code="${out##*$'\n'}"
  if [[ "$code" != 403 ]] || ! python3 -c 'import json, sys
sys.exit(0 if json.loads(sys.stdin.read()).get("code") == "parental.policy_unconfigured" else 1)' <<<"${out%$'\n'*}" 2>/dev/null; then
    echo "FAIL: ${user} has no parental policy but ${stream} returned HTTP ${code}: ${out%$'\n'*}" >&2
    return 1
  fi
  echo "OK unconfigured account ${user} denied ${stream} (403 parental.policy_unconfigured)"
}

# parental_smoke_userdata_tls — 0 (true) when the provider is household mTLS.
# SMOKE_USERDATA_TLS=1|0 forces; an https:// / http:// SMOKE_USERDATA_URL
# decides; registry mode follows the running core's profile; the host runner is
# dev unless MUXCORE_PROFILE is household/staging or MUXCORE_REQUIRE_TLS=1.
parental_smoke_userdata_tls() {
  case "${SMOKE_USERDATA_TLS:-auto}" in
    1|true|yes) return 0 ;;
    0|false|no) return 1 ;;
  esac
  case "${SMOKE_USERDATA_URL:-}" in
    https://*) return 0 ;;
    http://*) return 1 ;;
  esac
  if [[ "${MUXCORE_SMOKE_REGISTRY:-}" == 1 ]] && declare -F registry_smoke_tls_enabled >/dev/null; then
    registry_smoke_tls_enabled
    return
  fi
  case "${MUXCORE_PROFILE:-}" in
    household|staging) return 0 ;;
  esac
  [[ "${MUXCORE_REQUIRE_TLS:-}" == 1 ]]
}

# parental_smoke_seed_via_helper ADMIN_BEARER TARGET_USER_ID
# Runs SMOKE_PARENTAL_SEED_CMD (word-split, never eval'd) with TARGET appended as
# its last argument and the bearer as the only line on stdin (never argv). The
# helper must leave TARGET with a configured unrestricted policy (seed when
# unconfigured, no write when already unrestricted) and fail otherwise; its exit
# status is the step's. Its output is printed with the bearer masked.
parental_smoke_seed_via_helper() {
  local bearer="$1" target="$2" out rc
  local -a cmd
  if [[ -z "${SMOKE_PARENTAL_SEED_CMD:-}" ]]; then
    echo "FAIL: parental policy for ${target}: household userdata-local is mTLS-only (ADR-0033); the seed must run" >&2
    echo "      inside admin-ui's service context with admin-ui's seed helper (slice S9c, not yet published)." >&2
    echo "      Set SMOKE_PARENTAL_SEED_CMD to that invocation, e.g." >&2
    echo "      SMOKE_PARENTAL_SEED_CMD='docker compose -f docker-compose.registry.yml exec -T admin-ui <helper>'" >&2
    echo "      (the bearer is passed on stdin, the target user id as the last argument)." >&2
    return 1
  fi
  read -ra cmd <<<"$SMOKE_PARENTAL_SEED_CMD"
  rc=0
  out="$(printf '%s\n' "$bearer" | "${cmd[@]}" "$target" 2>&1)" || rc=$?
  [[ -n "$bearer" ]] && out="${out//"$bearer"/***}"
  [[ -n "$out" ]] && printf '%s\n' "$out"
  if [[ "$rc" -ne 0 ]]; then
    echo "FAIL: parental policy seed helper for ${target} exited ${rc}" >&2
    return 1
  fi
  echo "OK parental policy ${target}: unrestricted via the admin-ui seed helper"
}

# parental_smoke_seed USERDATA_URL ADMIN_BEARER TARGET_USER_ID
# The smoke's seed step: plaintext provider in explicit insecure dev, otherwise
# the admin-ui seed helper (see the header).
parental_smoke_seed() {
  local base="$1" bearer="$2" target="$3"
  if parental_smoke_userdata_tls; then
    parental_smoke_seed_via_helper "$bearer" "$target"
  else
    parental_smoke_seed_unrestricted "$base" "$bearer" "$target"
  fi
}
