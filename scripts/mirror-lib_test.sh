#!/usr/bin/env bash
# Offline tests for mirror-lib name mappings.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/mirror-lib.sh"

fail() { echo "FAIL: $*" >&2; exit 1; }

[[ "$(forgejo_repo_name core)" == "core" ]] || fail "core mapping"
[[ "$(forgejo_repo_name core.wiki)" == "core-wiki" ]] || fail "wiki → forgejo"
[[ "$(forgejo_repo_name media-tvos-app)" == "muxcore-tvos" ]] || fail "tvos mapping"
[[ "$(github_repo_name core-wiki)" == "core.wiki" ]] || fail "wiki → github"
[[ "$(github_repo_name muxcore-tvos)" == "media-tvos-app" ]] || fail "tvos → github"

should_skip_repo claude-working-directory || fail "should skip claude dir"
should_skip_repo core && fail "should not skip core"

echo "ok mirror-lib tests"
