#!/usr/bin/env bash
# Offline configuration contract: BFF validation must dial the auth service,
# never its own container loopback, in each supported media stack manifest.
set -euo pipefail
ROOT="${1:-$(cd "$(dirname "$0")/.." && pwd)}"
command -v yq >/dev/null || { echo "skip: yq not found (BFF provider wiring unverified)"; exit 0; }
yq_flags=()
if yq --help 2>&1 | grep -q -- '--yaml-fix-merge-anchor-to-spec'; then
  yq_flags+=(--yaml-fix-merge-anchor-to-spec=true)
fi
for manifest in docker-compose.yml docker-compose.registry.yml; do
  addr="$(yq "${yq_flags[@]}" -r '.services.media-ui.environment.AUTH_GRPC_CLIENT_ADDR' "$ROOT/$manifest")"
  [[ "$addr" == auth-local:9403 ]] || { echo "FAIL: $manifest BFF provider=$addr" >&2; exit 1; }
done
addr="$(yq "${yq_flags[@]}" -r 'select(.kind == "Deployment" and .metadata.name == "media-ui") | .spec.template.spec.containers[] | select(.name == "media-ui") | .env[] | select(.name == "AUTH_GRPC_CLIENT_ADDR") | .value' "$ROOT/deploy/kustomize/overlays/media-stack/media-stack.yaml")"
[[ "$addr" == auth-local:9403 ]] || { echo "FAIL: Kustomize BFF provider=$addr" >&2; exit 1; }
grep -Fq '(dict "name" "AUTH_GRPC_CLIENT_ADDR" "value" "auth-local:9403")' "$ROOT/deploy/helm/muxcore/templates/media-stack.yaml"
# Match the literal shell default, without expanding this test's environment.
# shellcheck disable=SC2016
grep -Fq 'AUTH_GRPC_CLIENT_ADDR="${AUTH_GRPC_CLIENT_ADDR:-127.0.0.1:9403}"' "$ROOT/run-host.sh"
if command -v helm >/dev/null; then
  addr="$(helm template fixture "$ROOT/deploy/helm/muxcore" --set media.enabled=true | yq "${yq_flags[@]}" -r 'select(.kind == "Deployment" and .metadata.name == "media-ui") | .spec.template.spec.containers[] | select(.name == "media-ui") | .env[] | select(.name == "AUTH_GRPC_CLIENT_ADDR") | .value')"
  [[ "$addr" == auth-local:9403 ]] || { echo "FAIL: rendered Helm BFF provider=$addr" >&2; exit 1; }
else
  echo "skip: Helm render (helm not found); source wiring checked"
fi
echo "OK: BFF auth-provider wiring in host, Compose, Helm source and Kustomize"
