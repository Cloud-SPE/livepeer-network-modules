#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"
mode="${1:-startup}"
case "$mode" in
  startup|pilot) ;;
  *) echo "usage: $0 [startup|pilot]" >&2; exit 2 ;;
esac

if [ "$mode" = pilot ] && [ "${PILOT_APPROVAL:-}" != "ARBITRUM_ONE_DUST_APPROVED" ]; then
  echo "refusing to mint: set PILOT_APPROVAL=ARBITRUM_ONE_DUST_APPROVED for this invocation after reviewing addresses and limits" >&2
  exit 2
fi
[ -f stack.env ] || { echo "stack.env missing — copy stack.env.example and edit it" >&2; exit 1; }
if grep -Eq '^[[:space:]]*PILOT_APPROVAL=' stack.env; then
  echo "stack.env must not persist PILOT_APPROVAL; approval is invocation-scoped" >&2
  exit 1
fi

set -a; . ./stack.env; set +a

required=(
  PAYMENT_IMAGE BROKER_IMAGE RUNNER_IMAGE CHAIN_ID CHAIN_RPC_URLS
  ORCH_ETH_ADDRESS PAYER_KEYSTORE PAYER_KEYSTORE_PASSWORD_FILE
  PAYEE_KEYSTORE PAYEE_KEYSTORE_PASSWORD_FILE BROKER_ADMIN_TOKEN
  PAYEE_ADMIN_TOKEN WORK_UNIT PRICE_WEI PER_UNITS MAX_AUTHORIZATION_UNITS
  ACCOUNT_FLOAT_WEI MAX_PAYMENT_WEI MAX_AUTHORIZATION_WEI
  MAX_TICKET_FACE_VALUE_WEI MAX_PRICE_PER_UNIT_WEI EXTERNAL_BASE_URL
	RUNNER_CALLBACK_BASE_URL
	SESSION_CAPABILITY SESSION_OFFERING SESSION_WORK_UNIT SESSION_PRICE_WEI
	SESSION_PER_UNITS SESSION_MAX_AUTHORIZATION_UNITS SESSION_RUNNER_CONTROL_URL
	SESSION_MAX_PRICE_PER_UNIT_WEI
)
missing=()
for name in "${required[@]}"; do
  [ -n "${!name:-}" ] || missing+=("$name")
done
if [ "${#missing[@]}" -ne 0 ]; then
  printf 'stack.env is from an older scenario or incomplete; missing: %s\n' "${missing[*]}" >&2
  echo "migrate it from stack.env.example before starting any container" >&2
  exit 1
fi

for name in PAYMENT_IMAGE BROKER_IMAGE RUNNER_IMAGE; do
  value="${!name}"
  if [[ ! "$value" =~ @sha256:[0-9a-f]{64}$ ]] || [[ "$value" == *REPLACE* ]]; then
    echo "$name must be an immutable image reference ending in @sha256:<64 lowercase hex characters>" >&2
    exit 1
  fi
done
[ "$CHAIN_ID" = 42161 ] || { echo "CHAIN_ID must be 42161 (Arbitrum One)" >&2; exit 1; }
[[ "$ORCH_ETH_ADDRESS" =~ ^0x[0-9a-fA-F]{40}$ ]] || { echo "ORCH_ETH_ADDRESS is not a 20-byte hex address" >&2; exit 1; }
[[ "$EXTERNAL_BASE_URL" =~ ^https://[A-Za-z0-9.-]+(:[0-9]+)?/?$ ]] || { echo "EXTERNAL_BASE_URL must be an https origin without userinfo, path, query, or fragment" >&2; exit 1; }
[[ "$RUNNER_CALLBACK_BASE_URL" =~ ^https?://[A-Za-z0-9.-]+(:[0-9]+)?/?$ ]] || { echo "RUNNER_CALLBACK_BASE_URL must be an http(s) origin without userinfo, path, query, or fragment" >&2; exit 1; }

for name in PAYER_KEYSTORE PAYER_KEYSTORE_PASSWORD_FILE PAYEE_KEYSTORE PAYEE_KEYSTORE_PASSWORD_FILE; do
  path="${!name}"
  if [ ! -f "$path" ] || [ ! -r "$path" ]; then
    echo "$name is missing or unreadable" >&2
    exit 1
  fi
done

payee_address="$(jq -r '.address // empty' "$PAYEE_KEYSTORE" | tr '[:upper:]' '[:lower:]')"
configured_address="$(printf '%s' "$ORCH_ETH_ADDRESS" | sed 's/^0x//' | tr '[:upper:]' '[:lower:]')"
if [ -z "$payee_address" ] || [ "$payee_address" != "$configured_address" ]; then
  echo "ORCH_ETH_ADDRESS does not match PAYEE_KEYSTORE" >&2
  exit 1
fi
payer_address="$(jq -r '.address // empty' "$PAYER_KEYSTORE" | sed 's/^0x//' | tr '[:upper:]' '[:lower:]')"
[[ "$payer_address" =~ ^[0-9a-f]{40}$ ]] || { echo "PAYER_KEYSTORE does not contain a 20-byte address" >&2; exit 1; }

numeric=(PRICE_WEI PER_UNITS MAX_AUTHORIZATION_UNITS ACCOUNT_FLOAT_WEI MAX_PAYMENT_WEI MAX_AUTHORIZATION_WEI MAX_TICKET_FACE_VALUE_WEI MAX_PRICE_PER_UNIT_WEI)
numeric+=(SESSION_PRICE_WEI SESSION_PER_UNITS SESSION_MAX_AUTHORIZATION_UNITS SESSION_MAX_PRICE_PER_UNIT_WEI)
for name in "${numeric[@]}"; do
  [[ "${!name}" =~ ^[1-9][0-9]*$ ]] || { echo "$name must be a positive decimal integer" >&2; exit 1; }
  value="${!name}"
  if [ "${#value}" -gt 18 ]; then
    echo "$name exceeds this dust harness's exact arithmetic ceiling of 999999999999999999" >&2
    exit 1
  fi
done

max_int=9223372036854775807
[ "$MAX_AUTHORIZATION_UNITS" -le $((max_int / PRICE_WEI)) ] || { echo "MAX_AUTHORIZATION_UNITS * PRICE_WEI overflows exact pilot arithmetic" >&2; exit 1; }
[ "$SESSION_MAX_AUTHORIZATION_UNITS" -le $((max_int / SESSION_PRICE_WEI)) ] || { echo "SESSION_MAX_AUTHORIZATION_UNITS * SESSION_PRICE_WEI overflows exact pilot arithmetic" >&2; exit 1; }
debit_numerator=$((MAX_AUTHORIZATION_UNITS * PRICE_WEI))
[ "$debit_numerator" -le $((max_int - PER_UNITS + 1)) ] || { echo "maximum-debit ceiling arithmetic overflows" >&2; exit 1; }
[ "$PRICE_WEI" -le $((max_int - PER_UNITS + 1)) ] || { echo "unit-price ceiling arithmetic overflows" >&2; exit 1; }
max_debit=$(( (debit_numerator + PER_UNITS - 1) / PER_UNITS ))
unit_price=$(( (PRICE_WEI + PER_UNITS - 1) / PER_UNITS ))
session_debit_numerator=$((SESSION_MAX_AUTHORIZATION_UNITS * SESSION_PRICE_WEI))
[ "$session_debit_numerator" -le $((max_int - SESSION_PER_UNITS + 1)) ] || { echo "session maximum-debit ceiling arithmetic overflows" >&2; exit 1; }
[ "$SESSION_PRICE_WEI" -le $((max_int - SESSION_PER_UNITS + 1)) ] || { echo "session unit-price ceiling arithmetic overflows" >&2; exit 1; }
session_max_debit=$(( (session_debit_numerator + SESSION_PER_UNITS - 1) / SESSION_PER_UNITS ))
[ "$ACCOUNT_FLOAT_WEI" -ge "$max_debit" ] || { echo "ACCOUNT_FLOAT_WEI must cover maximum authorization debit $max_debit" >&2; exit 1; }
[ "$ACCOUNT_FLOAT_WEI" -ge "$session_max_debit" ] || { echo "ACCOUNT_FLOAT_WEI must cover session maximum authorization debit $session_max_debit" >&2; exit 1; }
[ "$MAX_PAYMENT_WEI" -ge "$ACCOUNT_FLOAT_WEI" ] || { echo "MAX_PAYMENT_WEI must cover an empty-account refill of $ACCOUNT_FLOAT_WEI" >&2; exit 1; }
[ "$MAX_AUTHORIZATION_WEI" -ge "$max_debit" ] || { echo "MAX_AUTHORIZATION_WEI must cover maximum authorization debit $max_debit" >&2; exit 1; }
[ "$MAX_AUTHORIZATION_WEI" -ge "$session_max_debit" ] || { echo "MAX_AUTHORIZATION_WEI must cover session maximum authorization debit $session_max_debit" >&2; exit 1; }
[ "$MAX_AUTHORIZATION_WEI" -ge "$ACCOUNT_FLOAT_WEI" ] || { echo "MAX_AUTHORIZATION_WEI must cover the concurrency probe's one-float reservation of $ACCOUNT_FLOAT_WEI" >&2; exit 1; }
[ "$MAX_PRICE_PER_UNIT_WEI" -ge "$unit_price" ] || { echo "MAX_PRICE_PER_UNIT_WEI must be at least $unit_price" >&2; exit 1; }
[ "$SESSION_MAX_PRICE_PER_UNIT_WEI" -ge $(( (SESSION_PRICE_WEI + SESSION_PER_UNITS - 1) / SESSION_PER_UNITS )) ] || { echo "SESSION_MAX_PRICE_PER_UNIT_WEI is below the session quote" >&2; exit 1; }
[ "$MAX_TICKET_FACE_VALUE_WEI" -ge 1000000000000000 ] || { echo "MAX_TICKET_FACE_VALUE_WEI must cover the receiver's current 1000000000000000 wei redeemable face" >&2; exit 1; }

rpc="${CHAIN_RPC_URLS%%,*}"
chain_hex="$(curl -fsS --max-time 15 -H 'content-type: application/json' --data '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' "$rpc" | jq -r '.result // empty')"
if [ -z "$chain_hex" ] || [ "$((chain_hex))" -ne 42161 ]; then
  echo "first CHAIN_RPC_URLS endpoint did not report Arbitrum One" >&2
  exit 1
fi

echo "preflight ok: mode=$mode chain_id=42161 payee=$ORCH_ETH_ADDRESS max_debit_wei=$max_debit target_float_wei=$ACCOUNT_FLOAT_WEI"
