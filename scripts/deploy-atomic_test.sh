#!/usr/bin/env bash
# Offline tests for deploy-atomic.sh (flock, .prev, rollback, health).
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ATOMIC="$SCRIPT_DIR/deploy-atomic.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

[[ -x "$ATOMIC" ]] || fail "missing executable $ATOMIC"
# shellcheck disable=SC1091
source "$ATOMIC"

BIN="$TMP/bin"
RUN="$TMP/run"
MVP="$TMP/mvp"
mkdir -p "$BIN" "$RUN" "$MVP"
ln -s "$BIN" "$MVP/bin"
ln -s "$RUN" "$MVP/run"

cat >"$MVP/run-host.sh" <<'EOF'
#!/usr/bin/env bash
echo "run-host $*" >>"__RUN_LOG__"
exit 0
EOF
sed -i "s|__RUN_LOG__|$RUN/run-host.log|g" "$MVP/run-host.sh"
chmod +x "$MVP/run-host.sh"

echo 'old-binary' >"$BIN/admin-ui"
chmod +x "$BIN/admin-ui"
echo 'new-binary' >"$TMP/admin-ui.new"

atomic_bin_install "$BIN" admin-ui "$TMP/admin-ui.new" admin-ui "$MVP"
[[ -f "$BIN/admin-ui.prev" ]] || fail "missing .prev backup"
[[ "$(cat "$BIN/admin-ui")" == "new-binary" ]] || fail "binary not installed"
[[ ! -f "$TMP/admin-ui.new" ]] || fail ".new should be consumed"
grep -q 'stop-one admin-ui' "$RUN/run-host.log" || fail "stop-one not called"
grep -q 'restart admin-ui' "$RUN/run-host.log" || fail "restart not called"

echo 'newer-binary' >"$TMP/admin-ui.newer"
atomic_bin_install "$BIN" admin-ui "$TMP/admin-ui.newer" "" "$MVP"
[[ "$(cat "$BIN/admin-ui.prev")" == "new-binary" ]] || fail ".prev not updated on second install"

atomic_bin_rollback "$BIN" admin-ui admin-ui "$MVP"
[[ "$(cat "$BIN/admin-ui")" == "new-binary" ]] || fail "rollback did not restore .prev"

LOCK="$(deploy_lock_path "$MVP")"
echo 'blocked-install' >"$TMP/admin-ui.blocked"
flock -n "$LOCK" -c 'sleep 2' &
lock_pid=$!
sleep 0.2
if atomic_bin_install "$BIN" admin-ui "$TMP/admin-ui.blocked" "" "$MVP" 2>"$TMP/lock.err"; then
  kill "$lock_pid" 2>/dev/null || true
  fail "install should fail when lock held"
fi
kill "$lock_pid" 2>/dev/null || true
wait "$lock_pid" 2>/dev/null || true
grep -q 'another deploy in progress' "$TMP/lock.err" || fail "expected lock message: $(cat "$TMP/lock.err")"

mock_bin="$TMP/mockbin"
mkdir -p "$mock_bin"
cat >"$mock_bin/curl" <<'EOF'
#!/usr/bin/env bash
echo 200
EOF
chmod +x "$mock_bin/curl"
PATH="$mock_bin:$PATH" DEPLOY_ATOMIC_HEALTH_RETRIES=1 DEPLOY_ATOMIC_HEALTH_INTERVAL=0 \
  verify_module_health admin-ui "$MVP" || fail "admin-ui health should pass with mock curl"

echo 'muxcorectl' >"$BIN/muxcorectl"
chmod +x "$BIN/muxcorectl"
DEPLOY_ATOMIC_HEALTH_RETRIES=1 verify_module_health muxcorectl "$MVP" || fail "muxcorectl binary health"

echo "ok deploy-atomic tests"
