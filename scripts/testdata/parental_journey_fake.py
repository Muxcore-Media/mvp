"""Fake auth-local + admin-ui + consumer BFF for scripts/parental-journey_test.sh.

Mirrors only what scripts/lib/parental-journey.sh touches:
- auth-local: /login (CSRF cookie), /login/password (redirect with a code),
  /login/device, the admin user API (/api/users, /api/users/{id},
  /api/users/{id}/password);
- admin-ui: /health, /auth/callback, / (sets the csrf-token cookie),
  GET/POST /users/{id}/parental (UserParentalForm fragment, revision checked)
  and POST /media/media-movies/item/{id}/content-rating, both behind the
  double-submit CSRF check (cookie csrf-token == X-CSRF-Token);
- BFF: /healthz, POST /api/tv/login, /api/movies, /api/movies/{id},
  /api/playback/resolve, /stream/movies/{id}, the operator routes and C-DENY
  routes, evaluating ADR-0031 §2.6 for restricted policies.

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

USERS = {"admin": {"id": "u-admin", "password": "admin-pass-123", "roles": ["admin"]}}
AUTH_TOKENS = {}  # auth-local bearer -> uid
CODES = {}  # login code -> uid
ADMIN_SESSIONS = {}  # admin-ui session cookie -> uid
BFF = {}  # BFF session token -> uid
POLICIES = {"u-admin": (1, {"version": 1, "mode": "unrestricted", "rules": None})}  # uid -> (revision, policy)
MOVIES = {
    MOVIE_ID: {"id": MOVIE_ID, "tmdb_id": 550, "title": TITLE, "rating": "", "source": "", "changed": 0.0},
    "mv_other": {"id": "mv_other", "tmdb_id": 680, "title": "Pulp Fiction", "rating": "", "source": "", "changed": 0.0},
}
FLAGS = {}
BLOBS = {}  # uid -> userdata blob written through PUT /api/userdata
HISTORY = {}  # uid -> policies in write order (policy_stale serves the first)
PAUSED = [0.0]  # time userdata-local was "paused" (0 = running)
LOG = {"csrf_posts": [], "kid_requests": [], "module_calls": [], "passwords": [], "secrets": [], "policy_puts": 0}


def user_by_id(uid):
    return next((u for u in USERS.values() if u["id"] == uid), None)


def name_by_id(uid):
    return next((k for k, v in USERS.items() if v["id"] == uid), None)


def new_secret(prefix):
    s = prefix + secrets.token_hex(8)
    LOG["secrets"].append(s)
    return s


def rating_state(m, play=False):
    """Classification as seen by the gate; C-PLAY lookups see the previous value
    for FLAGS['play_delay'] seconds after a change (the ≤30 s cache)."""
    if play and time.time() - m["changed"] < float(FLAGS.get("play_delay") or 0):
        return m.get("prev", "")
    return m["rating"]


def allowed(uid, m, play=False):
    """None = allowed, else the parental code."""
    if uid not in POLICIES:
        return "parental.policy_unconfigured"
    _, pol = POLICIES[uid]
    if FLAGS.get("policy_stale") and HISTORY.get(uid):
        pol = HISTORY[uid][0]
    if FLAGS.get("blob_authority") and ((BLOBS.get(uid) or {}).get("prefs") or {}).get("parental", {}).get("mode") == "unrestricted":
        return None
    if pol["mode"] == "unrestricted":
        return None
    r = pol["rules"]
    rating = rating_state(m, play)
    if rating == "":
        return None if FLAGS.get("unavailable_visible") else "parental.blocked"
    if rating == "NR":
        return None if (r["allow_unrated"] or FLAGS.get("nr_allowed")) else "parental.blocked"
    ceiling = r["max_rating"] or ("PG" if r["kids_mode"] else "")
    if ceiling and LADDER.index(rating) > LADDER.index(ceiling):
        return "parental.blocked"
    return None


def restricted(uid):
    return uid in POLICIES and POLICIES[uid][1]["mode"] == "restricted"


def privileged(uid):
    u = user_by_id(uid)
    return bool(u) and ("admin" in u["roles"] or "manager" in u["roles"])


def parental_fragment(uid, saved=False, conflict=False):
    rev, pol = POLICIES.get(uid, (0, None))
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
        + '<select name="max_rating"><option value="">none</option>%s</select>' % opts
        + '<input name="blocked_tags" value="%s"/>' % ", ".join(r.get("blocked_tags") or [])
        + '<input name="allowed_tags" value="%s"/>' % ", ".join(r.get("allowed_tags") or [])
        + '<input type="checkbox" name="allow_unrated" value="1"%s/>' % (" checked" if r.get("allow_unrated") else "")
        + "</form></div>"
    )


def movie_json(m):
    return {"id": m["id"], "tmdb_id": m["tmdb_id"], "title": m["title"], "has_file": True,
            "content_rating": m["rating"], "stream_url": "/stream/movies/" + quote(m["id"], safe=":")}


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

    def deny(self, code, pcode, alias=""):
        body = {"error": "denied", "code": alias or pcode}
        if alias:
            body["parental_code"] = pcode
        if FLAGS.get("title_leak"):
            body["error"] = "%s is not allowed" % TITLE
        return self.send(403 if pcode != "parental.classification_unavailable" else 503, body)

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
        path, q = u.path, parse_qs(u.query)
        if path == "/_control":
            if self.headers.get("X-Reset"):
                FLAGS.clear()
                MOVIES.setdefault(MOVIE_ID, {"id": MOVIE_ID, "tmdb_id": 550, "title": TITLE, "rating": "", "source": "", "changed": 0.0})
            FLAGS.update(json.loads(self.body() or b"{}"))
            if "pause" in FLAGS:
                PAUSED[0] = time.time() if FLAGS.pop("pause") else 0.0
                LOG.setdefault("pause_calls", []).append(PAUSED[0] != 0.0)
            if FLAGS.pop("leftover_kid", False):
                USERS["smoke-kid"] = {"id": "u-leftover", "password": "old-pass-xyz", "roles": ["viewer"]}
                POLICIES["u-leftover"] = (3, {"version": 1, "mode": "unrestricted", "rules": None})
            if "set_rating" in FLAGS:
                MOVIES[MOVIE_ID].update(rating=FLAGS.pop("set_rating"), changed=0.0)
            return self.send(200, {})
        if path == "/_log":
            return self.send(200, dict(LOG, paused=PAUSED[0] != 0.0, users=sorted(USERS), policies={k: v for k, v in POLICIES.items()},
                                       rating=MOVIES.get(MOVIE_ID, {}).get("rating", ""), movies=sorted(MOVIES)))
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
            if not usr or usr["password"] != d.get("password"):
                return self.send(401, {"code": "auth.invalid_credentials"})
            tok = new_secret("bff-")
            BFF[tok] = usr["id"]
            return self.send(200, {"session_token": tok, "user_id": usr["id"]})
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
                POLICIES[target] = (rev + 1, {"version": 1, "mode": "unrestricted", "rules": None})
                return self.send(200, parental_fragment(target, conflict=True), "text/html")
            if int(g("expected_revision") or -1) != rev:
                return self.send(200, parental_fragment(target, conflict=True), "text/html")
            pol = {"version": 1, "mode": g("mode"), "rules": {
                "kids_mode": g("kids_mode") == "1", "max_rating": g("max_rating"),
                "blocked_tags": [t.strip() for t in g("blocked_tags").split(",") if t.strip()],
                "allowed_tags": [t.strip() for t in g("allowed_tags").split(",") if t.strip()],
                "allow_unrated": g("allow_unrated") == "1" or bool(FLAGS.get("store_allow_unrated"))}}
            LOG["policy_puts"] += 1
            if not FLAGS.get("policy_not_saved"):
                POLICIES[target] = (rev + 1, pol)
                HISTORY.setdefault(target, []).append(pol)
            frag = parental_fragment(target, saved=True)
            if FLAGS.get("policy_not_saved"):
                frag = frag.replace('data-state="unconfigured" data-mode=""', 'data-state="configured" data-mode="restricted"').replace(
                    'name="expected_revision" value="%d"' % rev, 'name="expected_revision" value="%d"' % (rev + 1))
            return self.send(200, frag, "text/html")
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
            new = {"clear": "", "unrated": "NR"}.get(choice, choice)
            if FLAGS.get("rating_unconfirmed"):
                return self.send(502, "The save was acknowledged, but its current classification could not be confirmed.", "text/html")
            m.update(prev=m["rating"], rating=new, source="operator" if new else "", changed=time.time())
            rec = "<p>Recorded value: <strong>%s</strong></p>" % html.escape(new) if new else ""
            return self.send(200, "<p role=status>Content rating saved and checked.</p>" + rec, "text/html")
        return self.send(404)

    def bff(self, method, path, q):
        tok = self.bearer()
        uid = BFF.get(tok or "")
        if not uid:
            return self.send(401, {"code": "auth.session_required"})
        if path == "/api/userdata" and method == "PUT":
            BLOBS[uid] = json.loads(self.body() or b"{}")
            return self.send(200, BLOBS[uid])
        if PAUSED[0] and time.time() - PAUSED[0] > 0.3 and not FLAGS.get("outage_open") and path != "/api/userdata":
            if FLAGS.get("outage_stream_open") and path.startswith("/stream/"):
                return self.send(206, raw=b"\x00" * 1024, ctype="video/mp4")
            if True:
                return self.send(503, {"error": "policy unavailable", "code": "parental.policy_unavailable"})
        if uid != "u-admin":
            LOG["kid_requests"].append({"method": method, "path": path, "auth": len(self.headers.get_all("Authorization") or [])})
        operator = (method == "DELETE" and path.startswith("/api/movies/")) or path == "/api/releases/grab" or (
            path.startswith("/api/sessions/") and path.endswith("/stop"))
        if operator:
            if privileged(uid) or FLAGS.get("rbac_open"):
                LOG["module_calls"].append(path)
                if method == "DELETE":
                    MOVIES.pop(unquote(path.rsplit("/", 1)[1]), None)
                return self.send(200, {"ok": True})
            return self.send(403, {"error": "forbidden", "code": FLAGS.get("rbac_code") or "operator.forbidden"})
        if path == "/api/search" or path.startswith("/api/discover/"):
            if restricted(uid) and not FLAGS.get("cdeny_open"):
                return self.send(403, {"error": "restricted", "code": FLAGS.get("cdeny_code") or "parental.restricted_route"})
            return self.send(200, {"results": [{"title": TITLE}]})
        if path == "/api/movies":
            if FLAGS.get("list_503"):
                return self.send(503, {"code": "parental.classification_unavailable"})
            items, hidden = [], 0
            for m in MOVIES.values():
                if allowed(uid, m) is None or (FLAGS.get("list_leak") and m["id"] == MOVIE_ID):
                    items.append(movie_json(m))
                else:
                    hidden += 1
            total = len(items) + (hidden if FLAGS.get("total_leak") else 0)
            return self.send(200, {"items": items, "total": total, "page": 1, "page_size": int((q.get("page_size") or ["48"])[0])})
        if path.startswith("/api/movies/"):
            m = MOVIES.get(unquote(path[len("/api/movies/"):]))
            if not m:
                return self.send(404, {"code": "movies.not_found"})
            why = allowed(uid, m)
            if why and not FLAGS.get("detail_open"):
                return self.deny(403, FLAGS.get("detail_code") or why)
            return self.send(200, movie_json(m))
        if path == "/api/playback/resolve":
            src = (q.get("src") or [""])[0]
            m = MOVIES.get(unquote(src.rsplit("/", 1)[-1]))
            if not m:
                return self.send(404, {"code": "playback.not_found"})
            why = allowed(uid, m, play=True)
            if why and not FLAGS.get("resolve_open"):
                return self.deny(403, why, "" if FLAGS.get("resolve_no_alias") else "playback.parental_blocked")
            return self.send(200, {"stream_url": src, "mode": "direct"})
        if path.startswith("/stream/movies/"):
            m = MOVIES.get(unquote(path[len("/stream/movies/"):]))
            if not m:
                return self.send(404)
            why = allowed(uid, m, play=True)
            if FLAGS.get("admin_stream_denied") and uid == "u-admin":
                why = "parental.blocked"
            if FLAGS.get("allowed_stream_denied") and not why and uid != "u-admin":
                why = "parental.blocked"
            if why and not FLAGS.get("stream_open"):
                return self.deny(403, why)
            return self.send(206, raw=b"\x00" * 1024, ctype="video/mp4", headers=[("Content-Range", "bytes 0-1023/4096")])
        return self.send(404)


if __name__ == "__main__":
    srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
    print(srv.server_address[1], flush=True)
    srv.serve_forever()
