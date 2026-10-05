#!/usr/bin/env bash
# Bootstrap local admin user + session token for MVP smoke.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
# shellcheck disable=SC1091
if [[ -f "$ROOT/.env" ]]; then source "$ROOT/.env"; fi

# shellcheck disable=SC1091
source "$ROOT/scripts/lib/smoke-cmd.sh"
smoke_cmd_init

mvp_require_admin_password "$ROOT" || exit 1

if [[ "${MUXCORE_SMOKE_REGISTRY:-}" == "1" ]]; then
  registry_smoke_bootstrap_auth
  exit 0
fi

USER="${MVP_ADMIN_USER:-admin}"
PASS="$MVP_ADMIN_PASSWORD"
AUTH_ADDR="${AUTH_GRPC_ADDR:-127.0.0.1:9403}"
TOKEN_FILE="${MVP_TOKEN_FILE:-$ROOT/run/admin.token}"
BIN="$ROOT/bin"

mkdir -p "$ROOT/run"

if [[ ! -x "$BIN/authctl" ]]; then
  echo "==> building authctl"
  (cd "$ROOT/../auth-local" && go build -o "$BIN/authctl" ./cmd/authctl)
fi
if [[ ! -x "$BIN/gettoken" ]]; then
  echo "==> building gettoken"
  (cd "$ROOT" && go build -o "$BIN/gettoken" ./cmd/gettoken)
fi

# auth-local creates the admin (with role) from AUTH_BOOTSTRAP_USER/PASSWORD on an
# empty DB (run-host.sh and compose set them); once any user exists, authctl
# adduser needs an admin token. So try logging in first.
if "$BIN/gettoken" -addr "$AUTH_ADDR" -user "$USER" -password "$PASS" -out "$TOKEN_FILE" 2>/tmp/mvp-gettoken.err; then
  echo "token written to $TOKEN_FILE (existing user $USER)"
  exit 0
fi

echo "==> ensuring user $USER exists"
if ! "$BIN/authctl" -addr "$AUTH_ADDR" adduser "$USER" "$PASS" 2>/tmp/mvp-adduser.err; then
  if grep -qiE 'already|exists|unique|duplicate' /tmp/mvp-adduser.err; then
    echo "user already exists"
  else
    cat /tmp/mvp-adduser.err >&2
    exit 1
  fi
fi

echo "==> ensuring admin role"
"$BIN/authctl" -addr "$AUTH_ADDR" addrole "$USER" admin || true

echo "==> fetching session token"
"$BIN/gettoken" -addr "$AUTH_ADDR" -user "$USER" -password "$PASS" -out "$TOKEN_FILE"
echo "token written to $TOKEN_FILE"
