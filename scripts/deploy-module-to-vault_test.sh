#!/usr/bin/env bash
# Smoke tests for deploy-module-to-vault.sh (no network).
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY="$SCRIPT_DIR/deploy-module-to-vault.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

[[ -x "$DEPLOY" ]] || fail "missing executable $DEPLOY"

if "$DEPLOY" 2>"$TMP/err" >/dev/null; then
  fail "expected usage error with no args"
fi
grep -q usage "$TMP/err" || fail "no usage message"

list_out="$("$DEPLOY" --list)"
grep -q 'media-scanner' <<<"$list_out" || fail "--list missing media-scanner"
grep -q 'media-ui-app' <<<"$list_out" || fail "--list missing media-ui-app"
grep -q 'admin-ui' <<<"$list_out" || fail "--list missing admin-ui"
grep -q 'Origin-pinned' <<<"$list_out" || fail "--list missing origin-pinned section"
grep -q 'smoke-vault-all' <<<"$list_out" || fail "--list missing smoke helpers"

help_err="$("$DEPLOY" --help 2>&1 >/dev/null || true)"
grep -q 'verify-all' <<<"$help_err" || fail "--help missing verify-all"

if "$DEPLOY" definitely-not-a-module --build-only 2>"$TMP/err" >/dev/null; then
  fail "expected error for unknown module"
fi
grep -q 'unknown module' "$TMP/err" || fail "unknown module error message"

echo "ok deploy-module-to-vault tests"
