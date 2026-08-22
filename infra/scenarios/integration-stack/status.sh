#!/usr/bin/env bash
set -uo pipefail
cd "$(dirname "$0")"
RUN_DIR="${RUN_DIR:-$PWD/run}"
[ -f stack.env ] && { set -a; . ./stack.env; set +a; }

echo "processes:"
if [ -f "$RUN_DIR/pids" ]; then
  while read -r pid; do
    [ -n "$pid" ] || continue
    if kill -0 "$pid" 2>/dev/null; then
      printf "  %-8s %s\n" "$pid" "$(ps -p "$pid" -o args= | cut -c1-90)"
    else
      printf "  %-8s (gone)\n" "$pid"
    fi
  done < "$RUN_DIR/pids"
else
  echo "  none tracked"
fi

echo "paid surface:"
code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${PAID_PORT:-8411}/healthz" 2>/dev/null)
echo "  healthz ${code:-unreachable}"
echo "offerings:"
curl -s "http://127.0.0.1:${PAID_PORT:-8411}/registry/offerings" 2>/dev/null \
  | head -c 400 | sed 's/^/  /'
echo
