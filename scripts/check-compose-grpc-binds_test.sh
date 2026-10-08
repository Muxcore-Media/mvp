#!/usr/bin/env bash
# Offline: in docker-compose.registry.yml every module gRPC listen address
# (*_GRPC_ADDR, excluding *_CLIENT_ADDR, MUXCORE_GRPC_ADDR and AUTH_LOCAL_GRPC_ADDR, which are dial
# targets) names the service itself ("<service>:<port>"). In the insecure dev
# profile modules rewrite a host-less or 0.0.0.0 bind to 127.0.0.1 and advertise
# that address to core, so core and peers in other containers cannot reach them
# (found by the first clean-room run, T-M2-10).
#
# media-ui (the BFF) runs no gRPC server; its *_GRPC_ADDR values are dial targets.
# Exception: services in core's network namespace (network_mode: service:core)
# must stay host-less so they advertise a loopback address, which is the only
# plaintext address core dials in the dev profile.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMPOSE="${1:-$ROOT/docker-compose.registry.yml}"

command -v yq >/dev/null || { echo "skip: yq not found"; exit 0; }
command -v jq >/dev/null || { echo "skip: jq not found"; exit 0; }

yq_flags=()
if yq --help 2>&1 | grep -q -- '--yaml-fix-merge-anchor-to-spec'; then
  yq_flags+=(--yaml-fix-merge-anchor-to-spec=true)
fi

# Lines "service<TAB>network_mode<TAB>VAR<TAB>value".
binds="$(yq "${yq_flags[@]}" -o=json 'explode(.)' "$COMPOSE" | jq -r '
  .services | to_entries[] | select(.key != "media-ui") | .key as $svc | (.value.network_mode // "-") as $nm
  | (.value.environment // {}) | to_entries[]
  | select(.key | test("_GRPC_ADDR$")) | select(.key | test("_CLIENT_ADDR$|^MUXCORE_GRPC_ADDR$|^AUTH_LOCAL_GRPC_ADDR$") | not)
  | "\($svc)\t\($nm)\t\(.key)\t\(.value)"')"

fail=0
checked=0
while IFS=$'\t' read -r svc nm var val; do
  [[ -n "$svc" ]] || continue
  checked=$((checked + 1))
  host="${val%:*}"
  if [[ "$nm" == "service:core" ]]; then
    if [[ -n "$host" && "$host" != 127.0.0.1 && "$host" != localhost ]]; then
      echo "FAIL: $svc shares core's network namespace; $var=$val must be host-less (\":port\") so core dials it on loopback" >&2
      fail=1
    fi
    continue
  fi
  if [[ "$host" != "$svc" ]]; then
    echo "FAIL: $svc $var=$val must bind the service name (\"$svc:${val##*:}\")" >&2
    fail=1
  fi
done <<<"$binds"

((checked > 0)) || { echo "FAIL: no *_GRPC_ADDR listen addresses found in $COMPOSE (parse error?)" >&2; exit 1; }
[[ "$fail" -eq 0 ]] || exit 1
echo "OK: compose gRPC listen addresses reachable across containers ($checked checked)"
