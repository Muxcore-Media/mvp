#!/usr/bin/env bash
# Issue mTLS client certificates for host-started MVP modules from the staging CA.
# Requires core to have already created tls/ca-mesh (run staging muxcored once).
#
# Usage:
#   ./scripts/issue-staging-module-certs.sh [module-id ...]
# Default modules: common MVP peers used by run-host.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CA_DIR="${MUXCORE_GRPC_CA_CERT_DIR:-$ROOT/tls/ca-mesh}"
OUT_ROOT="${MUXCORE_MODULE_CERT_DIR:-$ROOT/tls/module-certs}"

if [[ ! -f "$CA_DIR/ca.crt" || ! -f "$CA_DIR/ca.key" ]]; then
  echo "CA missing at $CA_DIR — start staging core once first" >&2
  exit 1
fi

modules=("$@")
if [[ ${#modules[@]} -eq 0 ]]; then
  modules=(
    api-rest auth-local database-sqlite secrets-file encryption-aesgcm
    call-policy-default publish-policy-default health-monitor
    media-movies media-tvshows media-automation metadata-tmdb
    media-scanner media-root-folders request-media notification-default
    jellyfin downloader-native-torrent indexer-torznab
  )
fi

mkdir -p "$OUT_ROOT"
for id in "${modules[@]}"; do
  dir="$OUT_ROOT/$id"
  mkdir -p "$dir"
  chmod 700 "$dir"
  key="$dir/module.key"
  csr="$dir/module.csr"
  crt="$dir/module.crt"
  ext="$dir/module.ext"
  cat >"$ext" <<EXT
basicConstraints=CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=clientAuth,serverAuth
subjectAltName=DNS:localhost,IP:127.0.0.1,IP:::1
EXT
  openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
    -keyout "$key" -out "$csr" -subj "/O=MuxCore/CN=${id}" >/dev/null 2>&1
  openssl x509 -req -in "$csr" -CA "$CA_DIR/ca.crt" -CAkey "$CA_DIR/ca.key" \
    -CAcreateserial -out "$crt" -days 825 -extfile "$ext" >/dev/null 2>&1
  cp "$CA_DIR/ca.crt" "$dir/ca.crt"
  chmod 600 "$key" "$crt"
  rm -f "$csr" "$ext"
  echo "issued $dir"
done

echo "Set for each module (or source from run-host staging):"
echo "  MUXCORE_TLS_CERT=\$dir/module.crt MUXCORE_TLS_KEY=\$dir/module.key MUXCORE_TLS_CA=\$dir/ca.crt"
