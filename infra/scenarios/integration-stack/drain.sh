#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
./preflight.sh startup
set -a; . ./stack.env; set +a
compose=(docker compose --env-file stack.env -f compose.yaml)

[ ! -e run/DRAINING ] || { echo "pilot is already drained: run/DRAINING"; exit 0; }
./evidence.sh pre-drain

payer_address="0x$(jq -r '.address' "$PAYER_KEYSTORE" | tr '[:upper:]' '[:lower:]' | sed 's/^0x//')"
account="$(curl -fsS -H 'content-type: application/json' \
  --data "{\"payer_eth_address\":\"$payer_address\"}" \
  "http://127.0.0.1:${PAID_PORT:-8411}/v1/payment/account")"
credited="$(jq -r '.credited_value_wei' <<<"$account")"
reserved="$(jq -r '.reserved_value_wei' <<<"$account")"
debited="$(jq -r '.debited_value_wei' <<<"$account")"
available="$(jq -r '.available_value_wei' <<<"$account")"
for value in "$credited" "$reserved" "$debited" "$available"; do
  [[ "$value" =~ ^[0-9]+$ ]] && [ "${#value}" -le 18 ] || { echo "account total is outside exact drain arithmetic: $value" >&2; exit 1; }
done
[ "$reserved" -eq 0 ] || { echo "refusing to drain with $reserved wei still reserved; let admitted work settle first" >&2; exit 1; }
[ $((credited - reserved - debited)) -eq "$available" ] || { echo "account does not conserve before drain" >&2; exit 1; }
[ "$available" -le "$ACCOUNT_FLOAT_WEI" ] || { echo "remaining float $available exceeds bounded target $ACCOUNT_FLOAT_WEI" >&2; exit 1; }

umask 077
mkdir -p run
printf 'draining_since=%s\npayer=%s\npayee=%s\nremaining_float_wei=%s\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$payer_address" "$ORCH_ETH_ADDRESS" "$available" > run/DRAINING

"${compose[@]}" stop runner
for _ in $(seq 1 30); do
  health="$(curl -fsS "http://127.0.0.1:${PAID_PORT:-8411}/registry/health" 2>/dev/null || true)"
  ready="$(jq -r --arg jc conformance:job --arg jo all --arg sc "$SESSION_CAPABILITY" --arg so "$SESSION_OFFERING" \
    '[.capabilities[]? | select(((.id==$jc and .offering_id==$jo) or (.id==$sc and .offering_id==$so)) and (.status=="ready" or .status=="degraded"))] | length' <<<"$health" 2>/dev/null || echo 2)"
  [ "$ready" -eq 0 ] && break
  sleep 1
done
[ "${ready:-2}" -eq 0 ] || { echo "runner stopped but broker still reports a selectable pilot offering; investigate before teardown" >&2; exit 1; }

./evidence.sh post-drain
echo "pilot drained: new harness admission is fenced by run/DRAINING and the runner is stopped"
echo "remaining reusable wholesale float: $available wei; no refund or withdrawal is implied"
echo "payer, payee, broker, probe, and all durable volumes were preserved"
