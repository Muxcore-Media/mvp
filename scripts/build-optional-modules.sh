#!/usr/bin/env bash
# Build linux/amd64 module binaries for vault deploy (optional + newly added peers).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WS="$(cd "$ROOT/.." && pwd)"
OUT="${1:-/tmp/muxcore-optional-bin}"
mkdir -p "$OUT"

build_module() {
  local name="$1" dir="$2"
  echo "building $name"
  (
    cd "$WS/$dir"
    local tmpmod=go.mod.build
    cp go.mod "$tmpmod"
    if ! grep -q 'replace github.com/Muxcore-Media/core/sdk/go/module => ../core/sdk/go/module' go.mod; then
      cat >>go.mod <<'EOF'

replace github.com/Muxcore-Media/core => ../core

replace github.com/Muxcore-Media/core/pkg/contracts => ../core/pkg/contracts

replace github.com/Muxcore-Media/core/sdk/go/client => ../core/sdk/go/client

replace github.com/Muxcore-Media/core/sdk/go/module => ../core/sdk/go/module
EOF
    fi
    go mod tidy
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o "$OUT/$name" ./cmd/module
    mv "$tmpmod" go.mod
    rm -f go.sum.build 2>/dev/null || true
  )
}

# Local repos without vault binary (except downloader-qbittorrent).
for pair in \
  auth-oidc:auth-oidc \
  cache-local:cache-local \
  circuitbreaker-simple:circuitbreaker-simple \
  config-watcher:config-watcher \
  database-postgres:database-postgres \
  data-redaction-pattern:data-redaction-pattern \
  distributed-lock-sqlite:distributed-lock-sqlite \
  emby:emby \
  executor-shell:executor-shell \
  feature-flags-file:feature-flags-file \
  input-validate-jsonschema:input-validate-jsonschema \
  logging-file:logging-file \
  media-dlna:media-dlna \
  media-library-maintainer:media-library-maintainer \
  metrics-prometheus:metrics-prometheus \
  playback-guard:playback-guard \
  playback-monitor:playback-monitor \
  plex:plex \
  scheduler-cron:scheduler-cron \
  secrets-vault:secrets-vault \
  serialization-safe:serialization-safe \
  spool-resolver-http:spool-resolver-http \
  tracing-otlp:tracing-otlp \
  userdata-local:userdata-local \
  worker-pool-memory:worker-pool-memory
do
  name="${pair%%:*}"
  dir="${pair#*:}"
  build_module "$name" "$dir"
done

echo "built $(ls -1 "$OUT" | wc -l) binaries in $OUT"
