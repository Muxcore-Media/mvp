#!/usr/bin/env bash
# Persistent SSH local-forwards: this machine → gringotts MuxCore (via platform9 jump).
# Requires Host gringotts in ~/.ssh/config with ProxyJump.
set -euo pipefail
LOG=/tmp/muxcore-gringotts-tunnel.log
PIDF=/tmp/muxcore-gringotts-tunnel.pid
echo "$(date -Is) supervisor start" >"$LOG"
while true; do
  echo "$(date -Is) starting standard-port tunnel" >>"$LOG"
  ssh -N \
    -o BatchMode=yes \
    -o ExitOnForwardFailure=yes \
    -o ServerAliveInterval=30 \
    -o ServerAliveCountMax=3 \
    -o TCPKeepAlive=yes \
    -o ConnectTimeout=20 \
    -L 127.0.0.1:8082:127.0.0.1:8082 \
    -L 127.0.0.1:5173:127.0.0.1:5173 \
    -L 127.0.0.1:18080:127.0.0.1:18080 \
    -L 127.0.0.1:9401:127.0.0.1:9401 \
    -L 127.0.0.1:8080:127.0.0.1:8080 \
    -L 127.0.0.1:8443:127.0.0.1:443 \
    -L 127.0.0.1:9380:127.0.0.1:9380 \
    gringotts >>"$LOG" 2>&1 &
  spid=$!
  echo "$spid" >"$PIDF"
  wait "$spid" || true
  echo "$(date -Is) tunnel exited; retry in 5s" >>"$LOG"
  sleep 5
done
