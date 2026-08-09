#!/usr/bin/env bash
# Issue a host cert signed by the mesh CA.
# Usage: ./gen-host-cert.sh [hostname]
# Default hostname: gringotts
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
CA_DIR="$ROOT/ca"
CERT_DIR="$ROOT/certs"
HOST="${1:-gringotts}"
IP="${MESH_HOST_IP:-10.10.0.3}"

if [[ ! -f "$CA_DIR/ca.key" || ! -f "$CA_DIR/ca.crt" ]]; then
  echo "CA missing — run $ROOT/gen-mesh-ca.sh first" >&2
  exit 1
fi
mkdir -p "$CERT_DIR"

KEY="$CERT_DIR/${HOST}.key"
CSR="$CERT_DIR/${HOST}.csr"
CRT="$CERT_DIR/${HOST}.crt"
EXT="$CERT_DIR/${HOST}.ext"

# SANs for Caddy vhosts on this host
cat >"$EXT" <<EOF
basicConstraints=CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=@alt_names

[alt_names]
DNS.1 = ${HOST}
DNS.2 = admin.${HOST}
DNS.3 = media.${HOST}
DNS.4 = api.${HOST}
DNS.5 = auth.${HOST}
DNS.6 = core.${HOST}
DNS.7 = health.${HOST}
IP.1 = ${IP}
IP.2 = 127.0.0.1
EOF

openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -keyout "$KEY" \
  -out "$CSR" \
  -subj "/O=MuxCore Mesh/CN=${HOST}"

openssl x509 -req -in "$CSR" -CA "$CA_DIR/ca.crt" -CAkey "$CA_DIR/ca.key" \
  -CAcreateserial -out "$CRT" -days 825 -extfile "$EXT"

chmod 600 "$KEY"
chmod 644 "$CRT"
rm -f "$CSR"
echo "Wrote $CRT and $KEY"
openssl x509 -in "$CRT" -noout -subject -ext subjectAltName
