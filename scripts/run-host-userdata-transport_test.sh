#!/usr/bin/env bash
# Offline: run-host.sh refuses the userdata transport in secure host modes
# (ADR-0033; scripts/lib/userdata-transport.sh) before stopping or starting
# anything, and leaves explicit insecure dev alone.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

# shellcheck disable=SC1091
source "$ROOT/scripts/lib/userdata-transport.sh"

clean_env() { env -u MUXCORE_INSECURE_DISABLE_TLS -u MUXCORE_PROFILE -u MUXCORE_REQUIRE_TLS -u MVP_ENABLE_USERDATA_LOCAL -u MVP_ENABLE_MEDIA_UI -u START_ONLY "$@"; }
# pre VAR=value… — the preflight in a clean subshell with exactly these settings.
pre() {
  (
    unset MUXCORE_INSECURE_DISABLE_TLS MUXCORE_PROFILE MUXCORE_REQUIRE_TLS MVP_ENABLE_USERDATA_LOCAL MVP_ENABLE_MEDIA_UI START_ONLY
    for kv in "$@"; do export "${kv?}"; done
    mvp_userdata_transport_preflight "${START_ONLY:-}"
  )
}

# Explicit insecure dev: always allowed.
pre MUXCORE_INSECURE_DISABLE_TLS=true 2>/dev/null || fail "insecure dev refused"
pre MUXCORE_INSECURE_DISABLE_TLS=1 MUXCORE_PROFILE=dev 2>/dev/null || fail "insecure dev (1) refused"
# Secure host modes: refused while userdata-local or media-ui would start.
for mode in "MUXCORE_PROFILE=staging" "MUXCORE_REQUIRE_TLS=1" "MUXCORE_PROFILE=household" "MUXCORE_INSECURE_DISABLE_TLS=TRUE"; do
  if pre "$mode" 2>"${TMPDIR:-/tmp}/ud-pre.err"; then fail "$mode: userdata transport not refused"; fi
  grep -q "ADR-0033" "${TMPDIR:-/tmp}/ud-pre.err" || fail "$mode: message lacks ADR-0033"
  grep -q "userdata-local media-ui" "${TMPDIR:-/tmp}/ud-pre.err" || fail "$mode: message lacks the refused services"
done
pre MUXCORE_REQUIRE_TLS=1 START_ONLY=userdata-local 2>/dev/null && fail "restart userdata-local in secure mode accepted"
pre MUXCORE_REQUIRE_TLS=1 START_ONLY=media-ui 2>/dev/null && fail "restart media-ui in secure mode accepted"
pre MUXCORE_REQUIRE_TLS=1 START_ONLY=auth-local 2>/dev/null || fail "restart of an unrelated service refused"
pre MUXCORE_REQUIRE_TLS=1 MVP_ENABLE_USERDATA_LOCAL=0 MVP_ENABLE_MEDIA_UI=0 2>/dev/null || fail "opt-out of both services refused"
pre MUXCORE_REQUIRE_TLS=1 MVP_ENABLE_USERDATA_LOCAL=0 2>/dev/null && fail "media-ui alone in secure mode accepted"
echo "OK preflight: insecure dev allowed; staging/REQUIRE_TLS/household refused for userdata-local and media-ui"

# End to end: `up` in secure host mode fails before stopping, building or
# starting anything (sandboxed copy of the runner).
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
runner="$tmp/runner"
mkdir -p "$runner/scripts/lib"
cp "$ROOT/run-host.sh" "$runner/"
cp "$ROOT/scripts/lib/admin-secret.sh" "$ROOT/scripts/lib/secrets-provider.sh" "$ROOT/scripts/lib/userdata-transport.sh" "$runner/scripts/lib/"
for mode in "MUXCORE_REQUIRE_TLS=1" "MUXCORE_PROFILE=staging"; do
  if clean_env env "$mode" bash "$runner/run-host.sh" up >"$tmp/out" 2>&1; then
    fail "run-host.sh up ($mode) started"
  fi
  grep -q "unsupported in secure host mode" "$tmp/out" || fail "up ($mode) message: $(cat "$tmp/out")"
  if compgen -G "$runner/run/*.pid" >/dev/null || compgen -G "$runner/bin/*" >/dev/null; then
    fail "up ($mode) started or built something before refusing"
  fi
  grep -q "^starting \|^building " "$tmp/out" && fail "up ($mode) proceeded: $(cat "$tmp/out")"
done
echo "OK run-host.sh up refuses secure host mode before starting anything"
echo "OK run-host-userdata-transport"
