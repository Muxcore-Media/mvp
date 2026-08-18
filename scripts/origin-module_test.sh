#!/usr/bin/env bash
# Fixture tests for origin-module pin/refuse.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/origin-module.sh"

fail() { echo "FAIL: $*" >&2; exit 1; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

init_repo() {
  local dir="$1" ver="$2" go_ver="${3:-$2}"
  mkdir -p "$dir/internal"
  cat >"$dir/muxcore.json" <<EOF
{"name":"T","version":"$ver"}
EOF
  cat >"$dir/internal/module.go" <<EOF
package internal
func Info() { _ = struct{ Version string }{Version: "$go_ver"} }
EOF
  git -C "$dir" init -q
  git -C "$dir" config user.email test@example.com
  git -C "$dir" config user.name test
  git -C "$dir" add muxcore.json internal/module.go
  git -C "$dir" commit -qm "init $ver"
}

# origin/main 0.1.0, HEAD matches → ok
A="$TMP/ok"
init_repo "$A" "0.1.0"
git -C "$A" branch -M main
git -C "$A" remote add origin "$A"
git -C "$A" update-ref refs/remotes/origin/main HEAD
got="$(assert_module_on_origin "$A")"
ver="${got%%$'\t'*}"
[[ "$ver" == "0.1.0" ]] || fail "expected 0.1.0 got $got"

# unpublished local muxcore.json version
B="$TMP/unpublished"
init_repo "$B" "0.2.5"
git -C "$B" branch -M main
git -C "$B" remote add origin "$B"
git -C "$B" update-ref refs/remotes/origin/main HEAD
cat >"$B/muxcore.json" <<'EOF'
{"name":"T","version":"0.2.6"}
EOF
if assert_module_on_origin "$B" >/dev/null 2>"$TMP/err"; then
  fail "should refuse dirty unpublished 0.2.6"
fi
grep -q "unpublished version 0.2.6" "$TMP/err" || grep -q "dirty" "$TMP/err" || fail "refuse message: $(cat "$TMP/err")"

# committed but unpushed HEAD
C="$TMP/unpushed"
init_repo "$C" "0.2.5"
git -C "$C" branch -M main
git -C "$C" remote add origin "$C"
git -C "$C" update-ref refs/remotes/origin/main HEAD
cat >"$C/muxcore.json" <<'EOF'
{"name":"T","version":"0.2.6"}
EOF
cat >"$C/internal/module.go" <<'EOF'
package internal
func Info() { _ = struct{ Version string }{Version: "0.2.6"} }
EOF
git -C "$C" add muxcore.json internal/module.go
git -C "$C" commit -qm "bump unpublished"
if assert_module_on_origin "$C" >/dev/null 2>"$TMP/err2"; then
  fail "should refuse unpushed HEAD"
fi
grep -q "is not origin/main" "$TMP/err2" || fail "unpushed message: $(cat "$TMP/err2")"

# explicit want version not on origin
if assert_module_on_origin "$A" "0.2.6" >/dev/null 2>"$TMP/err3"; then
  fail "should refuse requested 0.2.6"
fi
grep -q "refuse unpublished version 0.2.6" "$TMP/err3" || fail "want-version message: $(cat "$TMP/err3")"

# muxcore.json vs Info() mismatch on origin
D="$TMP/mismatch"
init_repo "$D" "0.1.0" "0.9.9"
git -C "$D" branch -M main
git -C "$D" remote add origin "$D"
git -C "$D" update-ref refs/remotes/origin/main HEAD
if assert_module_on_origin "$D" >/dev/null 2>"$TMP/err4"; then
  fail "should refuse Info() mismatch"
fi
grep -q "Info()" "$TMP/err4" || fail "mismatch message: $(cat "$TMP/err4")"

echo "ok origin-module tests"
