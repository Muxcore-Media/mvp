"""Fake auth-local + userdata-local policy provider + BFF stream gate for
scripts/parental-smoke_test.sh. Mirrors the ADR-0030 provider contract
(userdata-local v0.1.5 internal/server/parental.go) closely enough to exercise
the smoke seeding: bearer + X-MuxCore-User-Id selector, admin-only PUT,
strict {expected_revision, policy} body, revision conflicts (409)."""
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

USERS = {"admin": {"id": "u-admin", "password": "admin-pass", "roles": ["admin"]}}
SESSIONS = {}  # auth-local token -> user id
BFF = {}  # BFF session -> user id
POLICIES = {}  # user id -> (revision, policy dict)
LOG = []
FLAGS = {}
SEQ = [0]


def user_by_id(uid):
    return next((u for u in USERS.values() if u["id"] == uid), None)


class H(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def send(self, code, obj=None):
        body = b"" if obj is None else json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(n) if n else b""

    def caller(self):
        auth = self.headers.get_all("Authorization") or []
        if len(auth) != 1 or not auth[0].startswith("Bearer "):
            return None
        return SESSIONS.get(auth[0][7:])

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
        path = u.path
        if path == "/_control":
            FLAGS.update(json.loads(self.body() or b"{}"))
            if "restrict" in FLAGS:
                POLICIES[FLAGS.pop("restrict")] = (4, {"version": 1, "mode": "restricted", "rules": {"kids_mode": True, "max_rating": "", "blocked_tags": [], "allowed_tags": [], "allow_unrated": False}})
            return self.send(200, {})
        if path == "/_log":
            return self.send(200, {"log": LOG, "policies": {k: v[0] for k, v in POLICIES.items()}, "users": sorted(USERS)})
        if path == "/login/device" and method == "POST":
            d = json.loads(self.body())
            usr = USERS.get(d.get("username"))
            if not usr or usr["password"] != d.get("password"):
                return self.send(401)
            SEQ[0] += 1
            tok = "tok-%s-%d" % (usr["id"], SEQ[0])
            SESSIONS[tok] = usr["id"]
            return self.send(200, {"token": tok, "user_id": usr["id"], "username": d["username"], "tenant_id": ""})
        if path.startswith("/api/users"):
            uid = self.caller()
            if not uid or "admin" not in user_by_id(uid)["roles"]:
                return self.send(403)
            if path == "/api/users" and method == "GET":
                return self.send(200, {"users": [{"id": v["id"], "username": k} for k, v in USERS.items()]})
            if path == "/api/users" and method == "POST":
                d = json.loads(self.body())
                if d["username"] in USERS:
                    return self.send(400, {"error": "UNIQUE constraint failed"})
                SEQ[0] += 1
                USERS[d["username"]] = {"id": "u-%d" % SEQ[0], "password": d["password"], "roles": [d.get("role", "user")]}
                return self.send(201, {"user": {"id": USERS[d["username"]]["id"], "username": d["username"]}})
            if method == "DELETE":
                target = path.rsplit("/", 1)[1]
                name = next((k for k, v in USERS.items() if v["id"] == target), None)
                if not name:
                    return self.send(404)
                del USERS[name]
                return self.send(200, {"removed": True, "id": target})
            return self.send(405)
        if path == "/api/parental-policy":
            return self.policy(method, u)
        if path == "/api/tv/login" and method == "POST":
            d = json.loads(self.body())
            usr = USERS.get(d.get("username"))
            if not usr or usr["password"] != d.get("password"):
                return self.send(401, {"code": "auth.invalid_credentials"})
            SEQ[0] += 1
            BFF["bff-%d" % SEQ[0]] = usr["id"]
            return self.send(200, {"session_token": "bff-%d" % SEQ[0], "user_id": usr["id"]})
        if path.startswith("/stream/movies/"):
            auth = self.headers.get("Authorization", "")
            uid = BFF.get(auth[7:]) if auth.startswith("Bearer ") else None
            if not uid:
                return self.send(401)
            if FLAGS.get("bff_ungated") or uid in POLICIES:
                return self.send(206, None)
            return self.send(403, {"error": "denied", "code": FLAGS.get("bff_code") or "parental.policy_unconfigured"})
        self.send(404)

    def policy(self, method, u):
        entry = {"method": method, "query": u.query, "auth": self.headers.get_all("Authorization") or [],
                 "target": self.headers.get_all("X-MuxCore-User-Id") or []}
        raw = self.body()
        if method == "PUT":
            entry["body"] = raw.decode()
        LOG.append(entry)
        if FLAGS.get("provider_404"):
            return self.send(404)
        uid = self.caller()
        if not uid:
            return self.send(401, {"code": "policy.unauthenticated"})
        if u.query:
            return self.send(400, {"code": "policy.invalid_query"})
        targets = entry["target"]
        if len(targets) != 1:
            return self.send(400, {"code": "policy.invalid_target"})
        target, admin = targets[0], "admin" in user_by_id(uid)["roles"]
        if (method == "PUT" and not admin) or (target != uid and not admin):
            return self.send(403, {"code": "policy.forbidden"})
        if not user_by_id(target):
            return self.send(404, {"code": "policy.account_not_found"})
        if method == "PUT":
            d = json.loads(raw)
            pol = d.get("policy") or {}
            if set(d) != {"expected_revision", "policy"} or set(pol) != {"version", "mode", "rules"} or pol["mode"] != "unrestricted" or pol["rules"] is not None:
                return self.send(400, {"code": "policy.invalid_body"})
            if FLAGS.pop("conflict_once", False):
                POLICIES[target] = (1, {"version": 1, "mode": "unrestricted", "rules": None})
                return self.send(409, {"code": "policy.revision_conflict"})
            cur = POLICIES.get(target, (0, None))[0]
            if d["expected_revision"] != cur:
                return self.send(409, {"code": "policy.revision_conflict"})
            POLICIES[target] = (cur + 1, pol)
        if target in POLICIES:
            rev, pol = POLICIES[target]
            return self.send(200, {"user_id": target, "tenant_id": "", "state": "configured", "revision": rev, "policy": pol, "updated_at": "2026-10-08T00:00:00Z"})
        return self.send(200, {"user_id": target, "tenant_id": "", "state": "unconfigured", "revision": 0, "policy": None})


if __name__ == "__main__":
    srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
    print(srv.server_address[1], flush=True)
    srv.serve_forever()
