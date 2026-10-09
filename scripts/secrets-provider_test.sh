#!/usr/bin/env bash
# Exercise the host runner's actual provider selection and preflight functions.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib/secrets-provider.sh
source "$ROOT/scripts/lib/secrets-provider.sh"
tmp="$(mktemp -d)"
test_pid=""
cleanup() {
  [[ -z "$test_pid" ]] || kill "$test_pid" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT
BIN="$tmp/bin"
RUN="$tmp/run"
DATA="$tmp/data"
MESH=127.0.0.1:9090
mkdir -p "$BIN" "$RUN" "$DATA/secrets"
fail() { echo "FAIL: $*" >&2; exit 1; }
expect_failure() {
  local message="$1"; shift
  if "$@" >"$tmp/error" 2>&1; then fail "expected failure: $message"; fi
  grep -q "$message" "$tmp/error" || { cat "$tmp/error" >&2; fail "missing error: $message"; }
}
starts=()
maybe_start() { starts+=("$1"); printf '%s\n' "$@" >"$tmp/launch"; }

unset MVP_ENABLE_SECRETS_VAULT SECRETS_BACKEND SECRETS_GRPC_ADDR
mvp_secrets_preflight
mvp_start_secrets_provider
[[ "${starts[*]}" == secrets-file ]] || fail "default starts only file"
grep -Fxq "SECRETS_STORE=$DATA/secrets/store.json" "$tmp/launch" || fail "file store changed"
grep -Fxq "SECRETS_KEY_FILE=$DATA/secrets/master.key" "$tmp/launch" || fail "file key path changed"
expect_failure 'not selected' mvp_secrets_preflight secrets-vault

MVP_ENABLE_SECRETS_VAULT=1
expect_failure 'set SECRETS_BACKEND' mvp_secrets_preflight
SECRETS_BACKEND='   '
expect_failure 'set SECRETS_BACKEND' mvp_secrets_preflight
SECRETS_BACKEND=vault
mvp_secrets_preflight
mvp_secrets_preflight secrets-vault
starts=()
mvp_start_secrets_provider
[[ "${starts[*]}" == secrets-vault ]] || fail "vault selection must not also start file"
grep -Fxq 'MUXCORE_MODULE_ID=secrets-vault' "$tmp/launch" || fail "vault identity changed"
grep -Fxq 'SECRETS_GRPC_ADDR=:9551' "$tmp/launch" || fail "vault address changed"
expect_failure 'not selected' mvp_secrets_preflight secrets-file

# An unrelated live PID in a stale pidfile must not block selection.
printf '%s\n' "$$" >"$RUN/secrets-file.pid"
mvp_secrets_preflight
# The expected runner process blocks both full and single-provider starts.
# shellcheck disable=SC2329 # Called inside the sourced preflight function.
ps() { printf '%s\n' "$BIN/secrets-file"; }
expect_failure 'secrets-file is still running' mvp_secrets_preflight
expect_failure 'secrets-file is still running' mvp_secrets_preflight secrets-vault
mvp_secrets_preflight media-movies
unset -f ps
printf '%s\n' 'stale-not-a-pid' >"$RUN/secrets-file.pid"
mvp_secrets_preflight
MVP_ENABLE_SECRETS_VAULT=0
printf '%s\n' "$$" >"$RUN/secrets-vault.pid"
mvp_secrets_preflight
# shellcheck disable=SC2329 # Called inside the sourced preflight function.
ps() { printf '%s\n' "$BIN/secrets-vault"; }
expect_failure 'secrets-vault is still running' mvp_secrets_preflight
unset -f ps
printf '%s\n' 'stale-not-a-pid' >"$RUN/secrets-vault.pid"

# Lost pidfile: inspect literal runner paths, including spaces/regex characters.
BIN="$tmp/bin with [brackets]"
# shellcheck disable=SC2329 # Called inside the sourced preflight function.
ps() { printf '%s\n' "$BIN/secrets-vault"; }
expect_failure 'secrets-vault is still running' mvp_secrets_preflight
# shellcheck disable=SC2329 # Called inside the sourced preflight function.
ps() { printf '%s\n' '/another/checkout/secrets-vault'; }
mvp_secrets_preflight
unset -f ps

# Exercise the runner entry points as well: a rejected switch/restart must not
# stop even a test-owned process or proceed into module startup.
runner="$tmp/runner"
mkdir -p "$runner/scripts/lib" "$runner/run"
cp "$ROOT/run-host.sh" "$runner/"
cp "$ROOT/scripts/lib/admin-secret.sh" "$ROOT/scripts/lib/secrets-provider.sh" "$ROOT/scripts/lib/userdata-transport.sh" "$runner/scripts/lib/"
bash -c 'exec -a "$1" sleep 30' _ "$runner/bin/secrets-file" &
test_pid=$!
for _ in $(seq 1 30); do
  [[ "$(ps -p "$test_pid" -o args=)" == "$runner/bin/secrets-file "* ]] && break
  sleep 0.01
done
[[ "$(ps -p "$test_pid" -o args=)" == "$runner/bin/secrets-file "* ]] || fail "test provider process failed to start"
printf '%s\n' "$test_pid" >"$runner/run/secrets-file.pid"
expect_failure 'secrets-file is still running' env MVP_ENABLE_SECRETS_VAULT=1 SECRETS_BACKEND=vault START_ONLY= bash "$runner/run-host.sh" up
kill -0 "$test_pid" || fail "rejected up stopped the old provider"
expect_failure 'not selected' env MVP_ENABLE_SECRETS_VAULT=1 SECRETS_BACKEND=vault bash "$runner/run-host.sh" restart secrets-file
kill -0 "$test_pid" || fail "rejected restart stopped the old provider"
expect_failure 'set SECRETS_BACKEND' env MVP_ENABLE_SECRETS_VAULT=1 SECRETS_BACKEND= START_ONLY= bash "$runner/run-host.sh" up
kill -0 "$test_pid" || fail "missing backend stopped the old provider"

# A reused PID is safe throughout the actual stop paths, not just preflight.
kill "$test_pid"
wait "$test_pid" 2>/dev/null || true
sleep 30 &
test_pid=$!
printf '%s\n' "$test_pid" >"$runner/run/secrets-file.pid"
bash "$runner/run-host.sh" stop >"$tmp/stop-output" 2>&1
kill -0 "$test_pid" || fail "stop killed an unrelated process from a stale provider pidfile"
printf '#!/usr/bin/env bash\nexit 0\n' >"$runner/bin/unregistermodule"
chmod +x "$runner/bin/unregistermodule"
printf '%s\n' "$test_pid" >"$runner/run/secrets-vault.pid"
bash "$runner/run-host.sh" stop-one secrets-vault >"$tmp/stop-output" 2>&1
kill -0 "$test_pid" || fail "stop-one killed an unrelated process from a stale provider pidfile"
printf '%s\n' "$test_pid" >"$runner/run/secrets-file.pid"
# No core checkout/binary exists in this fixture: full up must get through its
# stop phase safely, then fail before any module can start.
expect_failure 'core: No such file or directory' env MVP_ENABLE_SECRETS_VAULT=1 SECRETS_BACKEND=vault START_ONLY= bash "$runner/run-host.sh" up
kill -0 "$test_pid" || fail "up killed an unrelated process from a stale provider pidfile"

echo 'OK: host provider defaults, opt-in, config rejection and live-process conflicts'
