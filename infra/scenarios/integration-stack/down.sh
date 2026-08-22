#!/usr/bin/env bash
# Stop the stack. Uses the pid file written at start rather than
# pattern-matching command lines: an earlier round of live testing left
# daemons running against mainnet for six hours because the greps used
# to find them did not match what the processes actually carried.
set -uo pipefail

cd "$(dirname "$0")"
RUN_DIR="${RUN_DIR:-$PWD/run}"
PID_FILE="$RUN_DIR/pids"

if [ ! -f "$PID_FILE" ]; then
  echo "no pid file at $PID_FILE; nothing tracked to stop"
else
  while read -r pid; do
    [ -n "$pid" ] || continue
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null
      echo "  stopped $pid"
    fi
  done < "$PID_FILE"
  sleep 2
  while read -r pid; do
    [ -n "$pid" ] || continue
    kill -9 "$pid" 2>/dev/null && echo "  force-killed $pid"
  done < "$PID_FILE"
  rm -f "$PID_FILE"
fi

rm -f /tmp/lpm-payer.sock /tmp/lpm-payee.sock
echo "sockets removed"

# Anything still holding the sockets is a leftover from an earlier run
# and is worth naming rather than leaving behind.
if command -v ss >/dev/null && ss -xl 2>/dev/null | grep -q lpm-pay; then
  echo "WARNING: something still listens on /tmp/lpm-*.sock:" >&2
  ss -xlp 2>/dev/null | grep lpm-pay >&2
fi
