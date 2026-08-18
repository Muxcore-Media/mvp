#!/usr/bin/env bash
# Force one wanted-search pass (does not wait for RSS).
# Usage (on the host running media-automation):
#   ./scripts/search-now.sh
#   AUTOMATION_GRPC_ADDR=127.0.0.1:9460 ./scripts/search-now.sh
set -euo pipefail
ADDR="${AUTOMATION_GRPC_ADDR:-127.0.0.1:9460}"
if [[ "$ADDR" == :* ]]; then
  ADDR="127.0.0.1${ADDR}"
fi
if ! command -v grpcurl >/dev/null 2>&1; then
  echo "search-now: grpcurl not found. Set the search_now setting to true, or:" >&2
  echo "  grpcurl -plaintext -d '{}' ${ADDR} muxcore.automation.v1.AutomationService/SearchNow" >&2
  exit 1
fi
grpcurl -plaintext -d '{}' "$ADDR" muxcore.automation.v1.AutomationService/SearchNow
