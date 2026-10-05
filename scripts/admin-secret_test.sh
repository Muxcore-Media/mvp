#!/usr/bin/env bash
# Offline tests for scripts/lib/admin-secret.sh (FR-INS-004: no default admin password).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
source "$ROOT/scripts/lib/admin-secret.sh"
fail() { echo "FAIL: $*" >&2; exit 1; }
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
unset MVP_ADMIN_PASSWORD MVP_ADMIN_PASSWORD_FILE

# load never generates and leaves the variable unset
mvp_admin_password_load "$tmp"
[[ -z "${MVP_ADMIN_PASSWORD:-}" ]] || fail "load must not invent a password"
[[ ! -e "$tmp/data/auth/admin.password" ]] || fail "load must not create the file"
(mvp_require_admin_password "$tmp") 2>/dev/null && fail "require must fail with no secret"

# first ensure generates, prints once, 0600
out="$(mvp_admin_password_ensure "$tmp" 2>&1 >/dev/null)"
unset MVP_ADMIN_PASSWORD
f="$tmp/data/auth/admin.password"
[[ -s "$f" ]] || fail "file not created"
[[ "$(stat -c %a "$f")" == "600" ]] || fail "file mode must be 0600"
pw="$(tr -d '[:space:]' <"$f")"
[[ ${#pw} -ge 32 ]] || fail "password too short: ${#pw}"
[[ "$pw" != "admin-dev-only" ]] || fail "default literal generated"
[[ "$out" == *"$pw"* ]] || fail "first run must print the password"

# second ensure prints only the path, never the value
out2="$(mvp_admin_password_ensure "$tmp" 2>&1 >/dev/null)"
[[ "$out2" != *"$pw"* ]] || fail "second run must not print the password"
[[ "$out2" == *"$f"* ]] || fail "second run must print the file path"
[[ "$(tr -d '[:space:]' <"$f")" == "$pw" ]] || fail "password changed between runs"
unset MVP_ADMIN_PASSWORD

# env wins over the file
MVP_ADMIN_PASSWORD=operator-supplied mvp_admin_password_ensure "$tmp" 2>/dev/null
echo "OK admin-secret script tests"
