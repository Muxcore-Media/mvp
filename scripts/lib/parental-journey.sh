# shellcheck shell=bash
# Restricted-account end-to-end journey for smoke.sh (roadmap T-M4-01,
# FR-PLAY-007 acceptance evidence; ADR-0030 provider, ADR-0031 BFF gate,
# ADR-0033 household mTLS write path).
#
# The rest of the smoke proves an explicitly unrestricted admin streams and an
# account without a policy is denied. This journey drives a RESTRICTED member
# through the real stack against the fixture movie the acquisition step
# imported (TMDB 550 unless SMOKE_PARENTAL_JOURNEY_TMDB says otherwise):
#
#   setup  create-or-reuse the member `smoke-kid` (role user, fresh random
#          password) through auth-local's admin API, like smoke-no-policy;
#          restrict it through admin-ui's provider-backed form
#          `POST /users/{id}/parental` (admin-ui session + CSRF double-submit,
#          current revision; admin-ui writes over its mTLS identity, S9c) and
#          read it back. Policies are always kids mode and no tags.
#   phases each change goes through admin-ui (content-rating page or the policy
#          form); the effective classification is read back through the
#          admin's BFF view, the expected member outcome is computed from it
#          and the policy (ADR-0031 §2.6), and the member's BFF session is
#          compared with the admin's until both match:
#            A  R, max PG             deny; the member's own "unrestricted"
#                                     prefs.parental blob (written and read
#                                     back) and resolve/stream/list query
#                                     inputs (parental_rating=G, unrated) do
#                                     not unlock it
#            B  G                     allow (deny → allow)
#            C  cleared               effective classification decides: on an
#                                     operator-only stack "unavailable" → deny
#                                     (allow → deny); query inputs ignored
#            B2 PG                    allow (PG = ceiling)
#            T  policy max G          the running BFF denies (stricter policy)
#            K  policy kids, no max   allow PG (kids default ceiling PG)
#            D  NR, unrated off       deny (allow → deny); query inputs ignored
#            U  policy allow_unrated  allow NR
#            U2 cleared               unavailable stays denied even with
#                                     allow_unrated (allow → deny)
#            K2 PG-13, kids, no max   deny (above the kids default ceiling)
#          deny = hidden from /api/movies (and `total`), detail 403
#          parental.blocked, resolve 403 playback.parental_blocked
#          (parental_code parental.blocked), Range stream 403 parental.blocked,
#          exact `code`, Cache-Control: no-store, no title in the body;
#          allow = listed, detail 200, resolve 200, Range stream 200/206.
#          The admin (unrestricted) is listed and streams in every phase.
#          A phase fails when the member was still seen in the old state more
#          than SMOKE_PARENTAL_JOURNEY_BOUND_SEC (default 35 = ADR-0031 §4 30 s
#          cache + 5 s slack) after the admin write returned (monotonic clock);
#          SMOKE_PARENTAL_JOURNEY_TIMEOUT_SEC (default 45) only stops polling.
#   rbac   DELETE /api/movies/{id}, POST /api/releases/grab,
#          POST /api/sessions/{id}/stop → 403 operator.forbidden;
#          /api/search, /api/discover/movie/{tmdb} → 403
#          parental.restricted_route, while the admin reaches both routes.
#   outage the userdata-local container is paused: once the cached policy
#          expires (within the bound) the member's list and stream fail closed
#          with 503 parental.policy_unavailable, never the movie; unpaused,
#          healthy again, the member recovers with the movie still hidden.
#   end    the member's BFF sessions are logged out (401 afterwards).
#
# Cleanup runs on success, failure, EXIT, INT and TERM: unpause, the movie's
# ORIGINAL rating restored (operator value, or cleared), member sessions logged
# out, smoke-kid deleted, the work dir (admin-ui cookie jar) removed; the
# caller's traps are restored. A preflight unpauses a userdata-local left
# paused by a crashed earlier run. The member's provider policy record is not
# deleted (neither admin-ui nor the provider offers a delete; it is keyed by
# the deleted user id and a recreated member gets a new id).
#
# Re-runnable (the restore drill re-runs the smoke): the member is reused when
# present (password reset, role user), and every policy write uses the
# revision admin-ui currently shows.
#
# Secrets: passwords, bearers, sessions and the admin-ui CSRF token reach curl
# and python only through stdin, process substitution or the environment —
# never argv — and every message is masked before it is printed. Functions
# print "FAIL: …" and return non-zero; only the signal handler exits.

PARENTAL_JOURNEY_USER_DEFAULT="smoke-kid"

# parental_journey_decide → prints "run" or "skip <reason>".
# The journey needs the household transport: a registry stack whose
# userdata-local is mTLS-only, so the restricted policy can only be written
# through admin-ui (ADR-0033). The explicit insecure dev profile (and the host
# runner) is skipped with a reason; nothing else is a skip — in a household run
# a missing admin-ui, BFF or fixture movie is a FAIL in parental_journey_run.
parental_journey_decide() {
  if [[ "${MUXCORE_SMOKE_REGISTRY:-}" != 1 ]]; then
    echo "skip host runner (the journey runs against household registry stacks)"
    return 0
  fi
  if ! parental_smoke_userdata_tls; then
    echo "skip dev profile (plaintext userdata-local; no admin-ui mTLS policy path)"
    return 0
  fi
  echo run
}

# ---- output, secrets, clock ------------------------------------------------

_PJ_SECRETS=()

_pj_mask() {
  local s="$1" v
  for v in "${_PJ_SECRETS[@]}"; do
    [[ -n "$v" ]] && s="${s//"$v"/***}"
  done
  printf '%s' "$s"
}

_pj_fail() {
  printf 'FAIL: parental journey: %s\n' "$(_pj_mask "$*")" >&2
  return 1
}

_pj_say() {
  printf '%s\n' "$(_pj_mask "$*")"
}

# _pj_snippet FILE → first 300 bytes of a response body, one line, masked.
_pj_snippet() {
  [[ -f "$1" ]] || return 0
  _pj_mask "$(head -c 300 "$1" | tr -d '\000' | tr '\n\r' '  ')"
}

# _pj_cs VALUE → centiseconds of a decimal number of seconds ("35", "1.5").
_pj_cs() {
  local v="$1" s f
  s="${v%%.*}"
  f="0"
  [[ "$v" == *.* ]] && f="${v#*.}"
  f="${f}00"
  printf '%d' "$((10#${s:-0} * 100 + 10#${f:0:2}))"
}

# _pj_now → PJ_NOW in centiseconds from a monotonic source (/proc/uptime, which
# never jumps with the wall clock; EPOCHREALTIME where it is missing).
_pj_now() {
  local up _rest
  if [[ -r /proc/uptime ]] && read -r up _rest </proc/uptime; then
    PJ_NOW="$(_pj_cs "$up")"
  else
    PJ_NOW="$(_pj_cs "${EPOCHREALTIME:-$(date +%s)}")"
  fi
}

# _pj_secs CENTISECONDS → "12.34"
_pj_secs() {
  printf '%d.%02d' "$(($1 / 100))" "$(($1 % 100))"
}

_pj_urlencode() {
  python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$1"
}

# ---- HTTP ------------------------------------------------------------------

# _pj_bff METHOD PATH BEARER [RANGE] [JSON_BODY]
# One BFF request with exactly one bearer (process substitution, not argv).
# Sets PJ_CODE; body in "$PJ_WORK/body", headers in "$PJ_WORK/hdr".
_pj_bff() {
  local method="$1" path="$2" bearer="$3" range="${4:-}" body="${5:-}"
  local -a args=(-sS -X "$method" -o "$PJ_WORK/body" -D "$PJ_WORK/hdr" -w '%{http_code}' --max-time 30)
  [[ -n "$range" ]] && args+=(-r "$range")
  if [[ -n "$body" ]]; then
    args+=(-H 'Content-Type: application/json' --data-binary @-)
  fi
  : >"$PJ_WORK/body"
  : >"$PJ_WORK/hdr"
  PJ_CODE="$(printf '%s' "$body" | curl "${args[@]}" \
    -H @<(printf 'Authorization: Bearer %s\nAccept: application/json\n' "$bearer") \
    "${PJ_BFF}${path}" 2>"$PJ_WORK/curl.err")" || PJ_CODE="000"
}

# _pj_json_code FILE → PJ_JCODE / PJ_JPCODE: the top-level string members
# "code" and "parental_code" of a JSON error body (empty when absent). Two
# separate globals, so an absent `code` can never shift into its place.
_pj_json_code() {
  local body re_c='(^|[{,])[[:space:]]*"code"[[:space:]]*:[[:space:]]*"([^"\\]*)"'
  local re_p='(^|[{,])[[:space:]]*"parental_code"[[:space:]]*:[[:space:]]*"([^"\\]*)"'
  PJ_JCODE="" PJ_JPCODE=""
  body="$(head -c 4096 "$1" 2>/dev/null | tr -d '\000\n\r')"
  [[ "$body" =~ $re_c ]] && PJ_JCODE="${BASH_REMATCH[2]}"
  [[ "$body" =~ $re_p ]] && PJ_JPCODE="${BASH_REMATCH[2]}"
  return 0
}

# _pj_expect_403 WHAT CODE [PARENTAL_CODE] — checks the last _pj_bff answer:
# 403, `code` exactly CODE (and parental_code when given), Cache-Control:
# no-store on parental denials, no title. Sets PJ_WHY; a title leak also sets
# PJ_FATAL (a definite leak is never "not converged yet").
_pj_expect_403() {
  local what="$1" want="$2" want_pc="${3:-}"
  if [[ "$PJ_CODE" != 403 ]]; then
    PJ_WHY="${what}: HTTP ${PJ_CODE}, want 403 ${want} — $(_pj_snippet "$PJ_WORK/body")"
    return 1
  fi
  _pj_json_code "$PJ_WORK/body"
  if [[ "$PJ_JCODE" != "$want" || ( -n "$want_pc" && "$PJ_JPCODE" != "$want_pc" ) ]]; then
    PJ_WHY="${what}: 403 code=${PJ_JCODE:-<none>} parental_code=${PJ_JPCODE:-<none>}, want ${want}${want_pc:+ / ${want_pc}} — $(_pj_snippet "$PJ_WORK/body")"
    return 1
  fi
  if [[ "$want" == parental.* || "$want" == playback.parental_blocked ]] &&
    ! grep -qiE '^cache-control:.*no-store' "$PJ_WORK/hdr"; then
    PJ_WHY="${what}: 403 ${want} without Cache-Control: no-store (ADR-0031 §3)"
    PJ_FATAL=1
    return 1
  fi
  if [[ -n "$PJ_TITLE" ]] && grep -qiF -- "$PJ_TITLE" "$PJ_WORK/body"; then
    PJ_WHY="${what}: the 403 body leaks the title \"${PJ_TITLE}\" — $(_pj_snippet "$PJ_WORK/body")"
    PJ_FATAL=1
    return 1
  fi
  return 0
}

_pj_stream_ok() {
  [[ "$PJ_CODE" == 200 || "$PJ_CODE" == 206 ]]
}

# _pj_list_state BEARER [EXTRA_QUERY] → PJ_LIST "listed|hidden <total> <n>
# <page_size> <stream_url|->". A 200 that is not an {items,total} document is
# a FAIL (PJ_FATAL), never "hidden".
_pj_list_state() {
  _pj_bff GET "/api/movies?page=1&page_size=200${2:-}" "$1"
  _pj_parse_list
}

# _pj_parse_list → PJ_LIST from the last _pj_bff answer (see _pj_list_state).
_pj_parse_list() {
  [[ "$PJ_CODE" == 200 ]] || { PJ_WHY="GET /api/movies: HTTP ${PJ_CODE} — $(_pj_snippet "$PJ_WORK/body")"; return 1; }
  PJ_LIST="$(python3 -c 'import json, sys
mid = sys.argv[2]
try:
    d = json.load(open(sys.argv[1], "rb"))
    items = d["items"]
    total, size = d["total"], d.get("page_size", 0)
    assert isinstance(items, list) and isinstance(total, int) and isinstance(size, int)
    assert all(isinstance(it, dict) for it in items)
except Exception:
    print("invalid"); sys.exit(0)
hit = [it for it in items if it.get("id") == mid]
if hit:
    print("listed %d %d %d %s" % (total, len(items), size, hit[0].get("stream_url") or "-"))
else:
    print("hidden %d %d %d -" % (total, len(items), size))' "$PJ_WORK/body" "$PJ_MOVIE_ID" 2>/dev/null || echo invalid)"
  if [[ "$PJ_LIST" == invalid ]]; then
    PJ_WHY="GET /api/movies: HTTP 200 but not an {items,total} document — $(_pj_snippet "$PJ_WORK/body")"
    PJ_FATAL=1
    return 1
  fi
}

# _pj_total_consistent → on a page that is not full, total == items returned.
_pj_total_consistent() {
  local _state total n size
  read -r _state total n size _ <<<"$PJ_LIST"
  if ((size > 0 && n < size && total != n)); then
    PJ_WHY="GET /api/movies: total=${total} but ${n} item(s) returned on a non-full page (size ${size}) — total counts hidden items"
    return 1
  fi
}

# _pj_check_admin → admin (unrestricted) lists the movie and streams it.
_pj_check_admin() {
  _pj_list_state "$PJ_ADMIN_SESSION" || { PJ_WHY="admin ${PJ_WHY}"; return 1; }
  local state total _n _s url
  read -r state total _n _s url <<<"$PJ_LIST"
  [[ "$state" == listed && "$url" != - ]] || { PJ_WHY="admin: movie ${PJ_MOVIE_ID} not listed with a stream_url (${PJ_LIST})"; return 1; }
  PJ_ADMIN_TOTAL="$total"
  _pj_bff GET "$url" "$PJ_ADMIN_SESSION" 0-1023
  _pj_stream_ok || { PJ_WHY="admin: Range ${url}: HTTP ${PJ_CODE}, want 200/206 — $(_pj_snippet "$PJ_WORK/body")"; return 1; }
}

# _pj_check_kid allow|deny → the member's view of the movie.
_pj_check_kid() {
  local want="$1" state total _n _s _url src="/stream/movies/${PJ_MOVIE_ID}"
  _pj_list_state "$PJ_KID_SESSION" || { PJ_WHY="member ${PJ_WHY}"; return 1; }
  read -r state total _n _s _url <<<"$PJ_LIST"
  _pj_total_consistent || { PJ_WHY="member ${PJ_WHY}"; return 1; }
  if [[ "$want" == deny ]]; then
    [[ "$state" == hidden ]] || { PJ_WHY="member: GET /api/movies lists the blocked movie ${PJ_MOVIE_ID} (${PJ_LIST})"; return 1; }
    if [[ -n "${PJ_ADMIN_TOTAL:-}" ]] && ((total >= PJ_ADMIN_TOTAL)); then
      PJ_WHY="member: GET /api/movies total=${total} is not below the admin's ${PJ_ADMIN_TOTAL} although the movie is hidden"
      return 1
    fi
    _pj_bff GET "/api/movies/${PJ_MOVIE_ID}" "$PJ_KID_SESSION"
    _pj_expect_403 "member: GET /api/movies/${PJ_MOVIE_ID}" parental.blocked || return 1
    _pj_bff GET "/api/playback/resolve?src=${PJ_SRC_Q}" "$PJ_KID_SESSION"
    _pj_expect_403 "member: GET /api/playback/resolve" playback.parental_blocked parental.blocked || return 1
    _pj_bff GET "$src" "$PJ_KID_SESSION" 0-1023
    _pj_expect_403 "member: Range ${src}" parental.blocked || return 1
    return 0
  fi
  [[ "$state" == listed ]] || { PJ_WHY="member: GET /api/movies does not list the allowed movie ${PJ_MOVIE_ID} (${PJ_LIST})"; return 1; }
  _pj_bff GET "/api/movies/${PJ_MOVIE_ID}" "$PJ_KID_SESSION"
  if [[ "$PJ_CODE" != 200 ]] || ! python3 -c 'import json, sys
d = json.load(open(sys.argv[1], "rb"))
sys.exit(0 if isinstance(d, dict) and (d.get("id") == sys.argv[2] or (d.get("movie") or {}).get("id") == sys.argv[2]) else 1)' "$PJ_WORK/body" "$PJ_MOVIE_ID" 2>/dev/null; then
    PJ_WHY="member: GET /api/movies/${PJ_MOVIE_ID}: HTTP ${PJ_CODE}, want 200 with the item — $(_pj_snippet "$PJ_WORK/body")"
    return 1
  fi
  _pj_bff GET "/api/playback/resolve?src=${PJ_SRC_Q}" "$PJ_KID_SESSION"
  if [[ "$PJ_CODE" != 200 ]] || ! grep -q '"stream_url"' "$PJ_WORK/body"; then
    PJ_WHY="member: GET /api/playback/resolve: HTTP ${PJ_CODE}, want 200 with stream_url — $(_pj_snippet "$PJ_WORK/body")"
    return 1
  fi
  _pj_bff GET "$src" "$PJ_KID_SESSION" 0-1023
  _pj_stream_ok || { PJ_WHY="member: Range ${src}: HTTP ${PJ_CODE}, want 200/206 — $(_pj_snippet "$PJ_WORK/body")"; return 1; }
}

# _pj_bff_login USER PASSWORD → PJ_LOGIN_SESSION (BFF POST /api/tv/login, which
# keeps the auth-local bearer; credentials on stdin).
_pj_bff_login() {
  local user="$1" code
  PJ_LOGIN_SESSION=""
  code="$(PS_USER="$user" PS_PASS="$2" python3 -c 'import json, os
print(json.dumps({"username": os.environ["PS_USER"], "password": os.environ["PS_PASS"]}))' |
    curl -sS -X POST -H 'Content-Type: application/json' --data-binary @- --max-time 30 \
      -o "$PJ_WORK/login" -w '%{http_code}' "${PJ_BFF}/api/tv/login" 2>"$PJ_WORK/curl.err")" || code=000
  if [[ "$code" != 200 ]]; then
    _pj_fail "BFF /api/tv/login as ${user}: HTTP ${code} — $(_pj_snippet "$PJ_WORK/login")"
    rm -f "$PJ_WORK/login"
    return 1
  fi
  PJ_LOGIN_SESSION="$(python3 -c 'import json, sys
print(json.load(open(sys.argv[1], "rb")).get("session_token") or "")' "$PJ_WORK/login" 2>/dev/null)"
  rm -f "$PJ_WORK/login"
  [[ -n "$PJ_LOGIN_SESSION" ]] || { _pj_fail "BFF /api/tv/login as ${user}: no session_token"; return 1; }
  _PJ_SECRETS+=("$PJ_LOGIN_SESSION")
}

# _pj_kid_login → a fresh member BFF session (its own empty policy cache).
_pj_kid_login() {
  _pj_bff_login "$PJ_USER" "$PJ_KID_PASS" || return 1
  PJ_KID_SESSION="$PJ_LOGIN_SESSION"
  PJ_KID_SESSIONS+=("$PJ_KID_SESSION")
}

# _pj_logout BEARER → BFF POST /logout (bearer clients are CSRF-exempt).
_pj_logout() {
  curl -sS -o /dev/null -X POST --max-time 15 \
    -H @<(printf 'Authorization: Bearer %s\nAccept: application/json\n' "$1") \
    "${PJ_BFF}/logout" 2>/dev/null || true
}

# ---- admin-ui session ------------------------------------------------------

# _pj_jar_cookie URL NAME... → value of the last cookie named NAME (any of
# them) that the jar holds for URL's host.
_pj_jar_cookie() {
  python3 -c 'import sys, urllib.parse
host, names = urllib.parse.urlsplit(sys.argv[2]).hostname or "", set(sys.argv[3:])
val = ""
for line in open(sys.argv[1], encoding="utf-8", errors="replace"):
    line = line.rstrip("\r\n")
    if line.startswith("#HttpOnly_"):
        line = line[len("#HttpOnly_"):]
    elif not line or line.startswith("#"):
        continue
    f = line.split("\t")
    if len(f) >= 7 and f[5] in names and f[0].lstrip(".") == host:
        val = f[6]
print(val)' "$PJ_JAR" "$@"
}

# _pj_admin METHOD PATH [FORM_FIELD=VALUE ...] → admin-ui request with the
# session cookie and, for writes, the double-submit CSRF header (cookie
# csrf-token == X-CSRF-Token, admin-ui main.go). Sets PJ_CODE; body in
# "$PJ_WORK/admin.html". Form values are never secret.
_pj_admin() {
  local method="$1" path="$2" f
  shift 2
  local -a args=(-sS -X "$method" -b "$PJ_JAR" -c "$PJ_JAR" -o "$PJ_WORK/admin.html" -w '%{http_code}' --max-time 30)
  : >"$PJ_WORK/admin.html"
  if [[ "$method" == GET ]]; then
    PJ_CODE="$(curl "${args[@]}" "${PJ_ADMIN_UI}${path}" 2>"$PJ_WORK/curl.err")" || PJ_CODE=000
    return 0
  fi
  if [[ -z "${PJ_CSRF:-}" ]]; then
    PJ_CODE=000
    printf 'no admin-ui csrf-token cookie for %s' "$PJ_ADMIN_UI" >"$PJ_WORK/admin.html"
    return 0
  fi
  args+=(-H 'Content-Type: application/x-www-form-urlencoded')
  for f in "$@"; do
    args+=(--data-urlencode "$f")
  done
  [[ $# -gt 0 ]] || args+=(--data-binary '')
  PJ_CODE="$(curl "${args[@]}" -H @<(printf 'X-CSRF-Token: %s\n' "$PJ_CSRF") \
    "${PJ_ADMIN_UI}${path}" 2>"$PJ_WORK/curl.err")" || PJ_CODE=000
}

# _pj_admin_login USER PASSWORD → admin-ui session in $PJ_JAR through
# auth-local's password form (same flow as the smoke's admin-ui step; the
# password and the auth CSRF token go to curl through stdin / a pipe).
_pj_admin_login() {
  local user="$1" pass="$2" redir csrf code loc
  local hdr="$PJ_WORK/login.hdr"
  : >"$PJ_JAR"
  redir="${PJ_ADMIN_UI}/auth/callback"
  curl -sS -c "$PJ_JAR" -b "$PJ_JAR" -o /dev/null --max-time 30 \
    "${PJ_AUTH}/login?redirect=$(_pj_urlencode "$redir")" 2>"$PJ_WORK/curl.err" || {
    _pj_fail "auth-local ${PJ_AUTH}/login unreachable"; return 1; }
  csrf="$(_pj_jar_cookie "$PJ_AUTH" muxcore-auth-csrf csrf-token)"
  [[ -n "$csrf" ]] || { _pj_fail "no CSRF cookie from auth-local's login page"; return 1; }
  _PJ_SECRETS+=("$csrf")
  code="$(printf '%s' "$pass" | curl -sS -c "$PJ_JAR" -b "$PJ_JAR" -D "$hdr" -o /dev/null -w '%{http_code}' --max-time 30 \
    -X POST "${PJ_AUTH}/login/password" -H 'Content-Type: application/x-www-form-urlencoded' \
    --data-urlencode "username=${user}" --data-urlencode "password@-" \
    --data-urlencode "csrf_token@"<(printf '%s' "$csrf") --data-urlencode "redirect=${redir}" 2>"$PJ_WORK/curl.err")" || code=000
  loc="$(awk -F': ' 'tolower($1)=="location"{gsub(/\r/,"",$2); print $2; exit}' "$hdr")"
  rm -f "$hdr"
  [[ "$code" == 30[23] && -n "$loc" ]] || { _pj_fail "admin-ui sign-in through auth-local: HTTP ${code} (want a redirect)"; return 1; }
  _PJ_SECRETS+=("${loc##*code=}")
  # The callback URL carries the one-time login code: give it to curl on stdin.
  code="$(printf 'url = "%s"\n' "$loc" | curl -sS -K - -c "$PJ_JAR" -b "$PJ_JAR" -o /dev/null -w '%{http_code}' --max-time 30 2>"$PJ_WORK/curl.err")" || code=000
  [[ "$code" == 30[23] || "$code" == 200 ]] || { _pj_fail "admin-ui /auth/callback: HTTP ${code}"; return 1; }
  # The dashboard GET sets admin-ui's csrf-token cookie.
  _pj_admin GET /
  [[ "$PJ_CODE" == 200 ]] || { _pj_fail "admin-ui / after sign-in: HTTP ${PJ_CODE}"; return 1; }
  PJ_CSRF="$(_pj_jar_cookie "$PJ_ADMIN_UI" csrf-token)"
  [[ -n "$PJ_CSRF" ]] || { _pj_fail "admin-ui set no csrf-token cookie"; return 1; }
  _PJ_SECRETS+=("$PJ_CSRF" "$(_pj_jar_cookie "$PJ_ADMIN_UI" session)")
}

# _pj_load_parental FILE → PJ_PARENTAL (JSON summary of admin-ui's
# UserParentalForm fragment) and PJ_P[field].
declare -gA PJ_P=()
_pj_load_parental() {
  local out k v
  out="$(python3 -c 'import json, sys
from html.parser import HTMLParser
class P(HTMLParser):
    def __init__(self):
        super().__init__()
        self.d = {"state": None, "mode": None, "revision": None, "kids_mode": False, "allow_unrated": False,
                  "max_rating": "", "blocked_tags": "", "allowed_tags": "", "saved": False, "conflict": False,
                  "error": False, "load_error": False, "user": None}
        self.select = None
    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        t = a.get("data-testid")
        if t == "parental-form":
            self.d["user"] = a.get("data-parental-user")
        elif t == "parental-state":
            self.d["state"], self.d["mode"] = a.get("data-state"), a.get("data-mode")
        elif t in ("parental-saved", "parental-conflict", "parental-error", "parental-load-error"):
            self.d[t.split("-", 1)[1].replace("-", "_")] = True
        if tag == "input":
            n = a.get("name")
            if n == "expected_revision":
                self.d["revision"] = a.get("value")
            elif n in ("kids_mode", "allow_unrated"):
                self.d[n] = "checked" in a
            elif n in ("blocked_tags", "allowed_tags"):
                self.d[n] = a.get("value") or ""
        elif tag == "select":
            self.select = a.get("name")
        elif tag == "option" and self.select == "max_rating" and "selected" in a:
            self.d["max_rating"] = a.get("value") or ""
    def handle_endtag(self, tag):
        if tag == "select":
            self.select = None
p = P()
p.feed(open(sys.argv[1], encoding="utf-8", errors="replace").read())
print(json.dumps(p.d))
for k, v in p.d.items():
    print("%s\t%s" % (k, "" if v is None else (str(v).lower() if isinstance(v, bool) else v)))' "$1")"
  PJ_PARENTAL="${out%%$'\n'*}"
  PJ_P=()
  while IFS=$'\t' read -r k v; do
    [[ -n "$k" ]] && PJ_P["$k"]="$v"
  done <<<"${out#*$'\n'}"
}

_pj_pget() {
  printf '%s\n' "${PJ_P[$1]:-}"
}

# _pj_set_restricted USER_ID MAX_RATING ALLOW_UNRATED(0|1) → restricted policy
# (kids mode, MAX_RATING or none, the unrated flag, no tags) through admin-ui's
# provider-backed form at the revision the form shows. A conflict re-reads the
# revision (up to three attempts). Sets PJ_T_WRITE when the accepted POST
# returned, PJ_POL_MAX / PJ_POL_UNRATED after a matching fresh read-back.
_pj_set_restricted() {
  local uid="$1" max="$2" unrated="$3" attempt rev newrev
  local -a extra=()
  [[ "$unrated" == 1 ]] && extra=(allow_unrated=1)
  for attempt in 1 2 3; do
    _pj_admin GET "/users/${uid}/parental"
    [[ "$PJ_CODE" == 200 ]] || { _pj_fail "admin-ui GET /users/${uid}/parental: HTTP ${PJ_CODE} — $(_pj_snippet "$PJ_WORK/admin.html")"; return 1; }
    _pj_load_parental "$PJ_WORK/admin.html"
    [[ "$(_pj_pget load_error)" != true ]] || { _pj_fail "admin-ui could not read the provider policy for ${uid} — $(_pj_snippet "$PJ_WORK/admin.html")"; return 1; }
    rev="$(_pj_pget revision)"
    [[ "$rev" =~ ^[0-9]+$ ]] || { _pj_fail "admin-ui parental form for ${uid} has no expected_revision — $(_pj_snippet "$PJ_WORK/admin.html")"; return 1; }
    _pj_admin POST "/users/${uid}/parental" "expected_revision=${rev}" mode=restricted kids_mode=1 "max_rating=${max}" \
      blocked_tags= allowed_tags= "${extra[@]}"
    _pj_now
    PJ_T_WRITE="$PJ_NOW"
    [[ "$PJ_CODE" == 200 ]] || { _pj_fail "admin-ui POST /users/${uid}/parental: HTTP ${PJ_CODE} — $(_pj_snippet "$PJ_WORK/admin.html")"; return 1; }
    _pj_load_parental "$PJ_WORK/admin.html"
    if [[ "$(_pj_pget conflict)" == true ]]; then
      _pj_say "parental journey: policy revision ${rev} conflicted, re-reading"
      continue
    fi
    [[ "$(_pj_pget saved)" == true && "$(_pj_pget mode)" == restricted ]] || {
      _pj_fail "admin-ui did not save the restricted policy for ${uid}: $(_pj_mask "$PJ_PARENTAL")"; return 1; }
    newrev="$(_pj_pget revision)"
    if ! [[ "$newrev" =~ ^[0-9]+$ ]] || ((newrev <= rev)); then
      _pj_fail "admin-ui saved ${uid} at revision ${newrev:-?}, not above ${rev}"
      return 1
    fi
    # Read back through a fresh GET: the stored document, not the echo.
    _pj_admin GET "/users/${uid}/parental"
    _pj_load_parental "$PJ_WORK/admin.html"
    if [[ "$PJ_CODE" != 200 ]] || ! python3 -c 'import json, sys
d, rev, mx, un = json.loads(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4] == "1"
ok = (d["state"] == "configured" and d["mode"] == "restricted" and d["revision"] == rev and d["kids_mode"]
      and d["allow_unrated"] == un and d["max_rating"] == mx and d["blocked_tags"] == "" and d["allowed_tags"] == "")
sys.exit(0 if ok else 1)' "$PJ_PARENTAL" "$newrev" "$max" "$unrated"; then
      _pj_fail "read-back of ${uid}'s policy is not restricted/kids/max ${max:-none}/unrated ${unrated} at revision ${newrev}: HTTP ${PJ_CODE} $(_pj_mask "$PJ_PARENTAL")"
      return 1
    fi
    PJ_POL_MAX="$max" PJ_POL_UNRATED="$unrated"
    _pj_say "OK parental journey: ${PJ_USER} restricted (kids mode, max ${max:-none (kids default PG)}, unrated $([[ "$unrated" == 1 ]] && echo allowed || echo off)) via admin-ui at revision ${newrev} (was ${rev})"
    return 0
  done
  _pj_fail "policy for ${uid} still conflicting after ${attempt} attempts"
}

# _pj_effective → PJ_EFF_RATING / PJ_EFF_SOURCE: the movie's effective
# classification as the gate sees it (the admin's BFF detail view).
_pj_effective() {
  local out
  _pj_bff GET "/api/movies/${PJ_MOVIE_ID}" "$PJ_ADMIN_SESSION"
  [[ "$PJ_CODE" == 200 ]] || { _pj_fail "admin GET /api/movies/${PJ_MOVIE_ID}: HTTP ${PJ_CODE}"; return 1; }
  out="$(python3 -c 'import json, sys
d = json.load(open(sys.argv[1], "rb"))
d = d.get("movie", d) if isinstance(d, dict) else {}
r, s = d.get("content_rating"), d.get("content_rating_source")
if not isinstance(r, str) or not isinstance(s, str) or "\t" in r + s:
    sys.exit(1)
print(r + "\t" + s + "\t.")' "$PJ_WORK/body" 2>/dev/null)" || {
    _pj_fail "admin GET /api/movies/${PJ_MOVIE_ID} has no content_rating/content_rating_source strings"; return 1; }
  PJ_EFF_RATING="${out%%$'\t'*}"
  out="${out#*$'\t'}"
  PJ_EFF_SOURCE="${out%%$'\t'*}"
}

# _pj_classify RATING → rated|unrated|unavailable (ADR-0031 §2.5; the movie
# ladder; any other token is unavailable).
_pj_level() {
  case "$1" in
    G) echo 0 ;; PG) echo 1 ;; PG-13) echo 2 ;; R) echo 3 ;; NC-17) echo 4 ;; *) echo -1 ;;
  esac
}

# _pj_expected RATING → allow|deny under the current restricted policy
# (ADR-0031 §2.6: unavailable always denied; NR/UR only with allow_unrated;
# rated ≤ ceiling, where the explicit max wins and kids mode means PG).
_pj_expected() {
  local r="$1" ceiling lvl
  case "$r" in
    NR|UR) [[ "$PJ_POL_UNRATED" == 1 ]] && echo allow || echo deny; return ;;
  esac
  lvl="$(_pj_level "$r")"
  ((lvl >= 0)) || { echo deny; return; }
  ceiling="$(_pj_level "${PJ_POL_MAX:-PG}")"
  ((lvl <= ceiling)) && echo allow || echo deny
}

_pj_describe_eff() {
  if [[ -z "$PJ_EFF_RATING" ]]; then
    echo "unavailable"
  else
    echo "${PJ_EFF_RATING}/${PJ_EFF_SOURCE:-?}"
  fi
}

# _pj_rate CHOICE → R|G|… , clear or unrated through admin-ui's content-rating
# page (admin only), then the effective classification read back. Operator
# values must be confirmed ("saved and checked", recorded value) and be the
# effective classification. A clear must remove the operator value; with
# ADR-0031 S4 (media modules ≥ v0.1.24) a TMDB value may then apply, which
# admin-ui's readback can report as unconfirmed (502) — accepted only when the
# effective source is no longer `operator`. Sets PJ_T_WRITE when the POST
# returned.
_pj_rate() {
  local choice="$1" want got
  _pj_admin POST "/media/media-movies/item/${PJ_MOVIE_SEG}/content-rating" "classification=${choice}"
  _pj_now
  PJ_T_WRITE="$PJ_NOW"
  if [[ "$PJ_CODE" == 200 ]] && grep -q 'Content rating saved and checked' "$PJ_WORK/admin.html"; then
    case "$choice" in
      clear) want="" ;;
      unrated) want="NR" ;;
      *) want="$choice" ;;
    esac
    got="$(python3 -c 'import re, sys
m = re.search(r"Recorded value:\s*<strong>([^<]*)</strong>", open(sys.argv[1], encoding="utf-8", errors="replace").read())
print(m.group(1) if m else "")' "$PJ_WORK/admin.html")"
    if [[ "$choice" != clear && "$got" != "$want" ]]; then
      _pj_fail "admin-ui content-rating ${choice}: recorded value '${got}', want '${want}'"
      return 1
    fi
  elif ! [[ "$choice" == clear && "$PJ_CODE" == 502 ]] || ! grep -q 'could not be confirmed' "$PJ_WORK/admin.html"; then
    _pj_fail "admin-ui content-rating ${choice} for ${PJ_MOVIE_ID}: HTTP ${PJ_CODE} — $(_pj_snippet "$PJ_WORK/admin.html")"
    return 1
  fi
  _pj_effective || return 1
  case "$choice" in
    clear)
      if [[ "$PJ_EFF_SOURCE" == operator || ( -z "$PJ_EFF_RATING" && -n "$PJ_EFF_SOURCE" ) ]]; then
        _pj_fail "after clearing, the effective classification is $(_pj_describe_eff) (an operator value or a source without a rating)"
        return 1
      fi
      ;;
    *)
      [[ "$PJ_EFF_RATING" == "$want" && "$PJ_EFF_SOURCE" == operator ]] || {
        _pj_fail "after rating ${choice}, the effective classification is $(_pj_describe_eff), want ${want}/operator"; return 1; }
      ;;
  esac
}

# _pj_phase LABEL rate|policy VALUE WANT [UNRATED] [RELOGIN]
#   rate VALUE   → the content rating (R, G, clear, unrated, …)
#   policy VALUE → the policy's max rating ("" = kids default), UNRATED 0|1;
#                  RELOGIN 1 signs the member in again after the write (a
#                  fresh session has no cached policy)
# WANT allow|deny|auto. The expected member outcome is computed from the
# effective classification and the policy; a WANT other than auto must agree
# (so a phase cannot pass on the wrong state). Then both views are polled until
# they match (deadline PJ_TIMEOUT); the phase FAILS when the member was still
# seen in the old state more than PJ_BOUND after the write returned.
_pj_phase() {
  local label="$1" what="$2" value="$3" want="$4" unrated="${5:-0}" relogin="${6:-0}" expect first="" stale="" t_iter t_ok
  if [[ "$what" == policy ]]; then
    _pj_set_restricted "$PJ_KID_ID" "$value" "$unrated" || return 1
    _pj_effective || return 1
    if [[ "$relogin" == 1 ]]; then
      _pj_kid_login || return 1
    fi
  else
    _pj_rate "$value" || return 1
  fi
  expect="$(_pj_expected "$PJ_EFF_RATING")"
  if [[ "$want" != auto && "$want" != "$expect" ]]; then
    _pj_fail "phase ${label}: the effective classification $(_pj_describe_eff) under max ${PJ_POL_MAX:-none}/unrated ${PJ_POL_UNRATED} means ${expect}, but the phase expects ${want}"
    return 1
  fi
  PJ_FATAL=0
  while :; do
    PJ_WHY=""
    _pj_now
    t_iter="$PJ_NOW"
    if _pj_check_admin && _pj_check_kid "$expect"; then
      break
    fi
    if [[ "$PJ_FATAL" == 1 ]]; then
      _pj_fail "phase ${label}: ${PJ_WHY}"
      return 1
    fi
    [[ -n "$first" ]] || first="$PJ_WHY"
    stale="$t_iter"
    if ((t_iter - PJ_T_WRITE >= PJ_TIMEOUT_CS)); then
      _pj_fail "phase ${label} (${what} ${value:-none}, $(_pj_describe_eff), member ${expect}): not reached within $(_pj_secs "$PJ_TIMEOUT_CS")s; last: ${PJ_WHY}"
      return 1
    fi
    sleep "$PJ_POLL"
  done
  _pj_now
  t_ok="$PJ_NOW"
  if [[ -n "$stale" ]] && ((stale - PJ_T_WRITE > PJ_BOUND_CS)); then
    _pj_fail "phase ${label}: the member still saw the old state $(_pj_secs "$((stale - PJ_T_WRITE))")s after the admin write (bound $(_pj_secs "$PJ_BOUND_CS")s = ADR-0031 §4 30 s + slack): ${first}"
    return 1
  fi
  local took
  took="≤$(_pj_secs "$((t_ok - PJ_T_WRITE))")s after the write"
  [[ -z "$stale" ]] || took+="; old state seen until $(_pj_secs "$((stale - PJ_T_WRITE))")s: ${first%% — *}"
  if [[ "$what" == policy ]]; then
    _pj_say "OK parental journey phase ${label} (policy max ${value:-none}, unrated $([[ "$unrated" == 1 ]] && echo on || echo off)$([[ "$relogin" == 1 ]] && echo ', fresh member session'); item $(_pj_describe_eff)): member ${expect}; admin streams (${took})"
  else
    _pj_say "OK parental journey phase ${label} (${value} → $(_pj_describe_eff)): member ${expect}; admin streams (${took})"
  fi
}

# _pj_blob_unlock → the member writes its own userdata blob with an
# "unrestricted" prefs.parental (and an empty PIN); the write must succeed and
# read back unchanged (the path is live), and the member must stay denied: the
# blob is not an authority (ADR-0031 §3).
_pj_blob_unlock() {
  local blob='{"mode":"unrestricted","kids_mode":false,"max_parental_rating":"NC-17","blocked_tags":"","allowed_tags":"","allow_unrated":true,"pin_hash":"","unlocked":true}'
  _pj_bff PUT /api/userdata "$PJ_KID_SESSION" "" "{\"prefs\":{\"parental\":${blob}}}"
  [[ "$PJ_CODE" == 200 ]] || { _pj_fail "member PUT /api/userdata: HTTP ${PJ_CODE}, want 200 — $(_pj_snippet "$PJ_WORK/body")"; return 1; }
  _pj_bff GET /api/userdata "$PJ_KID_SESSION"
  if [[ "$PJ_CODE" != 200 ]] || ! python3 -c 'import json, sys
d = json.load(open(sys.argv[1], "rb"))
prefs = (d.get("prefs") if isinstance(d, dict) else None) or (d.get("blob") or {}).get("prefs") or {}
sys.exit(0 if prefs.get("parental") == json.loads(sys.argv[2]) else 1)' "$PJ_WORK/body" "$blob" 2>/dev/null; then
    _pj_fail "member GET /api/userdata (HTTP ${PJ_CODE}) does not read back the prefs.parental it wrote — $(_pj_snippet "$PJ_WORK/body")"
    return 1
  fi
  PJ_WHY=""
  _pj_check_kid deny || { _pj_fail "after the member wrote and read back an unrestricted prefs.parental blob: ${PJ_WHY}"; return 1; }
  _pj_say "OK parental journey: the member's own unrestricted prefs.parental blob (PUT /api/userdata 200, read back) unlocks nothing"
}

# _pj_query_unlock → client classification inputs (parental_rating=G, unrated,
# tags) on list, resolve and stream change nothing for a denied item.
_pj_query_unlock() {
  local q="parental_rating=G&unrated=true&tags="
  PJ_WHY=""
  _pj_list_state "$PJ_KID_SESSION" "&${q}" || { _pj_fail "member GET /api/movies?${q}: ${PJ_WHY}"; return 1; }
  [[ "$PJ_LIST" == hidden* ]] || { _pj_fail "member GET /api/movies?${q} lists the blocked movie (${PJ_LIST})"; return 1; }
  _pj_bff GET "/api/playback/resolve?src=${PJ_SRC_Q}&${q}" "$PJ_KID_SESSION"
  _pj_expect_403 "member: GET /api/playback/resolve?…&${q}" playback.parental_blocked parental.blocked || { _pj_fail "$PJ_WHY"; return 1; }
  _pj_bff GET "/stream/movies/${PJ_MOVIE_ID}?${q}" "$PJ_KID_SESSION" 0-1023
  _pj_expect_403 "member: Range /stream/movies/…?${q}" parental.blocked || { _pj_fail "$PJ_WHY"; return 1; }
  _pj_say "OK parental journey: query inputs ${q} ignored for the $(_pj_describe_eff) item (list hidden, resolve/stream 403)"
}

# ---- provider outage -------------------------------------------------------

_pj_cli() {
  local -a cli=()
  read -ra cli <<<"${MUXCORE_CONTAINER_CLI:-docker}"
  "${cli[@]}" "$@"
}

# _pj_provider_container → the userdata-local container (override:
# SMOKE_PARENTAL_JOURNEY_PROVIDER_CONTAINER), via the registry compose.
_pj_provider_container() {
  local id="${SMOKE_PARENTAL_JOURNEY_PROVIDER_CONTAINER:-}"
  if [[ -z "$id" ]] && declare -F registry_smoke_compose >/dev/null; then
    id="$(registry_smoke_compose ps -q userdata-local 2>/dev/null | head -n1)"
  fi
  printf '%s' "$id"
}

# _pj_unpause — idempotent. PJ_PAUSED is set before `pause` runs, so a pause
# that half-succeeded is still undone.
_pj_unpause() {
  [[ -n "${PJ_PAUSED:-}" ]] || return 0
  if _pj_cli unpause "$PJ_PAUSED" >/dev/null 2>&1 ||
    [[ "$(_pj_cli inspect -f '{{.State.Paused}}' "$PJ_PAUSED" 2>/dev/null)" == false ]]; then
    PJ_PAUSED=""
    return 0
  fi
  echo "FAIL: parental journey: could not unpause the userdata-local container ${PJ_PAUSED}; run: ${MUXCORE_CONTAINER_CLI:-docker} unpause ${PJ_PAUSED}" >&2
  return 1
}

# _pj_preflight → unpause a userdata-local container left paused by a crashed
# earlier run (no-op when it cannot be found: the outage step reports that).
_pj_preflight() {
  local id
  id="$(_pj_provider_container)"
  [[ -n "$id" ]] || return 0
  if [[ "$(_pj_cli inspect -f '{{.State.Paused}}' "$id" 2>/dev/null)" == true ]]; then
    _pj_cli unpause "$id" >/dev/null 2>&1 || { _pj_fail "preflight: userdata-local ${id} is paused and could not be unpaused"; return 1; }
    _pj_say "parental journey: preflight unpaused userdata-local ${id} (left paused by an earlier run)"
  fi
}

# _pj_outage → userdata-local paused: once the member's cached policy expires
# (within PJ_BOUND) its list and stream fail closed with 503
# parental.policy_unavailable — never the movie, never 200/206 after the
# bound. Always unpaused; then healthy again and the member recovers with the
# movie still hidden.
_pj_outage() {
  local id t0 t_iter cached="" rc=0 health=""
  id="$(_pj_provider_container)"
  [[ -n "$id" ]] || { _pj_fail "userdata-local container not found for the outage check (set SMOKE_PARENTAL_JOURNEY_PROVIDER_CONTAINER)"; return 1; }
  PJ_PAUSED="$id"
  if ! _pj_cli pause "$id" >/dev/null 2>"$PJ_WORK/curl.err"; then
    _pj_fail "${MUXCORE_CONTAINER_CLI:-docker} pause userdata-local: $(head -c 200 "$PJ_WORK/curl.err")"
    _pj_unpause
    return 1
  fi
  _pj_now
  t0="$PJ_NOW"
  while :; do
    _pj_now
    t_iter="$PJ_NOW"
    _pj_bff GET "/api/movies?page=1&page_size=200" "$PJ_KID_SESSION"
    if [[ "$PJ_CODE" == 503 ]]; then
      _pj_json_code "$PJ_WORK/body"
      if [[ "$PJ_JCODE" == parental.policy_unavailable ]]; then
        _pj_bff GET "/stream/movies/${PJ_MOVIE_ID}" "$PJ_KID_SESSION" 0-1023
        _pj_json_code "$PJ_WORK/body"
        [[ "$PJ_CODE" == 503 && "$PJ_JCODE" == parental.policy_unavailable ]] && break
        if _pj_stream_ok; then
          _pj_fail "member: Range stream during the outage: HTTP ${PJ_CODE} (fail-open)"
          rc=1
          break
        fi
        PJ_WHY="member: Range stream during the outage: HTTP ${PJ_CODE} code=${PJ_JCODE:-<none>}, want 503 parental.policy_unavailable"
      else
        PJ_WHY="member: GET /api/movies during the outage: 503 code=${PJ_JCODE:-<none>}, want parental.policy_unavailable"
      fi
    elif [[ "$PJ_CODE" == 200 ]]; then
      # Inside the cached-policy window: the movie must stay hidden.
      if ! _pj_parse_list || [[ "$PJ_LIST" != hidden* ]]; then
        _pj_fail "member: GET /api/movies during the outage: ${PJ_WHY:-lists the blocked movie (${PJ_LIST})}"
        rc=1
        break
      fi
      PJ_WHY="member: GET /api/movies during the outage still HTTP 200 (cached policy)"
      cached="$t_iter"
    else
      PJ_WHY="member: GET /api/movies during the outage: HTTP ${PJ_CODE}, want 503 parental.policy_unavailable — $(_pj_snippet "$PJ_WORK/body")"
    fi
    if ((t_iter - t0 >= PJ_OUTAGE_TIMEOUT_CS)); then
      _pj_fail "outage: not failed closed within $(_pj_secs "$PJ_OUTAGE_TIMEOUT_CS")s; last: ${PJ_WHY}"
      rc=1
      break
    fi
    sleep "$PJ_POLL"
  done
  _pj_now
  local took=$((PJ_NOW - t0))
  if [[ "$rc" -eq 0 && -n "$cached" ]] && ((cached - t0 > PJ_BOUND_CS)); then
    _pj_fail "outage: the member was still served from the cached policy $(_pj_secs "$((cached - t0))")s after the pause (bound $(_pj_secs "$PJ_BOUND_CS")s)"
    rc=1
  fi
  _pj_unpause || rc=1
  [[ "$rc" -eq 0 ]] || return 1
  # Healthy again (its healthcheck failed while paused); no healthcheck → none.
  until health="$(_pj_cli inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$id" 2>/dev/null)" &&
    [[ "$health" == healthy || "$health" == none ]]; do
    _pj_now
    if ((PJ_NOW - t0 >= 2 * PJ_OUTAGE_TIMEOUT_CS)); then
      _pj_fail "userdata-local container ${id} not healthy again after the outage (${health:-unknown})"
      return 1
    fi
    sleep "$PJ_POLL"
  done
  # Recovery: errors are never cached; the movie must still be hidden.
  until _pj_list_state "$PJ_KID_SESSION" && [[ "$PJ_LIST" == hidden* ]]; do
    [[ "${PJ_LIST:-}" != listed* || "$PJ_CODE" != 200 ]] || { _pj_fail "after the outage the member's list shows the blocked movie (${PJ_LIST})"; return 1; }
    _pj_now
    if ((PJ_NOW - t0 >= 2 * PJ_OUTAGE_TIMEOUT_CS)); then
      _pj_fail "after unpausing userdata-local the member did not recover; last: ${PJ_WHY}"
      return 1
    fi
    sleep "$PJ_POLL"
  done
  _pj_say "OK parental journey: userdata-local paused → member /api/movies and stream 503 parental.policy_unavailable after $(_pj_secs "$took")s (cached policy until $(_pj_secs "$(( ${cached:-$t0} - t0 ))")s), never the movie; unpaused, provider ${health}, member recovered with the movie still hidden"
}

# _pj_rbac → operator routes and C-DENY routes refuse the member before any
# module call; the admin reaches the same C-DENY routes (so a 403 is not a
# missing route); the movie survives the DELETE attempt.
_pj_rbac() {
  local discover="/api/discover/movie/${PJ_TMDB}" search
  search="/api/search?q=$(_pj_urlencode "${PJ_TITLE:-fight}")"
  _pj_bff DELETE "/api/movies/${PJ_MOVIE_ID}" "$PJ_KID_SESSION"
  _pj_expect_403 "member: DELETE /api/movies/${PJ_MOVIE_ID}" operator.forbidden || { _pj_fail "$PJ_WHY"; return 1; }
  _pj_bff POST /api/releases/grab "$PJ_KID_SESSION" "" '{}'
  _pj_expect_403 "member: POST /api/releases/grab" operator.forbidden || { _pj_fail "$PJ_WHY"; return 1; }
  _pj_bff POST /api/sessions/smoke-parental-journey/stop "$PJ_KID_SESSION" "" '{}'
  _pj_expect_403 "member: POST /api/sessions/{id}/stop" operator.forbidden || { _pj_fail "$PJ_WHY"; return 1; }
  _pj_bff GET "$search" "$PJ_KID_SESSION"
  _pj_expect_403 "member: GET /api/search" parental.restricted_route || { _pj_fail "$PJ_WHY"; return 1; }
  _pj_bff GET "$discover" "$PJ_KID_SESSION"
  _pj_expect_403 "member: GET ${discover}" parental.restricted_route || { _pj_fail "$PJ_WHY"; return 1; }
  # Positive control: the unrestricted admin reaches both routes (an upstream
  # 502/503 still proves the route exists; 401/403/404/405 do not).
  local p admin_codes=""
  for p in "$search" "$discover"; do
    _pj_bff GET "$p" "$PJ_ADMIN_SESSION"
    case "$PJ_CODE" in
      2??|502|503) admin_codes+=" ${PJ_CODE}" ;;
      *) _pj_fail "admin (unrestricted) GET ${p}: HTTP ${PJ_CODE}; the member's 403 there proves nothing — $(_pj_snippet "$PJ_WORK/body")"; return 1 ;;
    esac
  done
  _pj_check_admin || { _pj_fail "after the member's DELETE attempt: ${PJ_WHY}"; return 1; }
  _pj_say "OK parental journey: member gets 403 operator.forbidden (DELETE movie, grab, session stop) and 403 parental.restricted_route (search, discover) where the admin gets${admin_codes}; movie intact"
}

# _pj_ensure_member AUTH_BEARER → create-or-reuse $PJ_USER with $PJ_KID_PASS
# (role user) through auth-local's admin API; sets PJ_KID_ID.
_pj_ensure_member() {
  local bearer="$1" id resp body
  id="$(parental_smoke_user_id "$PJ_AUTH" "$bearer" "$PJ_USER")" || return 1
  if [[ -z "$id" ]]; then
    PJ_KID_ID="$(parental_smoke_create_user "$PJ_AUTH" "$bearer" "$PJ_USER" "$PJ_KID_PASS")" || return 1
    [[ -n "$PJ_KID_ID" ]] || { _pj_fail "auth-local created ${PJ_USER} without an id"; return 1; }
    _pj_say "parental journey: created member ${PJ_USER} (${PJ_KID_ID})"
    return 0
  fi
  body="$(PS_PASS="$PJ_KID_PASS" python3 -c 'import json, os; print(json.dumps({"password": os.environ["PS_PASS"]}))')"
  resp="$(parental_smoke_admin_request POST "$PJ_AUTH" "$bearer" "/api/users/${id}/password" "$body")" || return 1
  [[ "${resp%%$'\n'*}" == 200 ]] || { _pj_fail "reset ${PJ_USER}'s password: HTTP ${resp%%$'\n'*}"; return 1; }
  resp="$(parental_smoke_admin_request POST "$PJ_AUTH" "$bearer" "/api/users/${id}" '{"roles":["user"]}')" || return 1
  [[ "${resp%%$'\n'*}" == 200 ]] || { _pj_fail "set ${PJ_USER}'s role to user: HTTP ${resp%%$'\n'*}"; return 1; }
  PJ_KID_ID="$id"
  _pj_say "parental journey: reusing member ${PJ_USER} (${PJ_KID_ID}); password reset, role user"
}

# _pj_find_movie → the fixture movie (TMDB $PJ_TMDB, has_file) in the admin's
# BFF list; sets PJ_MOVIE_ID, PJ_TITLE and the original classification.
_pj_find_movie() {
  local found
  _pj_bff GET "/api/movies?page=1&page_size=200" "$PJ_ADMIN_SESSION"
  [[ "$PJ_CODE" == 200 ]] || { _pj_fail "admin GET /api/movies: HTTP ${PJ_CODE}"; return 1; }
  found="$(python3 -c 'import json, sys
tmdb = int(sys.argv[2])
for it in json.load(open(sys.argv[1], "rb")).get("items") or []:
    if int(it.get("tmdb_id") or 0) == tmdb and it.get("has_file") and it.get("id"):
        print("%s\t%s" % (it["id"], it.get("title") or "")); break' "$PJ_WORK/body" "$PJ_TMDB" 2>/dev/null)"
  [[ -n "$found" ]] || { _pj_fail "no fixture movie (tmdb ${PJ_TMDB}, has_file) in the admin's /api/movies — the acquisition step must import it first"; return 1; }
  PJ_MOVIE_ID="${found%%$'\t'*}"
  PJ_TITLE="${found#*$'\t'}"
  PJ_MOVIE_SEG="$(_pj_urlencode "$PJ_MOVIE_ID")"
  PJ_SRC_Q="$(_pj_urlencode "/stream/movies/${PJ_MOVIE_ID}")"
  _pj_effective || return 1
  PJ_ORIG_RATING="$PJ_EFF_RATING" PJ_ORIG_SOURCE="$PJ_EFF_SOURCE" PJ_ORIG_KNOWN=1
}

# ---- cleanup and traps -----------------------------------------------------

# _pj_cleanup → idempotent; best effort for each part, non-zero when one fails.
_pj_cleanup() {
  local rc=0 s choice
  [[ "${PJ_CLEANED:-1}" == 0 ]] || return 0
  PJ_CLEANED=1
  _pj_unpause || rc=1
  if [[ "${PJ_ADMIN_READY:-0}" == 1 && "${PJ_ORIG_KNOWN:-0}" == 1 && -d "${PJ_WORK:-}" ]]; then
    choice=clear
    if [[ "$PJ_ORIG_SOURCE" == operator ]]; then
      case "$PJ_ORIG_RATING" in
        NR|UR) choice=unrated ;;
        *) choice="$PJ_ORIG_RATING" ;;
      esac
    fi
    _pj_rate "$choice" >/dev/null || rc=1
  fi
  if [[ -d "${PJ_WORK:-}" ]]; then
    for s in "${PJ_KID_SESSIONS[@]}"; do
      _pj_logout "$s"
    done
  fi
  if [[ -n "${PJ_AUTH_BEARER:-}" ]]; then
    parental_smoke_delete_user "$PJ_AUTH" "$PJ_AUTH_BEARER" "$PJ_USER" || rc=1
  fi
  [[ -n "${PJ_WORK:-}" ]] && rm -rf "$PJ_WORK"
  return "$rc"
}

# _pj_trap_cmd SIGNAL → the command of the current trap for SIGNAL (or empty).
_pj_trap_cmd() {
  local t
  t="$(trap -p "$1")"
  [[ -n "$t" ]] || return 0
  eval "set -- $t"
  printf '%s' "$3"
}

_pj_restore_traps() {
  local sig saved
  for sig in EXIT INT TERM; do
    saved="${PJ_SAVED_TRAPS[$sig]:-}"
    if [[ -n "$saved" ]]; then
      eval "$saved"
    else
      trap - "$sig"
    fi
  done
}

# _pj_on_signal CODE — INT/TERM: clean up, restore the caller's traps, exit.
_pj_on_signal() {
  echo "FAIL: parental journey: interrupted; cleaning up" >&2
  _pj_cleanup || true
  _pj_restore_traps
  exit "$1"
}

# parental_journey_run MEDIA_UI_URL ADMIN_UI_URL AUTH_URL ADMIN_USER ADMIN_PASSWORD AUTH_ADMIN_BEARER
# Runs the whole journey (see the header) and always cleans up. AUTH_ADMIN_BEARER
# is the admin's auth-local bearer (parental_smoke_device_login), used for the
# auth-local user admin API.
# It installs EXIT/INT/TERM handlers for the cleanup and restores the
# caller's afterwards. Call it from the shell that owns its traps (smoke.sh's
# top level): inside a subshell bash reports the parent's traps until the
# subshell sets its own, and restoring them would activate them there.
declare -gA PJ_SAVED_TRAPS=()
parental_journey_run() {
  PJ_BFF="${1%/}" PJ_ADMIN_UI="${2%/}" PJ_AUTH="${3%/}"
  local admin_user="$4" admin_pass="$5" rc=0 t0=$SECONDS sig
  PJ_AUTH_BEARER="$6"
  PJ_USER="${SMOKE_PARENTAL_JOURNEY_USER:-$PARENTAL_JOURNEY_USER_DEFAULT}"
  PJ_TMDB="${SMOKE_PARENTAL_JOURNEY_TMDB:-550}"
  PJ_TIMEOUT_CS="$(_pj_cs "${SMOKE_PARENTAL_JOURNEY_TIMEOUT_SEC:-45}")"
  PJ_BOUND_CS="$(_pj_cs "${SMOKE_PARENTAL_JOURNEY_BOUND_SEC:-35}")"
  PJ_OUTAGE_TIMEOUT_CS="$(_pj_cs "${SMOKE_PARENTAL_JOURNEY_OUTAGE_TIMEOUT_SEC:-60}")"
  PJ_POLL="${SMOKE_PARENTAL_JOURNEY_POLL_SEC:-1}"
  PJ_MOVIE_ID="" PJ_TITLE="" PJ_MOVIE_SEG="" PJ_SRC_Q="" PJ_KID_ID="" PJ_ADMIN_READY=0 PJ_ADMIN_TOTAL=""
  PJ_ADMIN_SESSION="" PJ_KID_SESSION="" PJ_CODE="" PJ_WHY="" PJ_PAUSED="" PJ_CSRF=""
  PJ_ORIG_KNOWN=0 PJ_ORIG_RATING="" PJ_ORIG_SOURCE="" PJ_POL_MAX="PG" PJ_POL_UNRATED=0 PJ_FATAL=0
  PJ_KID_SESSIONS=()
  unset PJ_EFF_RATING PJ_EFF_SOURCE
  PJ_KID_PASS="$(python3 -c 'import secrets; print(secrets.token_urlsafe(24))')"
  _PJ_SECRETS=("$admin_pass" "$PJ_AUTH_BEARER" "$PJ_KID_PASS")
  PJ_WORK="$(mktemp -d)" || { _pj_fail "mktemp"; return 1; }
  PJ_JAR="$PJ_WORK/admin-ui.jar"
  : >"$PJ_JAR"
  chmod 600 "$PJ_JAR"
  PJ_CLEANED=0
  for sig in EXIT INT TERM; do
    PJ_SAVED_TRAPS[$sig]="$(trap -p "$sig")"
  done
  # shellcheck disable=SC2064 # chain the caller's EXIT handler as it is now
  trap "_pj_cleanup >/dev/null 2>&1 || true; $(_pj_trap_cmd EXIT)" EXIT
  trap '_pj_on_signal 130' INT
  trap '_pj_on_signal 143' TERM

  _pj_journey "$admin_user" "$admin_pass" || rc=1
  if ! _pj_cleanup; then
    _pj_fail "cleanup (rating restored, ${PJ_USER} deleted, provider unpaused) did not complete"
    rc=1
  fi
  _pj_restore_traps
  _PJ_SECRETS=()
  if [[ "$rc" -eq 0 ]]; then
    echo "OK parental journey: restricted member ${PJ_USER} vs admin on movie ${PJ_MOVIE_ID} (A R + blob/query, B G, C cleared, B2 PG, T max G, K kids default, D NR, U allow_unrated, U2 cleared, K2 PG-13, RBAC, outage) in $((SECONDS - t0))s; cleaned up"
  fi
  return "$rc"
}

_pj_journey() {
  local admin_user="$1" admin_pass="$2" code s
  code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 "${PJ_ADMIN_UI}/health" 2>/dev/null)" || code=000
  [[ "$code" == 200 ]] || { _pj_fail "admin-ui ${PJ_ADMIN_UI}/health HTTP ${code}: a household run needs admin-ui for the policy and rating writes"; return 1; }
  _pj_preflight || return 1

  _pj_bff_login "$admin_user" "$admin_pass" || return 1
  PJ_ADMIN_SESSION="$PJ_LOGIN_SESSION"
  _pj_find_movie || return 1
  _pj_say "parental journey: fixture movie ${PJ_MOVIE_ID} \"${PJ_TITLE}\" (tmdb ${PJ_TMDB}), originally $(_pj_describe_eff)"

  _pj_admin_login "$admin_user" "$admin_pass" || return 1
  PJ_ADMIN_READY=1
  _pj_ensure_member "$PJ_AUTH_BEARER" || return 1
  _pj_set_restricted "$PJ_KID_ID" PG 0 || return 1
  # Signed in after the policy write: no earlier answer is cached for it.
  _pj_kid_login || return 1

  _pj_phase A rate R deny || return 1
  _pj_blob_unlock || return 1
  _pj_query_unlock || return 1
  _pj_phase B rate G allow || return 1
  _pj_phase C rate clear auto || return 1
  [[ "$(_pj_expected "$PJ_EFF_RATING")" != deny ]] || _pj_query_unlock || return 1
  _pj_phase B2 rate PG allow || return 1
  # A stricter policy reaches the running BFF (same session, ≤30 s cache).
  _pj_phase T policy G deny || return 1
  # Kids mode without a max: PG is the ceiling. A fresh session per policy
  # change below, so only the classification cache is measured.
  _pj_phase K policy "" allow 0 1 || return 1
  _pj_phase D rate unrated deny || return 1
  _pj_query_unlock || return 1
  _pj_phase U policy "" allow 1 1 || return 1
  _pj_phase U2 rate clear auto || return 1
  _pj_set_restricted "$PJ_KID_ID" "" 0 || return 1
  _pj_kid_login || return 1
  _pj_phase K2 rate PG-13 deny || return 1

  _pj_rbac || return 1
  _pj_outage || return 1

  # Log the member's sessions out; the last one must be revoked.
  for s in "${PJ_KID_SESSIONS[@]}"; do
    _pj_logout "$s"
  done
  _pj_bff GET "/api/movies?page=1" "$PJ_KID_SESSION"
  [[ "$PJ_CODE" == 401 ]] || { _pj_fail "member session still answers HTTP ${PJ_CODE} after POST /logout, want 401"; return 1; }
  PJ_KID_SESSIONS=()
  _pj_say "OK parental journey: member sessions logged out (401 afterwards)"
}
