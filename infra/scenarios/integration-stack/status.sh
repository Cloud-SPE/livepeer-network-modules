#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
set -a; . ./stack.env; set +a
compose=(docker compose --env-file stack.env -f compose.yaml)
if [ -f run/DRAINING ]; then
  echo "ADMISSION DRAINED"
  sed -n '1,10p' run/DRAINING
  echo
fi
"${compose[@]}" ps
echo
curl -fsS "http://127.0.0.1:${PAID_PORT:-8411}/registry/offerings" || true
echo
curl -fsS "http://127.0.0.1:${PAYEE_METRICS_PORT:-9413}/metrics" | grep 'livepeer_payment_wholesale_account_value_wei' || true
