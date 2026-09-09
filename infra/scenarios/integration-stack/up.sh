#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"
./preflight.sh startup
[ ! -e run/DRAINING ] || { echo "pilot is drained; preserve or clear run/DRAINING explicitly before restarting admission" >&2; exit 2; }
[ -f stack.env ] || { echo "stack.env missing — copy stack.env.example and edit it" >&2; exit 1; }
set -a; . ./stack.env; set +a

compose=(docker compose --env-file stack.env -f compose.yaml)
mkdir -p run
[ -f run/seal.key ] || openssl rand -hex 32 > run/seal.key
if [ ! -f run/settlement.key ]; then
  docker run --rm --user "$(id -u):$(id -g)" -v "$PWD/run:/state" \
    --entrypoint /usr/local/bin/livepeer-capability-broker \
    "${BROKER_IMAGE:?set BROKER_IMAGE}" settlement-key generate --out /state/settlement.key
fi
# Both broker secrets are bind-mounted into a uid-65532 container. Change
# ownership without making private key material world-readable.
docker run --rm --user 0 -v "$PWD/run:/state" --entrypoint sh \
  "${BROKER_IMAGE:?set BROKER_IMAGE}" -c \
  'chown 65532:65532 /state/seal.key /state/settlement.key && chmod 0400 /state/seal.key /state/settlement.key'
./render-config.sh > run/host-config.yaml

wait_for_log() {
  local service="$1" pattern="$2"
  for _ in $(seq 1 120); do
    if "${compose[@]}" logs "$service" 2>&1 | grep -q "$pattern"; then return 0; fi
    sleep 1
  done
  echo "$service did not become ready; inspect: ${compose[*]} logs $service" >&2
  return 1
}

# Migration order is an invariant: receiver first, advertisement second,
# payer opt-in last. No probe runs here and startup itself mints no ticket.
"${compose[@]}" up -d payee
wait_for_log payee "gRPC listening"
"${compose[@]}" up -d broker
for _ in $(seq 1 120); do
  if curl -fsS "http://127.0.0.1:${PAID_PORT:-8411}/healthz" >/dev/null; then break; fi
  sleep 1
done
curl -fsS "http://127.0.0.1:${PAID_PORT:-8411}/healthz" >/dev/null
"${compose[@]}" up -d runner
registry=""
for _ in $(seq 1 120); do
  registry="$(curl -fsS "http://127.0.0.1:${PAID_PORT:-8411}/registry/offerings" 2>/dev/null || true)"
  if grep -q 'conformance:job' <<<"$registry" && grep -q 'wholesale_accounts' <<<"$registry"; then break; fi
  sleep 1
done
grep -q 'conformance:job' <<<"$registry" || { echo "runner did not freeze the pilot offer" >&2; exit 1; }
grep -q 'wholesale_accounts' <<<"$registry" || { echo "pilot offer did not advertise wholesale accounts" >&2; exit 1; }
"${compose[@]}" up -d payer
wait_for_log payer "gRPC listening"

echo "pilot stack ready; no ticket has been minted"
echo "review ./status.sh, then run ./pilot.sh only with explicit spend approval"
