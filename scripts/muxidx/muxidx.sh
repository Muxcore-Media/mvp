#!/bin/bash
ROOT="/home/ender/Projects/MuxCore"
PY="$ROOT/.venv/bin/python3"
if [ ! -x "$PY" ]; then
  PY="$(command -v python3)"
fi
exec "$PY" "$ROOT/scripts/muxidx/main.py" "$@"
