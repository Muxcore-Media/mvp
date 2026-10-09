# shellcheck shell=bash
# Restricted-account end-to-end journey for smoke.sh (roadmap T-M4-01,
# FR-PLAY-007 acceptance evidence; ADR-0030 provider, ADR-0031 BFF gate,
# ADR-0033 household mTLS write path).
#
# The rest of the smoke proves an explicitly unrestricted admin streams and an
# account without a policy is denied. This journey drives a RESTRICTED member
# through the real stack, against the one fixture movie the acquisition step
# imported (TMDB 550 unless SMOKE_PARENTAL_JOURNEY_TMDB says otherwise):
#
#   1. create-or-reuse the member `smoke-kid` (role user, fresh random password)
#      through auth-local's admin API, like smoke-no-policy;
#   2. set its policy RESTRICTED (kids mode, max rating PG, unrated not allowed,
#      no tags) through admin-ui's provider-backed form
#      `POST /users/{id}/parental` with the current revision — admin-ui writes
#      through its mTLS identity to userdata-local (S9c) — and read it back;
#   3. rate the movie through admin-ui's content-rating workflow and check, for
#      the member's BFF session vs the admin's, after each change (polled up to
#      SMOKE_PARENTAL_JOURNEY_TIMEOUT_SEC, default 45 s: ADR-0031 §4 caches for
#      at most 30 s):
#        A  R        member: hidden from /api/movies (and from `total`), detail
#                    403 parental.blocked, resolve 403 playback.parental_blocked,
#                    /stream/movies 403 parental.blocked, no title in any 403;
#                    admin: listed, Range stream 206
#        B  G        member: listed, detail 200, resolve 200, Range stream 206
#        C  cleared  (unavailable) member denied and hidden again
#        D  NR       (explicit unrated, allow_unrated off) member denied, hidden
#   4. operator RBAC and C-DENY spot checks for the member: DELETE
#      /api/movies/{id}, POST /api/releases/grab, POST /api/sessions/{id}/stop →
#      403 operator.forbidden; /api/search and /api/discover/… → 403
#      parental.restricted_route; the movie is still listed for the admin;
#   5. cleanup, also after a failure: rating cleared, smoke-kid deleted.
#
# Re-runnable (the restore drill re-runs the smoke): the member is reused when
# present (password reset, role user), and the policy write always uses the
# revision admin-ui currently shows.
#
# Secrets: passwords, bearers and the admin-ui CSRF token reach curl and python
# only through stdin, process substitution or the environment — never argv —
# and every message is masked before it is printed. Every function prints
# "FAIL: …" and returns non-zero on error; nothing calls `exit`.

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

# ---- internals -------------------------------------------------------------

# Values masked in every message (set by parental_journey_run).
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
  _pj_mask "$(head -c 300 "$1" | tr '\n\r' '  ')"
}

# _pj_bff METHOD PATH BEARER [RANGE] [JSON_BODY]
# One BFF request with exactly one bearer (process substitution, not argv).
# Sets PJ_CODE; the body is in "$PJ_WORK/body".
_pj_bff() {
  local method="$1" path="$2" bearer="$3" range="${4:-}" body="${5:-}"
  local -a args=(-sS -X "$method" -o "$PJ_WORK/body" -w '%{http_code}' --max-time 30)
  [[ -n "$range" ]] && args+=(-r "$range")
  if [[ -n "$body" ]]; then
    args+=(-H 'Content-Type: application/json' --data-binary @-)
  fi
  : >"$PJ_WORK/body"
  PJ_CODE="$(printf '%s' "$body" | curl "${args[@]}" \
    -H @<(printf 'Authorization: Bearer %s\nAccept: application/json\n' "$bearer") \
    "${PJ_BFF}${path}" 2>"$PJ_WORK/curl.err")" || PJ_CODE="000"
}

# _pj_json_code FILE → prints "<code>\t<parental_code>" of a JSON error body
# (string members; no python start per request).
_pj_json_code() {
  local body code="" pc="" re_c='"code"[[:space:]]*:[[:space:]]*"([^"]*)"' re_p='"parental_code"[[:space:]]*:[[:space:]]*"([^"]*)"'
  body="$(head -c 4096 "$1" 2>/dev/null | tr -d '\000')"
  [[ "$body" =~ $re_p ]] && pc="${BASH_REMATCH[1]}"
  body="${body//\"parental_code\"/\"x\"}"
  [[ "$body" =~ $re_c ]] && code="${BASH_REMATCH[1]}"
  printf '%s\t%s\n' "$code" "$pc"
}

# _pj_expect_403 WHAT CODE [PARENTAL_CODE] — checks the last _pj_bff answer.
# Sets PJ_WHY on mismatch. A 403 must not name the title.
_pj_expect_403() {
  local what="$1" want="$2" want_pc="${3:-}" got pc
  if [[ "$PJ_CODE" != 403 ]]; then
    PJ_WHY="${what}: HTTP ${PJ_CODE}, want 403 ${want} — $(_pj_snippet "$PJ_WORK/body")"
    return 1
  fi
  IFS=$'\t' read -r got pc < <(_pj_json_code "$PJ_WORK/body")
  if [[ "$got" != "$want" || ( -n "$want_pc" && "$pc" != "$want_pc" ) ]]; then
    PJ_WHY="${what}: 403 code=${got:-<none>} parental_code=${pc:-<none>}, want ${want}${want_pc:+ / ${want_pc}} — $(_pj_snippet "$PJ_WORK/body")"
    return 1
  fi
  if [[ -n "$PJ_TITLE" ]] && grep -qiF -- "$PJ_TITLE" "$PJ_WORK/body"; then
    PJ_WHY="${what}: the 403 body leaks the title \"${PJ_TITLE}\" — $(_pj_snippet "$PJ_WORK/body")"
    return 1
  fi
  return 0
}

# _pj_list_state BEARER → sets PJ_LIST ("listed <total> <n> <page_size> <stream_url>"
# or "hidden <total> <n> <page_size> -") for the journey movie, PJ_CODE on error.
_pj_list_state() {
  _pj_bff GET "/api/movies?page=1&page_size=200" "$1"
  [[ "$PJ_CODE" == 200 ]] || { PJ_WHY="GET /api/movies: HTTP ${PJ_CODE} — $(_pj_snippet "$PJ_WORK/body")"; return 1; }
  PJ_LIST="$(python3 -c 'import json, sys
mid = sys.argv[2]
try:
    d = json.load(open(sys.argv[1], "rb"))
    items = d.get("items")
    total, size = int(d.get("total") or 0), int(d.get("page_size") or 0)
except Exception:
    print("invalid"); sys.exit(0)
if not isinstance(items, list):
    print("invalid"); sys.exit(0)
hit = [it for it in items if isinstance(it, dict) and it.get("id") == mid]
if hit:
    print("listed %d %d %d %s" % (total, len(items), size, hit[0].get("stream_url") or "-"))
else:
    print("hidden %d %d %d -" % (total, len(items), size))' "$PJ_WORK/body" "$PJ_MOVIE_ID")"
  [[ "$PJ_LIST" != invalid ]] || { PJ_WHY="GET /api/movies: not a {items,total} document — $(_pj_snippet "$PJ_WORK/body")"; return 1; }
}

# _pj_total_consistent → 0 when the member's `total` counts only what it sees:
# on a page that is not full, total must equal the items returned.
_pj_total_consistent() {
  local _state total n size
  read -r _state total n size _ <<<"$PJ_LIST"
  if (( size > 0 && n < size && total != n )); then
    PJ_WHY="GET /api/movies: total=${total} but ${n} item(s) returned on a non-full page (size ${size}) — total counts hidden items"
    return 1
  fi
}

# _pj_check_admin → admin (unrestricted) lists the movie and streams it (206).
_pj_check_admin() {
  _pj_list_state "$PJ_ADMIN_SESSION" || { PJ_WHY="admin ${PJ_WHY}"; return 1; }
  local state total _n _s url
  read -r state total _n _s url <<<"$PJ_LIST"
  [[ "$state" == listed && "$url" != - ]] || { PJ_WHY="admin: movie ${PJ_MOVIE_ID} not listed with a stream_url (${PJ_LIST})"; return 1; }
  PJ_ADMIN_TOTAL="$total"
  _pj_bff GET "$url" "$PJ_ADMIN_SESSION" 0-1023
  [[ "$PJ_CODE" == 206 ]] || { PJ_WHY="admin: Range ${url}: HTTP ${PJ_CODE}, want 206 — $(_pj_snippet "$PJ_WORK/body")"; return 1; }
}

# _pj_check_kid allow|deny → the member's view of the movie in this phase.
_pj_check_kid() {
  local want="$1" state total _n _s _url src="/stream/movies/${PJ_MOVIE_ID}"
  _pj_list_state "$PJ_KID_SESSION" || { PJ_WHY="member ${PJ_WHY}"; return 1; }
  read -r state total _n _s _url <<<"$PJ_LIST"
  _pj_total_consistent || { PJ_WHY="member ${PJ_WHY}"; return 1; }
  if [[ "$want" == deny ]]; then
    [[ "$state" == hidden ]] || { PJ_WHY="member: GET /api/movies lists the blocked movie ${PJ_MOVIE_ID} (${PJ_LIST})"; return 1; }
    if [[ -n "${PJ_ADMIN_TOTAL:-}" ]] && (( total >= PJ_ADMIN_TOTAL )); then
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
  [[ "$PJ_CODE" == 206 ]] || { PJ_WHY="member: Range ${src}: HTTP ${PJ_CODE}, want 206 — $(_pj_snippet "$PJ_WORK/body")"; return 1; }
}

_pj_urlencode() {
  python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$1"
}

# _pj_bff_login USER PASSWORD → sets PJ_LOGIN_SESSION (BFF POST /api/tv/login,
# which keeps the auth-local bearer; credentials on stdin).
_pj_bff_login() {
  local user="$1" code
  PJ_LOGIN_SESSION=""
  code="$(PS_USER="$user" PS_PASS="$2" python3 -c 'import json, os
print(json.dumps({"username": os.environ["PS_USER"], "password": os.environ["PS_PASS"]}))' |
    curl -sS -X POST -H 'Content-Type: application/json' --data-binary @- --max-time 30 \
      -o "$PJ_WORK/login" -w '%{http_code}' "${PJ_BFF}/api/tv/login" 2>"$PJ_WORK/curl.err")" || code=000
  [[ "$code" == 200 ]] || { _pj_fail "BFF /api/tv/login as ${user}: HTTP ${code}"; return 1; }
  PJ_LOGIN_SESSION="$(python3 -c 'import json, sys
print(json.load(open(sys.argv[1], "rb")).get("session_token") or "")' "$PJ_WORK/login" 2>/dev/null)"
  rm -f "$PJ_WORK/login"
  [[ -n "$PJ_LOGIN_SESSION" ]] || { _pj_fail "BFF /api/tv/login as ${user}: no session_token"; return 1; }
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
  local method="$1" path="$2" csrf f
  shift 2
  local -a args=(-sS -X "$method" -b "$PJ_JAR" -c "$PJ_JAR" -o "$PJ_WORK/admin.html" -w '%{http_code}' --max-time 30)
  : >"$PJ_WORK/admin.html"
  if [[ "$method" == GET ]]; then
    PJ_CODE="$(curl "${args[@]}" "${PJ_ADMIN_UI}${path}" 2>"$PJ_WORK/curl.err")" || PJ_CODE=000
    return 0
  fi
  csrf="${PJ_CSRF:-$(_pj_jar_cookie "$PJ_ADMIN_UI" csrf-token)}"
  if [[ -z "$csrf" ]]; then
    PJ_CODE=000
    printf 'no admin-ui csrf-token cookie for %s' "$PJ_ADMIN_UI" >"$PJ_WORK/admin.html"
    return 0
  fi
  args+=(-H 'Content-Type: application/x-www-form-urlencoded')
  for f in "$@"; do
    args+=(--data-urlencode "$f")
  done
  [[ $# -gt 0 ]] || args+=(--data-binary '')
  PJ_CODE="$(curl "${args[@]}" -H @<(printf 'X-CSRF-Token: %s\n' "$csrf") \
    "${PJ_ADMIN_UI}${path}" 2>"$PJ_WORK/curl.err")" || PJ_CODE=000
}

# _pj_admin_login USER PASSWORD → admin-ui session in $PJ_JAR through
# auth-local's password form (same flow as the smoke's admin-ui step; the
# password goes to curl on stdin).
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
  code="$(curl -sS -c "$PJ_JAR" -b "$PJ_JAR" -o /dev/null -w '%{http_code}' --max-time 30 "$loc" 2>"$PJ_WORK/curl.err")" || code=000
  [[ "$code" == 30[23] || "$code" == 200 ]] || { _pj_fail "admin-ui /auth/callback: HTTP ${code}"; return 1; }
  # The dashboard GET also sets admin-ui's csrf-token cookie.
  _pj_admin GET /
  [[ "$PJ_CODE" == 200 ]] || { _pj_fail "admin-ui / after sign-in: HTTP ${PJ_CODE}"; return 1; }
  PJ_CSRF="$(_pj_jar_cookie "$PJ_ADMIN_UI" csrf-token)"
  [[ -n "$PJ_CSRF" ]] || { _pj_fail "admin-ui set no csrf-token cookie"; return 1; }
  _PJ_SECRETS+=("$PJ_CSRF")
}

# _pj_parse_parental FILE → JSON summary of admin-ui's UserParentalForm fragment.
_pj_parse_parental() {
  python3 -c 'import json, sys
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
    print("%s\t%s" % (k, "" if v is None else (str(v).lower() if isinstance(v, bool) else v)))' "$1"
}

# _pj_load_parental FILE → PJ_PARENTAL (JSON summary) and PJ_P[field].
declare -gA PJ_P=()
_pj_load_parental() {
  local out k v
  out="$(_pj_parse_parental "$1")"
  PJ_PARENTAL="${out%%$'\n'*}"
  PJ_P=()
  while IFS=$'\t' read -r k v; do
    [[ -n "$k" ]] && PJ_P["$k"]="$v"
  done <<<"${out#*$'\n'}"
}

# _pj_pget FIELD → one field of the last loaded parental summary.
_pj_pget() {
  printf '%s\n' "${PJ_P[$1]:-}"
}

# _pj_set_restricted USER_ID MAX_RATING → restricted policy (kids mode, the
# given max rating, unrated off, no tags) through admin-ui's provider-backed
# form, based on the revision the form shows; a 409 conflict re-reads once
# more. Read back afterwards.
_pj_set_restricted() {
  local uid="$1" max="$2" attempt rev newrev
  for attempt in 1 2 3; do
    _pj_admin GET "/users/${uid}/parental"
    [[ "$PJ_CODE" == 200 ]] || { _pj_fail "admin-ui GET /users/${uid}/parental: HTTP ${PJ_CODE} — $(_pj_snippet "$PJ_WORK/admin.html")"; return 1; }
    _pj_load_parental "$PJ_WORK/admin.html"
    [[ "$(_pj_pget load_error)" != true ]] || { _pj_fail "admin-ui could not read the provider policy for ${uid} — $(_pj_snippet "$PJ_WORK/admin.html")"; return 1; }
    rev="$(_pj_pget revision)"
    [[ "$rev" =~ ^[0-9]+$ ]] || { _pj_fail "admin-ui parental form for ${uid} has no expected_revision — $(_pj_snippet "$PJ_WORK/admin.html")"; return 1; }
    _pj_admin POST "/users/${uid}/parental" "expected_revision=${rev}" mode=restricted kids_mode=1 "max_rating=${max}" \
      blocked_tags= allowed_tags=
    [[ "$PJ_CODE" == 200 ]] || { _pj_fail "admin-ui POST /users/${uid}/parental: HTTP ${PJ_CODE} — $(_pj_snippet "$PJ_WORK/admin.html")"; return 1; }
    _pj_load_parental "$PJ_WORK/admin.html"
    if [[ "$(_pj_pget conflict)" == true ]]; then
      _pj_say "parental journey: policy revision ${rev} conflicted, re-reading"
      continue
    fi
    [[ "$(_pj_pget saved)" == true && "$(_pj_pget mode)" == restricted ]] || {
      _pj_fail "admin-ui did not save the restricted policy for ${uid}: $(_pj_mask "$PJ_PARENTAL")"; return 1; }
    newrev="$(_pj_pget revision)"
    if ! [[ "$newrev" =~ ^[0-9]+$ ]] || (( newrev <= rev )); then
      _pj_fail "admin-ui saved ${uid} at revision ${newrev:-?}, not above ${rev}"
      return 1
    fi
    # Read back through a fresh GET: the stored document, not the echo.
    _pj_admin GET "/users/${uid}/parental"
    _pj_load_parental "$PJ_WORK/admin.html"
    if [[ "$PJ_CODE" != 200 ]] || ! python3 -c 'import json, sys
d, rev, mx = json.loads(sys.argv[1]), sys.argv[2], sys.argv[3]
ok = (d["state"] == "configured" and d["mode"] == "restricted" and d["revision"] == rev and d["kids_mode"]
      and not d["allow_unrated"] and d["max_rating"] == mx and d["blocked_tags"] == "" and d["allowed_tags"] == "")
sys.exit(0 if ok else 1)' "$PJ_PARENTAL" "$newrev" "$max"; then
      _pj_fail "read-back of ${uid}'s policy is not restricted/kids/${max}/no-unrated at revision ${newrev}: HTTP ${PJ_CODE} $(_pj_mask "$PJ_PARENTAL")"
      return 1
    fi
    _pj_say "OK parental journey: ${PJ_USER} restricted (kids mode, max ${max}, unrated off) via admin-ui at revision ${newrev} (was ${rev})"
    return 0
  done
  _pj_fail "policy for ${uid} still conflicting after ${attempt} attempts"
}

# _pj_rate CHOICE → R|G|… , clear or unrated through admin-ui's content-rating
# page (admin only); requires its "saved and checked" readback.
_pj_rate() {
  local choice="$1" want got
  _pj_admin POST "/media/media-movies/item/${PJ_MOVIE_SEG}/content-rating" "classification=${choice}"
  if [[ "$PJ_CODE" != 200 ]] || ! grep -q 'Content rating saved and checked' "$PJ_WORK/admin.html"; then
    _pj_fail "admin-ui content-rating ${choice} for ${PJ_MOVIE_ID}: HTTP ${PJ_CODE} — $(_pj_snippet "$PJ_WORK/admin.html")"
    return 1
  fi
  PJ_RATING="$choice"
  case "$choice" in
    clear) want="" ;;
    unrated) want="NR" ;;
    *) want="$choice" ;;
  esac
  got="$(python3 -c 'import re, sys
m = re.search(r"Recorded value:\s*<strong>([^<]*)</strong>", open(sys.argv[1], encoding="utf-8", errors="replace").read())
print(m.group(1) if m else "")' "$PJ_WORK/admin.html")"
  [[ "$got" == "$want" ]] || { _pj_fail "admin-ui content-rating ${choice}: recorded value '${got}', want '${want}'"; return 1; }
}

# _pj_phase LABEL CHOICE allow|deny [policy] → rate (or, with `policy`, write
# the member's policy with max rating CHOICE), then poll the member and admin
# views until they match or the deadline passes (the last failing check is
# reported).
_pj_phase() {
  local label="$1" choice="$2" want="$3" what="${4:-rating}" start deadline first="" took
  if [[ "$what" == policy ]]; then
    _pj_set_restricted "$PJ_KID_ID" "$choice" || return 1
  else
    _pj_rate "$choice" || return 1
  fi
  start=$SECONDS
  deadline=$((SECONDS + PJ_TIMEOUT))
  while :; do
    PJ_WHY=""
    if _pj_check_admin && _pj_check_kid "$want"; then
      break
    fi
    [[ -n "$first" ]] || first="$PJ_WHY"
    if (( SECONDS >= deadline )); then
      _pj_fail "phase ${label} (rating ${choice}, member ${want}): not reached within ${PJ_TIMEOUT}s; last: ${PJ_WHY}"
      return 1
    fi
    sleep "$PJ_POLL"
  done
  # Report what lagged when the phase needed more than one poll (ADR-0031 §4
  # allows up to 30 s for cached C-PLAY classifications).
  took="$((SECONDS - start))s"
  [[ -z "$first" ]] || took+="; until then ${first%% — *}"
  if [[ "$what" == policy ]]; then
    _pj_say "OK parental journey phase ${label} (policy max ${choice}, item ${PJ_RATING}): the running BFF denies the member (hidden, detail/resolve/stream 403); admin 206 (${took})"
  elif [[ "$want" == deny ]]; then
    _pj_say "OK parental journey phase ${label} (${choice}): member hidden from /api/movies, detail/resolve/stream 403 without the title; admin listed + 206 (${took})"
  else
    _pj_say "OK parental journey phase ${label} (${choice}): member listed, detail 200, resolve 200, Range stream 206; admin 206 (${took})"
  fi
}

# _pj_blob_unlock → the member writes its own userdata blob with an
# "unrestricted" prefs.parental (and a PIN) and asks resolve with the removed
# query inputs; neither is an authority (ADR-0031 §3), so it stays denied.
_pj_blob_unlock() {
  _pj_bff PUT /api/userdata "$PJ_KID_SESSION" "" \
    '{"prefs":{"parental":{"mode":"unrestricted","kids_mode":false,"max_parental_rating":"","blocked_tags":"","allowed_tags":"","allow_unrated":true,"pin_hash":"","unlocked":true}}}'
  local put="$PJ_CODE"
  case "$put" in
    2??|4??) ;;
    *) _pj_fail "member PUT /api/userdata: HTTP ${put} — $(_pj_snippet "$PJ_WORK/body")"; return 1 ;;
  esac
  PJ_WHY=""
  _pj_check_kid deny || { _pj_fail "after the member wrote an unrestricted prefs.parental blob (PUT /api/userdata HTTP ${put}): ${PJ_WHY}"; return 1; }
  _pj_bff GET "/api/playback/resolve?src=${PJ_SRC_Q}&parental_rating=R&unrated=true&tags=" "$PJ_KID_SESSION"
  _pj_expect_403 "member: GET /api/playback/resolve with parental_rating/unrated query inputs" playback.parental_blocked parental.blocked ||
    { _pj_fail "$PJ_WHY"; return 1; }
  _pj_say "OK parental journey: the member's own unrestricted prefs.parental blob (PUT /api/userdata HTTP ${put}) and resolve query inputs unlock nothing"
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

# _pj_trap_cmd SIGNAL → the command of the current trap for SIGNAL (or empty).
_pj_trap_cmd() {
  local t
  t="$(trap -p "$1")"
  [[ -n "$t" ]] || return 0
  eval "set -- $t"
  printf '%s' "$3"
}

# _pj_unpause — idempotent; also the EXIT/INT/TERM backstop while paused.
_pj_unpause() {
  [[ -n "${PJ_PAUSED:-}" ]] || return 0
  local -a cli=()
  read -ra cli <<<"${MUXCORE_CONTAINER_CLI:-docker}"
  if "${cli[@]}" unpause "$PJ_PAUSED" >/dev/null 2>&1; then
    PJ_PAUSED=""
    return 0
  fi
  echo "FAIL: parental journey: could not unpause the userdata-local container ${PJ_PAUSED}; run: ${cli[*]} unpause ${PJ_PAUSED}" >&2
  return 1
}

# _pj_outage → userdata-local paused: once the member's cached policy (≤30 s)
# is gone, its list and stream fail closed with 503 parental.policy_unavailable
# (never the movie, never an empty 200 after expiry, never 206). Always
# unpaused (trap backstop), then the member recovers.
_pj_outage() {
  local id start deadline rc=0 old_exit old_int old_term got pc state
  local -a cli=()
  read -ra cli <<<"${MUXCORE_CONTAINER_CLI:-docker}"
  id="$(_pj_provider_container)"
  [[ -n "$id" ]] || { _pj_fail "userdata-local container not found for the outage check (set SMOKE_PARENTAL_JOURNEY_PROVIDER_CONTAINER)"; return 1; }
  old_exit="$(trap -p EXIT)" old_int="$(trap -p INT)" old_term="$(trap -p TERM)"
  # Chain the unpause in front of the caller's EXIT handler while paused.
  # shellcheck disable=SC2064 # expand the previous handler now
  trap "_pj_unpause; $(_pj_trap_cmd EXIT)" EXIT
  trap '_pj_unpause; exit 130' INT
  trap '_pj_unpause; exit 143' TERM
  "${cli[@]}" pause "$id" >/dev/null 2>"$PJ_WORK/curl.err" || {
    _pj_fail "${cli[*]} pause userdata-local: $(head -c 200 "$PJ_WORK/curl.err")"; rc=1; }
  [[ "$rc" -ne 0 ]] || PJ_PAUSED="$id"
  start=$SECONDS
  deadline=$((SECONDS + PJ_OUTAGE_TIMEOUT))
  while [[ "$rc" -eq 0 ]]; do
    PJ_WHY=""
    _pj_bff GET "/api/movies?page=1&page_size=200" "$PJ_KID_SESSION"
    if [[ "$PJ_CODE" == 503 ]]; then
      IFS=$'\t' read -r got pc < <(_pj_json_code "$PJ_WORK/body")
      if [[ "$got" == parental.policy_unavailable ]]; then
        _pj_bff GET "/stream/movies/${PJ_MOVIE_ID}" "$PJ_KID_SESSION" 0-1023
        IFS=$'\t' read -r got pc < <(_pj_json_code "$PJ_WORK/body")
        if [[ "$PJ_CODE" == 503 && "$got" == parental.policy_unavailable ]]; then
          break
        fi
        PJ_WHY="member: Range stream during the outage: HTTP ${PJ_CODE} code=${got:-<none>}, want 503 parental.policy_unavailable"
        [[ "$PJ_CODE" != 206 ]] || { _pj_fail "$PJ_WHY (fail-open)"; rc=1; break; }
      else
        PJ_WHY="member: GET /api/movies during the outage: 503 code=${got:-<none>}, want parental.policy_unavailable"
      fi
    elif [[ "$PJ_CODE" == 200 ]]; then
      # Still inside the cached-policy window: the movie (NR) must stay hidden.
      state="$(python3 -c 'import json, sys
items = json.load(open(sys.argv[1], "rb")).get("items") or []
print("listed" if any(isinstance(i, dict) and i.get("id") == sys.argv[2] for i in items) else "hidden")' "$PJ_WORK/body" "$PJ_MOVIE_ID" 2>/dev/null || echo invalid)"
      [[ "$state" == hidden ]] || { _pj_fail "member: GET /api/movies during the outage lists the blocked movie (${state})"; rc=1; break; }
      PJ_WHY="member: GET /api/movies during the outage still HTTP 200 (cached policy)"
    else
      PJ_WHY="member: GET /api/movies during the outage: HTTP ${PJ_CODE}, want 503 parental.policy_unavailable — $(_pj_snippet "$PJ_WORK/body")"
    fi
    if (( SECONDS >= deadline )); then
      _pj_fail "outage: not failed closed within ${PJ_OUTAGE_TIMEOUT}s; last: ${PJ_WHY}"
      rc=1
      break
    fi
    sleep "$PJ_POLL"
  done
  local took=$((SECONDS - start))
  _pj_unpause || rc=1
  if [[ -n "$old_exit" ]]; then eval "$old_exit"; else trap - EXIT; fi
  if [[ -n "$old_int" ]]; then eval "$old_int"; else trap - INT; fi
  if [[ -n "$old_term" ]]; then eval "$old_term"; else trap - TERM; fi
  [[ "$rc" -eq 0 ]] || return 1
  # Recovery: errors are never cached, so the member's next requests succeed.
  deadline=$((SECONDS + PJ_OUTAGE_TIMEOUT))
  until _pj_list_state "$PJ_KID_SESSION"; do
    if (( SECONDS >= deadline )); then
      _pj_fail "after unpausing userdata-local the member did not recover within ${PJ_OUTAGE_TIMEOUT}s; last: ${PJ_WHY}"
      return 1
    fi
    sleep "$PJ_POLL"
  done
  # Leave the provider as later smoke steps expect it: healthy again (its
  # healthcheck failed while paused). No healthcheck → nothing to wait for.
  local health=""
  until health="$("${cli[@]}" inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$id" 2>/dev/null)" &&
    [[ "$health" == healthy || "$health" == none ]]; do
    if (( SECONDS >= deadline )); then
      _pj_fail "userdata-local container ${id} not healthy again after the outage (${health:-unknown})"
      return 1
    fi
    sleep "$PJ_POLL"
  done
  _pj_say "OK parental journey: userdata-local paused → member /api/movies and stream 503 parental.policy_unavailable after ${took}s (cached policy ≤30 s), never the movie; unpaused, recovered, provider ${health}"
}

# _pj_rbac → operator routes and C-DENY routes refuse the member before any
# module call; the movie survives the DELETE attempt.
_pj_rbac() {
  _pj_bff DELETE "/api/movies/${PJ_MOVIE_ID}" "$PJ_KID_SESSION"
  _pj_expect_403 "member: DELETE /api/movies/${PJ_MOVIE_ID}" operator.forbidden || { _pj_fail "$PJ_WHY"; return 1; }
  _pj_bff POST /api/releases/grab "$PJ_KID_SESSION" "" '{}'
  _pj_expect_403 "member: POST /api/releases/grab" operator.forbidden || { _pj_fail "$PJ_WHY"; return 1; }
  _pj_bff POST /api/sessions/smoke-parental-journey/stop "$PJ_KID_SESSION" "" '{}'
  _pj_expect_403 "member: POST /api/sessions/{id}/stop" operator.forbidden || { _pj_fail "$PJ_WHY"; return 1; }
  _pj_bff GET "/api/search?q=$(_pj_urlencode "${PJ_TITLE:-fight}")" "$PJ_KID_SESSION"
  _pj_expect_403 "member: GET /api/search" parental.restricted_route || { _pj_fail "$PJ_WHY"; return 1; }
  _pj_bff GET /api/discover/trending "$PJ_KID_SESSION"
  _pj_expect_403 "member: GET /api/discover/trending" parental.restricted_route || { _pj_fail "$PJ_WHY"; return 1; }
  _pj_check_admin || { _pj_fail "after the member's DELETE attempt: ${PJ_WHY}"; return 1; }
  _pj_say "OK parental journey: member gets 403 operator.forbidden (DELETE movie, grab, session stop) and 403 parental.restricted_route (search, discover); movie intact"
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
# BFF list; sets PJ_MOVIE_ID and PJ_TITLE.
_pj_find_movie() {
  local found
  _pj_bff GET "/api/movies?page=1&page_size=200" "$PJ_ADMIN_SESSION"
  [[ "$PJ_CODE" == 200 ]] || { _pj_fail "admin GET /api/movies: HTTP ${PJ_CODE}"; return 1; }
  found="$(python3 -c 'import json, sys
tmdb = int(sys.argv[2])
for it in json.load(open(sys.argv[1], "rb")).get("items") or []:
    if int(it.get("tmdb_id") or 0) == tmdb and it.get("has_file") and it.get("id"):
        print("%s\t%s" % (it["id"], it.get("title") or "")); break' "$PJ_WORK/body" "$PJ_TMDB")"
  [[ -n "$found" ]] || { _pj_fail "no fixture movie (tmdb ${PJ_TMDB}, has_file) in the admin's /api/movies — the acquisition step must import it first"; return 1; }
  IFS=$'\t' read -r PJ_MOVIE_ID PJ_TITLE <<<"$found"
  PJ_MOVIE_SEG="$(_pj_urlencode "$PJ_MOVIE_ID")"
  PJ_SRC_Q="$(_pj_urlencode "/stream/movies/${PJ_MOVIE_ID}")"
}

# _pj_cleanup → rating cleared, member deleted. Best effort for each part;
# returns non-zero when either fails.
_pj_cleanup() {
  local auth_bearer="$1" rc=0
  _pj_unpause || rc=1
  if [[ "${PJ_ADMIN_READY:-0}" == 1 && -n "${PJ_MOVIE_ID:-}" ]]; then
    _pj_rate clear || rc=1
  fi
  if [[ -n "$auth_bearer" ]]; then
    parental_smoke_delete_user "$PJ_AUTH" "$auth_bearer" "$PJ_USER" || rc=1
  fi
  return "$rc"
}

# parental_journey_run MEDIA_UI_URL ADMIN_UI_URL AUTH_URL ADMIN_USER ADMIN_PASSWORD AUTH_ADMIN_BEARER
# Runs the whole journey (see the header) and always cleans up. AUTH_ADMIN_BEARER
# is the admin's auth-local bearer (parental_smoke_device_login) used for the
# auth-local user admin API.
parental_journey_run() {
  PJ_BFF="${1%/}" PJ_ADMIN_UI="${2%/}" PJ_AUTH="${3%/}"
  local admin_user="$4" admin_pass="$5" auth_bearer="$6" rc=0 t0=$SECONDS
  PJ_USER="${SMOKE_PARENTAL_JOURNEY_USER:-$PARENTAL_JOURNEY_USER_DEFAULT}"
  PJ_TMDB="${SMOKE_PARENTAL_JOURNEY_TMDB:-550}"
  PJ_TIMEOUT="${SMOKE_PARENTAL_JOURNEY_TIMEOUT_SEC:-45}"
  PJ_POLL="${SMOKE_PARENTAL_JOURNEY_POLL_SEC:-3}"
  # The outage needs the cached policy (≤30 s) to expire plus the BFF's 5 s
  # provider timeout per request.
  PJ_OUTAGE_TIMEOUT="${SMOKE_PARENTAL_JOURNEY_OUTAGE_TIMEOUT_SEC:-60}"
  PJ_MOVIE_ID="" PJ_TITLE="" PJ_MOVIE_SEG="" PJ_SRC_Q="" PJ_KID_ID="" PJ_ADMIN_READY=0 PJ_ADMIN_TOTAL=""
  PJ_ADMIN_SESSION="" PJ_KID_SESSION="" PJ_CODE="" PJ_WHY="" PJ_RATING="" PJ_PAUSED="" PJ_CSRF=""
  PJ_KID_PASS="$(python3 -c 'import secrets; print(secrets.token_urlsafe(24))')"
  _PJ_SECRETS=("$admin_pass" "$auth_bearer" "$PJ_KID_PASS")
  PJ_WORK="$(mktemp -d)" || { _pj_fail "mktemp"; return 1; }
  PJ_JAR="$PJ_WORK/admin-ui.jar"
  : >"$PJ_JAR"
  chmod 600 "$PJ_JAR"

  _pj_journey "$admin_user" "$admin_pass" "$auth_bearer" || rc=1
  if ! _pj_cleanup "$auth_bearer"; then
    _pj_fail "cleanup (rating cleared, ${PJ_USER} deleted) did not complete"
    rc=1
  fi
  rm -rf "$PJ_WORK"
  _PJ_SECRETS=()
  if [[ "$rc" -eq 0 ]]; then
    echo "OK parental journey: restricted member ${PJ_USER} vs admin on movie ${PJ_MOVIE_ID} (phases A R + blob, B G, B2 PG, T policy max G, C cleared, D NR, RBAC, provider outage) in $((SECONDS - t0))s; cleaned up"
  fi
  return "$rc"
}

_pj_journey() {
  local admin_user="$1" admin_pass="$2" auth_bearer="$3" code
  code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 "${PJ_ADMIN_UI}/health" 2>/dev/null)" || code=000
  [[ "$code" == 200 ]] || { _pj_fail "admin-ui ${PJ_ADMIN_UI}/health HTTP ${code}: a household run needs admin-ui for the policy and rating writes"; return 1; }

  _pj_bff_login "$admin_user" "$admin_pass" || return 1
  PJ_ADMIN_SESSION="$PJ_LOGIN_SESSION"
  _PJ_SECRETS+=("$PJ_ADMIN_SESSION")
  _pj_find_movie || return 1
  _pj_say "parental journey: fixture movie ${PJ_MOVIE_ID} \"${PJ_TITLE}\" (tmdb ${PJ_TMDB})"

  _pj_admin_login "$admin_user" "$admin_pass" || return 1
  PJ_ADMIN_READY=1
  _pj_ensure_member "$auth_bearer" || return 1
  _pj_set_restricted "$PJ_KID_ID" PG || return 1

  # Signed in after the policy write, so no earlier `unconfigured` answer is
  # cached for this bearer (ADR-0031 §4).
  _pj_bff_login "$PJ_USER" "$PJ_KID_PASS" || return 1
  PJ_KID_SESSION="$PJ_LOGIN_SESSION"
  _PJ_SECRETS+=("$PJ_KID_SESSION")

  _pj_phase A R deny || return 1
  _pj_blob_unlock || return 1
  _pj_phase B G allow || return 1
  _pj_phase B2 PG allow || return 1
  # A stricter policy (max G) reaches the running BFF (policy cache ≤30 s).
  _pj_phase T G deny policy || return 1
  _pj_phase C clear deny || return 1
  _pj_phase D unrated deny || return 1
  _pj_rbac || return 1
  _pj_outage || return 1
}
