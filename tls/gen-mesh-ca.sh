#!/usr/bin/env bash
# Generate MuxCore Mesh CA (10-year ECDSA P-256).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
CA_DIR="$ROOT/ca"
mkdir -p "$CA_DIR"

if [[ -f "$CA_DIR/ca.key" && -f "$CA_DIR/ca.crt" ]]; then
  echo "CA already exists at $CA_DIR (remove to regenerate)"
  openssl x509 -in "$CA_DIR/ca.crt" -noout -subject -dates
  exit 0
fi

openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
  -days 3650 -nodes \
  -keyout "$CA_DIR/ca.key" \
  -out "$CA_DIR/ca.crt" \
  -subj "/O=MuxCore Mesh/CN=MuxCore Mesh CA" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign,digitalSignature"

chmod 600 "$CA_DIR/ca.key"
chmod 644 "$CA_DIR/ca.crt"
echo "Wrote $CA_DIR/ca.crt and $CA_DIR/ca.key"
openssl x509 -in "$CA_DIR/ca.crt" -noout -subject -dates
