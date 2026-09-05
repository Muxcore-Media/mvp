#!/usr/bin/env bash
# Offline tests for install-origin-module.sh atomic install path.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

MVP_PARENT="$TMP/workspace"
MVP="$MVP_PARENT/_mvp"
MOD="$MVP_PARENT/mod"
INSTALL_SCRIPT="$MVP/scripts/install-origin-module.sh"

mkdir -p "$MOD/internal" "$MVP/bin" "$MVP/run" "$MVP/scripts"
cp "$SCRIPT_DIR/deploy-atomic.sh" "$MVP/scripts/"
cp "$SCRIPT_DIR/origin-module.sh" "$MVP/scripts/"
cp "$SCRIPT_DIR/install-origin-module.sh" "$MVP/scripts/"
chmod +x "$MVP/scripts/"*.sh

[[ -x "$INSTALL_SCRIPT" ]] || fail "missing executable $INSTALL_SCRIPT"

help_err="$(bash "$INSTALL_SCRIPT" --help 2>&1 || true)"
grep -q 'no-verify' <<<"$help_err" || fail "--help missing --no-verify"
grep -q 'atomic install' <<<"$help_err" || fail "--help missing atomic install note"

cat >"$MOD/muxcore.json" <<'EOF'
{"name":"mod","version":"0.1.0"}
EOF
cat >"$MOD/internal/module.go" <<'EOF'
package internal
func Info() { _ = struct{ Version string }{Version: "0.1.0"} }
EOF
git -C "$MOD" init -q
git -C "$MOD" config user.email test@example.com
git -C "$MOD" config user.name test
git -C "$MOD" add muxcore.json internal/module.go
git -C "$MOD" commit -qm init
git -C "$MOD" branch -M main
git -C "$MOD" remote add origin "$MOD"
git -C "$MOD" update-ref refs/remotes/origin/main HEAD

mock_bin="$TMP/mockbin"
mkdir -p "$mock_bin"
cat >"$mock_bin/go" <<'EOF'
#!/usr/bin/env bash
out=""
prev=""
for arg in "$@"; do
  if [[ "$prev" == -o ]]; then
    out="$arg"
  fi
  prev="$arg"
done
[[ -n "$out" ]] && echo 'built-mod' >"$out" && chmod +x "$out"
exit 0
EOF
chmod +x "$mock_bin/go"

PATH="$mock_bin:$PATH" \
  MVP_BIN="$MVP/bin" MVP_ORIGIN_RECORD="$MVP/run/ORIGIN-BINARIES.log" \
  bash "$INSTALL_SCRIPT" mod --dest "$MVP/bin" --build-only 2>"$TMP/build.err" || fail "build-only failed: $(cat "$TMP/build.err")"

echo 'existing' >"$MVP/bin/mod"
chmod +x "$MVP/bin/mod"

PATH="$mock_bin:$PATH" \
  MVP_BIN="$MVP/bin" MVP_ORIGIN_RECORD="$MVP/run/ORIGIN-BINARIES.log" \
  bash "$INSTALL_SCRIPT" mod --dest "$MVP/bin" --no-verify 2>"$TMP/install.err" || fail "install failed: $(cat "$TMP/install.err")"

[[ -f "$MVP/bin/mod.prev" ]] || fail "missing mod.prev after atomic install"
[[ -x "$MVP/bin/mod" ]] || fail "mod binary missing"
[[ ! -f "$MVP/bin/mod.new" ]] || fail "mod.new should be consumed"

echo "ok install-origin-module tests"
