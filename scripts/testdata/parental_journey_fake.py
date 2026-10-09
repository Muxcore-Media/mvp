"""Fake auth-local + admin-ui + consumer BFF for scripts/parental-journey_test.sh.

Mirrors only what scripts/lib/parental-journey.sh touches:
- auth-local: /login (CSRF cookie), /login/password (redirect with a code),
  /login/device, the admin user API (/api/users, /api/users/{id},
  /api/users/{id}/password);
- admin-ui: /health, /auth/callback, / (sets the csrf-token cookie),
  GET/POST /users/{id}/parental (UserParentalForm fragment, revision checked)
  and POST /media/media-movies/item/{id}/content-rating, both behind the
  double-submit CSRF check (cookie csrf-token == X-CSRF-Token);
- BFF: /healthz, POST /api/tv/login, POST /logout, /api/userdata,
  /api/movies, /api/movies/{id}, /api/playback/resolve, /stream/movies/{id},
  operator routes, /api/search and /api/discover/movie/{id}. Restricted
  policies are evaluated per ADR-0031 §2.6 with a per-session policy cache and
  a per-item classification cache for play routes (TTLs settable, so stale
  propagation beyond the bound can be modelled); a "paused" provider makes
  expired policy lookups 503 parental.policy_unavailable. Optional TMDB
  fallback models ADR-0031 S4 (operator > tmdb > unavailable).

POST /_control sets FLAGS that break one behaviour each, so the test can prove
every journey assertion fails when the product misbehaves. GET /_log returns
state for assertions."""
import html
import json
import secrets
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, quote, unquote, urlsplit

MOVIE_ID = "mv_550_2026-10-09T18:48:06Z"
TITLE = "Fight Club"
LADDER = ["G", "PG", "PG-13", "R", "NC-17"]
UNRESTRICTED = {"version": 1, "mode": "unrestricted", "rules": None}

USERS = {"admin": {"id": "u-admin", "password": "admin-pass-123", "roles": ["admin"]}}
AUTH_TOKENS = {}  # auth-local bearer -> uid
CODES = {}  # login code -> uid
ADMIN_SESSIONS = {}  # admin-ui session cookie -> uid
BFF = {}  # BFF session token -> uid
POLICIES = {"u-admin": (1, UNRESTRICTED)}  # uid -> (revision, policy)
POLICY_CACHE = {}  # BFF token -> (fetched_at, policy or None)
CLASS_CACHE = {}  # movie id -> (fetched_at, effective rating)
BLOBS = {}  # uid -> userdata blob
WRITES = {}  # uid -> (time, previous policy) of the last policy write
RATED = {}  # movie id -> (time, previous effective rating) of the last rating change


def new_movie(mid, tmdb, title):
    return {"id": mid, "tmdb_id": tmdb, "title": title, "rating": "", "source": ""}


MOVIES = {MOVIE_ID: new_movie(MOVIE_ID, 550, TITLE), "mv_other": new_movie("mv_other", 680, "Pulp Fiction")}
FLAGS = {}
PAUSED = [False]
LOG = {"csrf_posts": [], "kid_requests": [], "module_calls": [], "passwords": [], "secrets": [],
       "policy_puts": 0, "pause_calls": [], "logouts": 0}


def user_by_id(uid):
    return next((u for u in USERS.values() if u["id"] == uid), None)


def name_by_id(uid):
    return next((k for k, v in USERS.items() if v["id"] == uid), None)


def new_secret(prefix):
    s = prefix + secrets.token_hex(8)
    LOG["secrets"].append(s)
    return s


def effective(m):
    """(rating, source) as the media module reports it: operator > tmdb > none."""
    if m["source"] == "operator":
        return m["rating"], "operator"
    if FLAGS.get("tmdb_fallback"):
        return "R", "tmdb"
    return "", ""


def privileged(uid):
    u = user_by_id(uid)
    return bool(u) and ("admin" in u["roles"] or "manager" in u["roles"])


class Unavailable(Exception):
    pass


def session_policy(tok, uid):
    """Policy for a BFF session through the ≤TTL cache; the provider being
    paused makes an expired lookup unavailable."""
    now, ttl = time.time(), float(FLAGS.get("policy_ttl", 0.2))
    if PAUSED[0]:
        ttl = float(FLAGS.get("outage_policy_ttl", ttl))
    hit = POLICY_CACHE.get(tok)
    if hit and now - hit[0] < ttl:
        return hit[1]
    if PAUSED[0] and not FLAGS.get("outage_open"):
        raise Unavailable()
    pol = POLICIES.get(uid, (0, None))[1]
    w = WRITES.get(uid)
    if w and now - w[0] < float(FLAGS.get("policy_stale_for", 0)):
        pol = w[1]  # a BFF that keeps the old policy this long after a write
    POLICY_CACHE[tok] = (now, pol)
    return pol


def play_rating(m):
    now, ttl = time.time(), float(FLAGS.get("class_ttl", 0.2))
    hit = CLASS_CACHE.get(m["id"])
    if hit and now - hit[0] < ttl:
        return hit[1]
    r = effective(m)[0]
    w = RATED.get(m["id"])
    if w and now - w[0] < float(FLAGS.get("class_stale_for", 0)):
        r = w[1]  # a BFF that keeps the old classification this long after a change
    CLASS_CACHE[m["id"]] = (now, r)
    return r


def ceiling(rules):
    if rules["max_rating"]:
        return LADDER.index(rules["max_rating"])
    if rules["kids_mode"] and not FLAGS.get("kids_no_default"):
        return LADDER.index("PG")
    return len(LADDER) - 1


def verdict(pol, uid, rating, q=None):
    """None = allowed, else the parental code."""
    if pol is None:
        return "parental.policy_unconfigured"
    if pol["mode"] == "unrestricted":
        return None
    if FLAGS.get("blob_authority") and ((BLOBS.get(uid) or {}).get("prefs") or {}).get("parental", {}).get("mode") == "unrestricted":
        return None
    r = pol["rules"]
    if FLAGS.get("trust_query") and q and q.get("parental_rating"):
        claimed = q["parental_rating"][0]
        if claimed in LADDER and LADDER.index(claimed) <= ceiling(r):
            return None
    if rating == "":
        if FLAGS.get("unavailable_visible"):
            return None
        if FLAGS.get("unavailable_as_unrated") and r["allow_unrated"]:
            return None
        return "parental.blocked"
    if rating in ("NR", "UR"):
        return None if (r["allow_unrated"] or FLAGS.get("nr_allowed")) else "parental.blocked"
    if rating not in LADDER:
        return "parental.blocked"
    return None if LADDER.index(rating) <= ceiling(r) else "parental.blocked"


def parental_fragment(uid, saved=False, conflict=False, pol_override=None):
    rev, pol = POLICIES.get(uid, (0, None))
    if pol_override is not None:
        rev, pol = pol_override
    state = "configured" if pol else "unconfigured"
    mode = pol["mode"] if pol else ""
    r = (pol or {}).get("rules") or {}
    opts = "".join('<option value="%s"%s>%s</option>' % (t, " selected" if r.get("max_rating") == t else "", t) for t in LADDER)
    return (
        '<div data-testid="parental-form" data-parental-user="%s">' % html.escape(uid)
        + '<p data-testid="parental-state" data-state="%s" data-mode="%s">state</p>' % (state, mode)
        + ('<p data-testid="parental-saved">Parental policy saved.</p>' if saved else "")
        + ('<div data-testid="parental-conflict">changed by someone else</div>' if conflict else "")
        + '<form><input type="hidden" name="expected_revision" value="%d"/>' % rev
        + '<select name="mode"><option value="restricted"%s>R</option></select>' % (" selected" if mode == "restricted" else "")
        + '<input type="checkbox" name="kids_mode" value="1"%s/>' % (" checked" if r.get("kids_mode") else "")
        + '<select name="max_rating"><option value=""%s>none</option>%s</select>' % (" selected" if not r.get("max_rating") else "", opts)
        + '<input name="blocked_tags" value="%s"/>' % ", ".join(r.get("blocked_tags") or [])
        + '<input name="allowed_tags" value="%s"/>' % ", ".join(r.get("allowed_tags") or [])
        + '<input type="checkbox" name="allow_unrated" value="1"%s/>' % (" checked" if r.get("allow_unrated") else "")
        + "</form></div>"
    )


def movie_json(m, view=None):
    r, s = effective(m)
    if FLAGS.get("effective_wrong") and s == "operator":
        r = "G"
    out = {"id": m["id"], "tmdb_id": m["tmdb_id"], "title": m["title"], "has_file": True,
           "content_rating": r, "content_rating_source": s, "stream_url": "/stream/movies/" + quote(m["id"], safe=":")}
    return out


class H(BaseHTTPRequestHandler):
    disable_nagle_algorithm = True  # headers and body go out as separate writes

    def log_message(self, *args):
        pass

    def send(self, code, obj=None, ctype="application/json", headers=None, raw=None):
        if raw is not None:
            body = raw
        elif obj is None:
            body = b""
        elif isinstance(obj, str):
            body = obj.encode()
        else:
            body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        for k, v in (headers or []):
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(body)

    def deny(self, pcode, alias="", status=403):
        body = {"error": "denied", "code": alias or pcode}
        if alias:
            body["parental_code"] = pcode
        if FLAGS.get("missing_code"):
            body = {"error": "denied", "parental_code": pcode}
        if FLAGS.get("title_leak"):
            body["error"] = "%s is not allowed" % TITLE
        hdrs = [] if FLAGS.get("no_store_missing") else [("Cache-Control", "no-store")]
        return self.send(status, body, headers=hdrs)

    def body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(n) if n else b""

    def cookies(self):
        out = {}
        for h in self.headers.get_all("Cookie") or []:
            for part in h.split(";"):
                k, _, v = part.strip().partition("=")
                out[k] = v
        return out

    def bearer(self):
        auth = self.headers.get_all("Authorization") or []
        if len(auth) != 1 or not auth[0].startswith("Bearer "):
            return None
        return auth[0][7:]

    def do_GET(self):
        self.route("GET")

    def do_POST(self):
        self.route("POST")

    def do_PUT(self):
        self.route("PUT")

    def do_DELETE(self):
        self.route("DELETE")

    def route(self, method):
        u = urlsplit(self.path)
        path, q = u.path, parse_qs(u.query, keep_blank_values=True)
        if path == "/_control":
            if self.headers.get("X-Reset"):
                FLAGS.clear()
                MOVIES.setdefault(MOVIE_ID, new_movie(MOVIE_ID, 550, TITLE))
            FLAGS.update(json.loads(self.body() or b"{}"))
            if "pause" in FLAGS:
                PAUSED[0] = bool(FLAGS.pop("pause"))
                LOG["pause_calls"].append(PAUSED[0])
            if FLAGS.pop("leftover_kid", False):
                USERS["smoke-kid"] = {"id": "u-leftover", "password": "old-pass-xyz", "roles": ["viewer"]}
                POLICIES["u-leftover"] = (3, UNRESTRICTED)
            if "set_rating" in FLAGS:
                v = FLAGS.pop("set_rating")
                MOVIES[MOVIE_ID].update(rating=v, source="operator" if v else "")
            return self.send(200, {})
        if path == "/_log":
            m = MOVIES.get(MOVIE_ID)
            return self.send(200, dict(LOG, paused=PAUSED[0], users=sorted(USERS),
                                       rating=(m["rating"], m["source"]) if m else None, movies=sorted(MOVIES),
                                       bff_sessions=len(BFF)))
        # ---------------- auth-local ----------------
        if path == "/login" and method == "GET":
            tok = new_secret("acsrf-")
            return self.send(200, "<form>login</form>", "text/html",
                             [("Set-Cookie", "muxcore-auth-csrf=%s; Path=/; HttpOnly" % tok)])
        if path == "/login/password" and method == "POST":
            f = parse_qs(self.body().decode())
            usr = USERS.get((f.get("username") or [""])[0])
            if (f.get("csrf_token") or [""])[0] != self.cookies().get("muxcore-auth-csrf"):
                return self.send(403, "csrf", "text/plain")
            if not usr or usr["password"] != (f.get("password") or [""])[0]:
                return self.send(401, "bad credentials", "text/plain")
            code = new_secret("code-")
            CODES[code] = usr["id"]
            return self.send(303, None, "text/plain", [("Location", f["redirect"][0] + "?code=" + code)])
        if path == "/login/device" and method == "POST":
            d = json.loads(self.body())
            usr = USERS.get(d.get("username"))
            if not usr or usr["password"] != d.get("password"):
                return self.send(401)
            tok = new_secret("tok-")
            AUTH_TOKENS[tok] = usr["id"]
            return self.send(200, {"token": tok, "user_id": usr["id"]})
        if path.startswith("/api/users"):
            uid = AUTH_TOKENS.get(self.bearer() or "")
            if not uid or not privileged(uid):
                return self.send(403)
            if path == "/api/users" and method == "GET":
                return self.send(200, {"users": [{"id": v["id"], "username": k, "roles": v["roles"]} for k, v in USERS.items()]})
            if path == "/api/users" and method == "POST":
                d = json.loads(self.body())
                if d["username"] in USERS:
                    return self.send(400, "exists", "text/plain")
                USERS[d["username"]] = {"id": "u-" + secrets.token_hex(3), "password": d["password"], "roles": [d.get("role", "user")]}
                LOG["passwords"].append(d["password"])
                return self.send(201, {"user": {"id": USERS[d["username"]]["id"], "username": d["username"]}})
            rest = path[len("/api/users/"):]
            if rest.endswith("/password") and method == "POST":
                name = name_by_id(rest[: -len("/password")])
                if not name:
                    return self.send(404)
                USERS[name]["password"] = json.loads(self.body())["password"]
                LOG["passwords"].append(USERS[name]["password"])
                return self.send(200, {"ok": True})
            name = name_by_id(rest)
            if not name:
                return self.send(404)
            if method == "DELETE":
                if FLAGS.get("delete_user_fails"):
                    return self.send(500)
                del USERS[name]
                return self.send(200, {"removed": True})
            if method == "POST":
                USERS[name]["roles"] = json.loads(self.body())["roles"]
                return self.send(200, {"user": {"id": USERS[name]["id"]}})
            return self.send(405)
        # ---------------- admin-ui ----------------
        if path == "/health":
            return self.send(503 if FLAGS.get("admin_down") else 200, {"ok": True})
        if path == "/auth/callback":
            uid = CODES.pop((q.get("code") or [""])[0], None)
            if not uid:
                return self.send(400)
            sess = new_secret("asess-")
            ADMIN_SESSIONS[sess] = uid
            return self.send(303, None, "text/plain", [("Location", "/"), ("Set-Cookie", "session=%s; Path=/; HttpOnly" % sess)])
        if path == "/" or path.startswith("/users/") or path.startswith("/media/"):
            return self.admin_ui(method, path)
        # ---------------- BFF ----------------
        if path == "/healthz":
            return self.send(200, {"ok": True})
        if path == "/api/tv/login" and method == "POST":
            d = json.loads(self.body())
            usr = USERS.get(d.get("username"))
            if FLAGS.get("tv_login_echo") and d.get("username") == "smoke-kid":
                return self.send(401, {"code": "auth.invalid_credentials", "error": "rejected password %s" % d.get("password")})
            if not usr or usr["password"] != d.get("password"):
                return self.send(401, {"code": "auth.invalid_credentials"})
            tok = new_secret("bff-")
            BFF[tok] = usr["id"]
            return self.send(200, {"session_token": tok, "user_id": usr["id"]})
        if path == "/logout" and method == "POST":
            LOG["logouts"] += 1
            if not FLAGS.get("logout_noop"):
                BFF.pop(self.bearer() or "", None)
            return self.send(200, {"logged_out": True})
        return self.bff(method, path, q)

    def admin_ui(self, method, path):
        uid = ADMIN_SESSIONS.get(self.cookies().get("session", ""))
        if not uid:
            return self.send(303, None, "text/plain", [("Location", "/login")])
        hdrs = []
        if method == "GET" and not self.cookies().get("csrf-token"):
            hdrs.append(("Set-Cookie", "csrf-token=%s; Path=/" % new_secret("csrf-")))
        if method == "POST":
            ok = self.headers.get("X-CSRF-Token") and self.headers.get("X-CSRF-Token") == self.cookies().get("csrf-token")
            LOG["csrf_posts"].append({"path": path, "ok": bool(ok)})
            if not ok:
                return self.send(403, "Forbidden", "text/plain")
        if path == "/":
            return self.send(200, "<main>Dashboard</main>", "text/html", hdrs)
        parts = path.strip("/").split("/")
        if len(parts) == 3 and parts[0] == "users" and parts[2] == "parental":
            target = parts[1]
            if method == "GET":
                return self.send(200, parental_fragment(target), "text/html", hdrs)
            f = parse_qs(self.body().decode(), keep_blank_values=True)
            g = lambda k: (f.get(k) or [""])[0]
            rev = POLICIES.get(target, (0, None))[0]
            if FLAGS.pop("conflict_once", False):
                POLICIES[target] = (rev + 1, UNRESTRICTED)
                return self.send(200, parental_fragment(target, conflict=True), "text/html")
            if int(g("expected_revision") or -1) != rev:
                return self.send(200, parental_fragment(target, conflict=True), "text/html")
            pol = {"version": 1, "mode": g("mode"), "rules": {
                "kids_mode": g("kids_mode") == "1", "max_rating": g("max_rating"),
                "blocked_tags": [t.strip() for t in g("blocked_tags").split(",") if t.strip()],
                "allowed_tags": [t.strip() for t in g("allowed_tags").split(",") if t.strip()],
                "allow_unrated": g("allow_unrated") == "1" or bool(FLAGS.get("store_allow_unrated"))}}
            LOG["policy_puts"] += 1
            if FLAGS.get("policy_not_saved"):
                return self.send(200, parental_fragment(target, saved=True, pol_override=(rev + 1, pol)), "text/html")
            WRITES[target] = (time.time(), POLICIES.get(target, (0, None))[1])
            POLICIES[target] = (rev + 1, pol)
            return self.send(200, parental_fragment(target, saved=True), "text/html")
        if len(parts) == 5 and parts[:3] == ["media", "media-movies", "item"] and parts[4] == "content-rating" and method == "POST":
            if not privileged(uid):
                return self.send(403, "admins only", "text/html")
            f = parse_qs(self.body().decode(), keep_blank_values=True)
            if list(f) != ["classification"] or len(f["classification"]) != 1:
                return self.send(400, "Submit exactly one operator classification.", "text/html")
            m = MOVIES.get(unquote(parts[3]))
            if not m:
                return self.send(404, "not found", "text/html")
            choice = f["classification"][0]
            RATED[m["id"]] = (time.time(), effective(m)[0])
            if FLAGS.get("rating_unconfirmed"):
                return self.send(502, "The save was acknowledged, but its current classification could not be confirmed.", "text/html")
            if choice == "clear":
                m.update(rating="", source="")
            else:
                m.update(rating="NR" if choice == "unrated" else choice, source="operator")
            r, s = effective(m)
            if choice == "clear" and s:
                # admin-ui compares the readback with ""/"" and cannot confirm a TMDB fallback.
                return self.send(502, "The save was acknowledged, but its current classification could not be confirmed.", "text/html")
            rec = "<p>Recorded value: <strong>%s</strong></p>" % html.escape(r) if r else ""
            return self.send(200, "<p role=status>Content rating saved and checked.</p>" + rec, "text/html")
        return self.send(404)

    def bff(self, method, path, q):
        tok = self.bearer()
        uid = BFF.get(tok or "")
        if not uid:
            return self.send(401, {"code": "auth.session_required"})
        if uid != "u-admin":
            LOG["kid_requests"].append({"method": method, "path": path, "auth": len(self.headers.get_all("Authorization") or [])})
        # Operator routes: role gate before any policy lookup or module call.
        operator = (method == "DELETE" and path.startswith("/api/movies/")) or path == "/api/releases/grab" or (
            path.startswith("/api/sessions/") and path.endswith("/stop"))
        if operator:
            if privileged(uid) or FLAGS.get("rbac_open"):
                LOG["module_calls"].append(path)
                if method == "DELETE":
                    MOVIES.pop(unquote(path.rsplit("/", 1)[1]), None)
                return self.send(200, {"ok": True})
            return self.send(403, {"error": "forbidden", "code": FLAGS.get("rbac_code") or "operator.forbidden"})
        if path == "/api/userdata":
            if method == "PUT":
                if FLAGS.get("userdata_put_400"):
                    return self.send(400, {"code": "userdata.invalid"})
                d = json.loads(self.body() or b"{}")
                if not FLAGS.get("userdata_drop"):
                    BLOBS[uid] = d
                return self.send(200, BLOBS.get(uid, {}))
            return self.send(200, dict(BLOBS.get(uid, {"prefs": {}}), user_id=uid))
        try:
            pol = session_policy(tok, uid)
        except Unavailable:
            if FLAGS.get("outage_stream_open") and path.startswith("/stream/"):
                return self.send(206, raw=b"\x00" * 1024, ctype="video/mp4")
            return self.deny("parental.policy_unavailable", status=503)
        restricted = pol is not None and pol["mode"] == "restricted"
        if path == "/api/search" or path.startswith("/api/discover/"):
            if path.startswith("/api/discover/") and (path != "/api/discover/movie/550" or (FLAGS.get("cdeny_route_missing") and not restricted)):
                return self.send(404, "404 page not found", "text/plain")
            if restricted and not FLAGS.get("cdeny_open"):
                return self.send(403, {"error": "restricted", "code": FLAGS.get("cdeny_code") or "parental.restricted_route"},
                                 headers=[("Cache-Control", "no-store")])
            return self.send(200, {"results": [{"title": TITLE}]})
        if path == "/api/movies":
            if FLAGS.get("list_503"):
                return self.deny("parental.classification_unavailable", status=503)
            if FLAGS.get("list_garbage") and uid != "u-admin":
                return self.send(200, "<html>upstream hiccup</html>", "text/html")
            items, hidden = [], 0
            for m in MOVIES.values():
                if verdict(pol, uid, effective(m)[0], q) is None or (FLAGS.get("list_leak") and m["id"] == MOVIE_ID):
                    items.append(movie_json(m))
                else:
                    hidden += 1
            total = len(items) + (hidden if FLAGS.get("total_leak") else 0)
            return self.send(200, {"items": items, "total": total, "page": 1, "page_size": 20})
        if path.startswith("/api/movies/"):
            m = MOVIES.get(unquote(path[len("/api/movies/"):]))
            if not m:
                return self.send(404, {"code": "movies.not_found"})
            why = verdict(pol, uid, effective(m)[0], q)
            if why and FLAGS.get("detail_open"):
                body = movie_json(m)
                if FLAGS.get("echo_bearer"):
                    body["debug"] = "served to Bearer %s" % tok
                return self.send(200, body)
            if why:
                return self.deny(FLAGS.get("detail_code") or why)
            return self.send(200, movie_json(m))
        if path == "/api/playback/resolve":
            src = (q.get("src") or [""])[0]
            m = MOVIES.get(unquote(src.rsplit("/", 1)[-1]))
            if not m:
                return self.send(404, {"code": "playback.not_found"})
            why = verdict(pol, uid, play_rating(m), q)
            if why and not FLAGS.get("resolve_open"):
                return self.deny(why, "" if FLAGS.get("resolve_no_alias") else "playback.parental_blocked")
            return self.send(200, {"stream_url": src, "mode": "direct"})
        if path.startswith("/stream/movies/"):
            m = MOVIES.get(unquote(path[len("/stream/movies/"):]))
            if not m:
                return self.send(404)
            why = verdict(pol, uid, play_rating(m), q)
            if FLAGS.get("admin_stream_denied") and uid == "u-admin":
                why = "parental.blocked"
            if FLAGS.get("allowed_stream_denied") and not why and uid != "u-admin":
                why = "parental.blocked"
            if why and not FLAGS.get("stream_open"):
                return self.deny(why)
            return self.send(206, raw=b"\x00" * 1024, ctype="video/mp4", headers=[("Content-Range", "bytes 0-1023/4096")])
        return self.send(404)


if __name__ == "__main__":
    srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
    print(srv.server_address[1], flush=True)
    srv.serve_forever()
