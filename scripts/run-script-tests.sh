#!/usr/bin/env bash
# Run offline script tests under _mvp/scripts/.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
failed=0
for t in "$SCRIPT_DIR"/*_test.sh; do
  [[ -f "$t" ]] || continue
  echo "==> $(basename "$t")"
  if ! bash "$t"; then
    failed=1
  fi
done
exit "$failed"
