#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
./preflight.sh startup
set -a; . ./stack.env; set +a

label="${1:-snapshot}"
[[ "$label" =~ ^[a-zA-Z0-9._-]+$ ]] || { echo "evidence label must use only letters, digits, dot, underscore, or dash" >&2; exit 2; }
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
bundle="run/evidence-${stamp}-${label}"
umask 077
mkdir -p "$bundle"
compose=(docker compose --env-file stack.env -f compose.yaml)
payer_address="0x$(jq -r '.address' "$PAYER_KEYSTORE" | tr '[:upper:]' '[:lower:]' | sed 's/^0x//')"

jq -n \
  --arg observed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg chain_id "$CHAIN_ID" --arg payer "$payer_address" --arg payee "$ORCH_ETH_ADDRESS" \
  --arg payment_image "$PAYMENT_IMAGE" --arg broker_image "$BROKER_IMAGE" --arg runner_image "$RUNNER_IMAGE" \
  --arg external_base_url "${EXTERNAL_BASE_URL%/}" \
  --arg work_unit "$WORK_UNIT" --arg price_wei "$PRICE_WEI" --arg per_units "$PER_UNITS" \
  --arg max_units "$MAX_AUTHORIZATION_UNITS" --arg target_float "$ACCOUNT_FLOAT_WEI" \
  --arg max_payment "$MAX_PAYMENT_WEI" --arg max_authorization "$MAX_AUTHORIZATION_WEI" \
  --arg max_face "$MAX_TICKET_FACE_VALUE_WEI" --arg max_unit_price "$MAX_PRICE_PER_UNIT_WEI" \
  --arg session_capability "$SESSION_CAPABILITY" --arg session_offering "$SESSION_OFFERING" \
  --arg session_unit "$SESSION_WORK_UNIT" --arg session_price "$SESSION_PRICE_WEI" \
  --arg session_per_units "$SESSION_PER_UNITS" --arg session_max_units "$SESSION_MAX_AUTHORIZATION_UNITS" \
  '{observed_at:$observed_at,chain_id:$chain_id,payer:$payer,payee:$payee,
    images:{payment:$payment_image,broker:$broker_image,runner:$runner_image},external_base_url:$external_base_url,
    job:{work_unit:$work_unit,price_wei:$price_wei,per_units:$per_units,max_authorization_units:$max_units},
    session:{capability:$session_capability,offering:$session_offering,work_unit:$session_unit,price_wei:$session_price,per_units:$session_per_units,max_authorization_units:$session_max_units},
    limits:{target_float_wei:$target_float,max_payment_wei:$max_payment,max_authorization_wei:$max_authorization,max_ticket_face_value_wei:$max_face,max_price_per_unit_wei:$max_unit_price}}' \
  > "$bundle/config.json"

"${compose[@]}" ps --format json > "$bundle/containers.jsonl"
: > "$bundle/images.jsonl"
for service in payee broker runner payer; do
  container_id="$("${compose[@]}" ps -aq "$service")"
  [ -n "$container_id" ] || continue
  local_image_id="$(docker inspect --format '{{.Image}}' "$container_id")"
  configured_image="$(docker inspect --format '{{.Config.Image}}' "$container_id")"
  state="$(docker inspect --format '{{.State.Status}}' "$container_id")"
  jq -cn --arg service "$service" --arg container_id "$container_id" \
    --arg configured_image "$configured_image" --arg local_image_id "$local_image_id" --arg state "$state" \
    '{service:$service,container_id:$container_id,configured_image:$configured_image,local_image_id:$local_image_id,state:$state}' \
    >> "$bundle/images.jsonl"
done

{
  docker run --rm --entrypoint /usr/local/bin/livepeer-payment-daemon "$PAYMENT_IMAGE" --version
  docker run --rm --entrypoint /usr/local/bin/livepeer-capability-broker "$BROKER_IMAGE" --version
  docker run --rm --entrypoint /usr/local/bin/livepeer-conformance "$RUNNER_IMAGE" --version
} > "$bundle/versions.txt"

curl -fsS "http://127.0.0.1:${PAID_PORT:-8411}/registry/offerings" > "$bundle/registry-offerings.json"
curl -fsS "http://127.0.0.1:${PAID_PORT:-8411}/registry/health" > "$bundle/registry-health.json"
curl -fsS "http://127.0.0.1:${PAYEE_METRICS_PORT:-9413}/metrics" \
  | grep -E '^(# (HELP|TYPE) livepeer_payment_(build_info|tickets_|credited_ev_|wholesale_account_|redemption_)|livepeer_payment_(build_info|tickets_|credited_ev_|wholesale_account_|redemption_))' \
  > "$bundle/payee-metrics.prom"
curl -fsS "http://127.0.0.1:${PAYER_METRICS_PORT:-9414}/metrics" \
  | grep -E '^(# (HELP|TYPE) livepeer_payment_(build_info|payments_created_|tickets_signed_|sender_deposit_|sender_reserve_)|livepeer_payment_(build_info|payments_created_|tickets_signed_|sender_deposit_|sender_reserve_))' \
  > "$bundle/payer-metrics.prom"
curl -fsS "http://127.0.0.1:${METRICS_PORT:-9412}/metrics" \
  | grep -E '^(# (HELP|TYPE) livepeer_(paid_|payment_client_|broker_registry_)|livepeer_(paid_|payment_client_|broker_registry_))' \
  > "$bundle/broker-metrics.prom"

if [ -f run/DRAINING ]; then
  cp run/DRAINING "$bundle/drain-state.txt"
else
  printf 'state=admitting\nobserved_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$bundle/drain-state.txt"
fi

"${compose[@]}" --profile pilot run --rm --no-deps probe \
  --protocol=wholesale-evidence \
  --payer-socket=/var/run/livepeer/payer/payer-daemon.sock \
  --payee-socket=/var/run/livepeer/payee/payment-daemon.sock \
  --broker-url=http://broker:8080 \
  --recipient="$ORCH_ETH_ADDRESS" \
  --checkpoint-dir=/var/lib/livepeer/payment-daemon \
  > "$bundle/reconciliation.txt"

(cd "$bundle" && sha256sum config.json containers.jsonl images.jsonl versions.txt registry-offerings.json registry-health.json \
  payee-metrics.prom payer-metrics.prom broker-metrics.prom drain-state.txt reconciliation.txt > SHA256SUMS)
echo "redact-safe evidence bundle: $bundle"
