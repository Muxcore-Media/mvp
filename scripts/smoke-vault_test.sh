#!/usr/bin/env bash
# Offline smoke tests for vault health/public curl scripts (mock SSH/curl when needed).
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

PUBLIC="$SCRIPT_DIR/smoke-vault-public.sh"
HEALTH="$SCRIPT_DIR/smoke-vault-health.sh"

[[ -x "$PUBLIC" ]] || fail "missing $PUBLIC"
[[ -x "$HEALTH" ]] || fail "missing $HEALTH"

# Public script: mock curl success paths
mock_bin="$TMP/bin"
mkdir -p "$mock_bin"
cat >"$mock_bin/curl" <<'EOF'
#!/usr/bin/env bash
case "$*" in
  *admin*) echo 200; exit 0 ;;
  *mux*) echo 303; exit 0 ;;
  *auth*) echo 200; exit 0 ;;
esac
echo 000; exit 1
EOF
chmod +x "$mock_bin/curl"
PATH="$mock_bin:$PATH" "$PUBLIC" || fail "public smoke with mock curl"
out=$(PATH="$mock_bin:$PATH" MUXCORE_PUBLIC_MUX=https://mux.example/ "$PUBLIC")
grep -q mux.example <<<"$out" || fail "custom MUX url"

# Health script: mock ssh (remote curl one-liner is the last argument)
cat >"$mock_bin/ssh" <<'EOF'
#!/usr/bin/env bash
remote="${*: -1}"
case "$remote" in
  *18080*) echo 401; exit 0 ;;
  *8082*) echo 200; exit 0 ;;
  *5173*) echo 303; exit 0 ;;
  *9401*) echo 200; exit 0 ;;
  *8080/health*) echo 200; exit 0 ;;
esac
echo 000; exit 1
EOF
chmod +x "$mock_bin/ssh"
PATH="$mock_bin:$PATH" MVP_DEPLOY_SSH=mock@host "$HEALTH" || fail "health smoke with mock ssh"

# smoke-vault-all: sibling mocks in a temp scripts dir (SCRIPT_DIR resolution)
ALL="$SCRIPT_DIR/smoke-vault-all.sh"
[[ -x "$ALL" ]] || fail "missing $ALL"
mock_scripts="$TMP/scripts"
mkdir -p "$mock_scripts"
cp "$ALL" "$mock_scripts/smoke-vault-all.sh"
cat >"$mock_scripts/smoke-vault-health.sh" <<'EOF'
#!/usr/bin/env bash
echo ok health
exit 0
EOF
cat >"$mock_scripts/smoke-vault-public.sh" <<'EOF'
#!/usr/bin/env bash
echo ok public
exit 0
EOF
cat >"$mock_scripts/muxcorectl-vault.sh" <<'EOF'
#!/usr/bin/env bash
echo "leader: test"
exit 0
EOF
chmod +x "$mock_scripts"/*.sh
"$mock_scripts/smoke-vault-all.sh" | grep -q 'smoke-vault-all: OK' || fail "smoke-vault-all mock run"

echo "ok smoke-vault script tests"
