#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

[[ -x "$ROOT/scripts/smoke-registry.sh" ]] || fail "missing smoke-registry.sh"
[[ -f "$ROOT/scripts/lib/registry-smoke.sh" ]] || fail "missing registry-smoke lib"
[[ -f "$ROOT/scripts/lib/smoke-cmd.sh" ]] || fail "missing smoke-cmd lib"

grep -q 'AUTH_BOOTSTRAP_USER' "$ROOT/docker-compose.registry.yml" || fail "compose missing AUTH_BOOTSTRAP_USER"
grep -q 'AUTH_BOOTSTRAP_PASSWORD' "$ROOT/docker-compose.registry.yml" || fail "compose missing AUTH_BOOTSTRAP_PASSWORD"
grep -q 'smoke-registry.sh' "$ROOT/docs/PUBLIC-INSTALL.md" || fail "PUBLIC-INSTALL missing smoke-registry.sh"
grep -q 'MUXCORE_SMOKE_REGISTRY' "$ROOT/smoke.sh" || fail "smoke.sh missing registry hook"

# shellcheck disable=SC1091
source "$ROOT/scripts/lib/registry-smoke.sh"
registry_smoke_root="$ROOT"
MUXCORE_SMOKE_REGISTRY=0
registry_smoke_should_use && fail "should_use should be false without compose stack"

echo "OK smoke-registry script tests"
