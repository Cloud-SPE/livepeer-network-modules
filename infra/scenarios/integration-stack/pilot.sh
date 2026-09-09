#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

if [ "${PILOT_APPROVAL:-}" != "ARBITRUM_ONE_DUST_APPROVED" ]; then
  echo "refusing to mint: set PILOT_APPROVAL=ARBITRUM_ONE_DUST_APPROVED for this invocation after reviewing addresses and limits" >&2
  exit 2
fi
[ -f stack.env ] || { echo "stack.env missing" >&2; exit 1; }
set -a; . ./stack.env; set +a
[ "${CHAIN_ID:-}" = "42161" ] || { echo "refusing: CHAIN_ID must be 42161" >&2; exit 2; }

echo "approved Arbitrum One dust pilot"
echo "  payee:                 ${ORCH_ETH_ADDRESS}"
echo "  target float:          ${ACCOUNT_FLOAT_WEI:-derived from authorization maximum} wei"
echo "  max payment:           ${MAX_PAYMENT_WEI} wei"
echo "  max winning face:      ${MAX_TICKET_FACE_VALUE_WEI} wei"
echo "  max authorization:     ${MAX_AUTHORIZATION_WEI} wei"

args=(
  --protocol=wholesale
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
)
if [ -n "${ACCOUNT_FLOAT_WEI:-}" ]; then
  args+=(--account-float-wei="$ACCOUNT_FLOAT_WEI")
fi
docker compose --env-file stack.env -f compose.yaml --profile pilot run --rm probe "${args[@]}"
