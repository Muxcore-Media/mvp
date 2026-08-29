#!/usr/bin/env bash
# Run offline script tests under _mvp/scripts/.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
failed=0
echo "==> check-household-manifest.sh"
if ! bash "$ROOT/scripts/check-household-manifest.sh"; then
  failed=1
fi
for t in "$SCRIPT_DIR"/*_test.sh; do
  [[ -f "$t" ]] || continue
  echo "==> $(basename "$t")"
  if ! bash "$t"; then
    failed=1
  fi
done
exit "$failed"
