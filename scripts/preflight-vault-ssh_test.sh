#!/usr/bin/env bash
# Offline tests for preflight-vault-ssh.sh
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREFLIGHT="$SCRIPT_DIR/preflight-vault-ssh.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

[[ -x "$PREFLIGHT" ]] || fail "missing $PREFLIGHT"

mock_bin="$TMP/bin"
mkdir -p "$mock_bin"
cat >"$mock_bin/ssh" <<'EOF'
#!/usr/bin/env bash
echo ok
exit 0
EOF
chmod +x "$mock_bin/ssh"

PATH="$mock_bin:$PATH" MVP_DEPLOY_SSH=mock@host "$PREFLIGHT" | grep -q 'OK mock@host' || fail "expected OK"

cat >"$mock_bin/ssh" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
chmod +x "$mock_bin/ssh"
if PATH="$mock_bin:$PATH" MVP_DEPLOY_SSH=mock@host "$PREFLIGHT" 2>/dev/null; then
  fail "expected failure when ssh fails"
fi

"$PREFLIGHT" --help >/dev/null || fail "--help failed"

echo "ok preflight-vault-ssh tests"
