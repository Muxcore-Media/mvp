#!/usr/bin/env bash
# proto/smoke.protoset (the descriptors containerized grpcurl uses in registry
# smoke mode; core and modules serve no gRPC reflection) must match what
# cmd/smokeprotoset generates from the modules' Go packages. Regenerate with:
#   go run ./cmd/smokeprotoset -o proto/smoke.protoset
# Skips when Go or the private module dependencies are unavailable.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WS="$(cd "$ROOT/.." && pwd)"

command -v go >/dev/null || { echo "skip: go not found"; exit 0; }
[[ -f "$ROOT/proto/smoke.protoset" ]] || { echo "FAIL: proto/smoke.protoset missing" >&2; exit 1; }

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
# Inside the umbrella use its go.work (pinned submodule commits), else go.mod.
if [[ -f "$WS/go.work" ]]; then export GOWORK="$WS/go.work"; fi
export GOPRIVATE="${GOPRIVATE:-github.com/Muxcore-Media/*}" GONOSUMDB="${GONOSUMDB:-github.com/Muxcore-Media/*}" GIT_TERMINAL_PROMPT=0
if ! (cd "$ROOT" && go run ./cmd/smokeprotoset -o "$tmp") 2>"$tmp.err"; then
  echo "skip: cannot build cmd/smokeprotoset ($(tail -1 "$tmp.err"))"
  rm -f "$tmp.err"
  exit 0
fi
rm -f "$tmp.err"
if ! cmp -s "$tmp" "$ROOT/proto/smoke.protoset"; then
  echo "FAIL: proto/smoke.protoset is stale; run: go run ./cmd/smokeprotoset -o proto/smoke.protoset" >&2
  exit 1
fi
echo "OK: proto/smoke.protoset matches cmd/smokeprotoset"
