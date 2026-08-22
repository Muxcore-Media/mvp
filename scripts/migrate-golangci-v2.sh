#!/usr/bin/env bash
# Migrate module .golangci.yml to canonical v2 (MASTER-ROADMAP P1).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
TEMPLATE="$ROOT/_mvp/tooling/module-golangci-v2.yml"
count=0
while IFS= read -r -d '' f; do
  if grep -q '^version: "2"' "$f" 2>/dev/null; then
    continue
  fi
  cp "$TEMPLATE" "$f"
  echo "updated $f"
  count=$((count + 1))
done < <(find "$ROOT" -name .golangci.yml -not -path '*/core/.golangci.yml' -print0)
echo "migrated $count module .golangci.yml files"
