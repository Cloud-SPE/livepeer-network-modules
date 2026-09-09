#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
./preflight.sh pilot
set -a; . ./stack.env; set +a

compose=(docker compose --env-file stack.env -f compose.yaml)
checkpoint="/var/lib/livepeer/payment-daemon/recovery-$(date -u +%Y%m%dT%H%M%SZ)-$$.json"
common=(
  --payer-socket=/var/run/livepeer/payer/payer-daemon.sock
  --payee-socket=/var/run/livepeer/payee/payment-daemon.sock
  --broker-url=http://broker:8080
  --broker-uri="${EXTERNAL_BASE_URL%/}"
  --recipient="$ORCH_ETH_ADDRESS"
  --chain-id="$CHAIN_ID"
  --capability=conformance:job
  --offering=all
  --work-unit="${WORK_UNIT:-tokens}"
  --price-wei="$PRICE_WEI"
  --per-units="${PER_UNITS:-1000}"
  --max-authorization-units="${MAX_AUTHORIZATION_UNITS:-131072}"
  --account-float-wei="$ACCOUNT_FLOAT_WEI"
  --checkpoint-file="$checkpoint"
)

echo "preparing durable wholesale recovery checkpoint: $checkpoint"
"${compose[@]}" --profile pilot run --rm probe --protocol=wholesale-recovery-prepare "${common[@]}"

echo "stopping all stateful participants at the admitted checkpoint"
"${compose[@]}" stop payer runner broker payee

echo "restarting in receiver-first migration order"
./up.sh

echo "verifying replay and settling the preserved authorization"
"${compose[@]}" --profile pilot run --rm probe --protocol=wholesale-recovery-verify "${common[@]}"

echo "restart checkpoint verified: $checkpoint"
