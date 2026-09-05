#!/usr/bin/env bash
# Offline smoke for ci-checkout-self-hosted.sh
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

fail() { echo "FAIL: $*" >&2; exit 1; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

if GITHUB_WORKSPACE="$TMP/ws" GITHUB_REPOSITORY= GITHUB_SHA= \
  bash "$SCRIPT_DIR/ci-checkout-self-hosted.sh" 2>"$TMP/err"; then
  fail "should require GITHUB_REPOSITORY"
fi
grep -q GITHUB_REPOSITORY "$TMP/err" || fail "missing env error: $(cat "$TMP/err")"

mkdir -p "$TMP/ws"
if GITHUB_WORKSPACE="$TMP/ws" GITHUB_REPOSITORY=Muxcore-Media/demo GITHUB_SHA= \
  bash "$SCRIPT_DIR/ci-checkout-self-hosted.sh" 2>"$TMP/err2"; then
  fail "should require GITHUB_SHA"
fi
grep -q GITHUB_SHA "$TMP/err2" || fail "missing sha error: $(cat "$TMP/err2")"

echo "ok ci-checkout-self-hosted smoke"
