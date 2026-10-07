#!/usr/bin/env bash
# Provider selection for run-host.sh. ROOT/BIN/RUN/DATA/MESH and maybe_start
# belong to the runner. Keep both providers' identities and data paths distinct.

mvp_secrets_provider() {
  if [[ "${MVP_ENABLE_SECRETS_VAULT:-0}" == "1" ]]; then
    echo secrets-vault
  else
    echo secrets-file
  fi
}

mvp_secrets_pid_matches() {
  local name="$1" pid="$2" args
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] && kill -0 "$pid" 2>/dev/null || return 1
  args="$(ps -p "$pid" -o args= 2>/dev/null)" || return 1
  [[ "$args" == "$BIN/$name" || "$args" == "$BIN/$name "* ]]
}

mvp_secrets_running() {
  local name="$1" pid args
  if [[ -f "$RUN/$name.pid" ]]; then
    pid="$(cat "$RUN/$name.pid")"
    # A stale pidfile can refer to an unrelated process after PID reuse.
    if mvp_secrets_pid_matches "$name" "$pid"; then
      return 0
    fi
  fi
  # Also detect a runner binary whose pidfile was lost. Compare literal paths,
  # not an unescaped regular expression that could match another checkout.
  while IFS= read -r args; do
    if [[ "$args" == "$BIN/$name" || "$args" == "$BIN/$name "* ]]; then
      return 0
    fi
  done < <(ps -eo args=)
  return 1
}

mvp_secrets_preflight() {
  local requested="${1:-}" selected other backend="${SECRETS_BACKEND:-}"
  # Unrelated single-module restarts do not change the secrets provider.
  case "$requested" in ""|secrets-file|secrets-vault) ;; *) return 0 ;; esac
  selected="$(mvp_secrets_provider)"
  if [[ -n "$requested" && "$requested" != "$selected" ]]; then
    echo "FAIL: $requested is not selected; MVP_ENABLE_SECRETS_VAULT selects $selected" >&2
    return 1
  fi
  if [[ "$selected" == secrets-vault && -z "${backend//[[:space:]]/}" ]]; then
    echo "FAIL: set SECRETS_BACKEND before selecting secrets-vault" >&2
    return 1
  fi
  other=secrets-vault
  [[ "$selected" == secrets-vault ]] && other=secrets-file
  if mvp_secrets_running "$other"; then
    echo "FAIL: $other is still running; run ./run-host.sh stop-one $other before starting $selected (keep its data)" >&2
    return 1
  fi
}

mvp_start_secrets_provider() {
  if [[ "$(mvp_secrets_provider)" == secrets-vault ]]; then
    maybe_start secrets-vault env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=secrets-vault MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      SECRETS_GRPC_ADDR=":9551" \
      "$BIN/secrets-vault"
  else
    maybe_start secrets-file env \
      MUXCORE_GRPC_ADDR="$MESH" MUXCORE_MODULE_ID=secrets-file MUXCORE_INSECURE_DISABLE_TLS="${MUXCORE_INSECURE_DISABLE_TLS:-}" \
      SECRETS_STORE="$DATA/secrets/store.json" \
      SECRETS_KEY_FILE="$DATA/secrets/master.key" \
      SECRETS_GRPC_ADDR="${SECRETS_GRPC_ADDR:-:9550}" \
      "$BIN/secrets-file"
  fi
}
