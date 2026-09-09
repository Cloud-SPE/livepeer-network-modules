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

# payment-daemon is distroless and runs as uid 65532. Operator keystores are
# correctly 0600/operator-owned, so direct bind mounts would be unreadable.
# Stage private copies under ignored run/ without weakening the originals.
mkdir -p run/payment-secrets
chmod 0700 run/payment-secrets
docker run --rm --user 0 \
  -v "$PWD/run/payment-secrets:/state" \
  -v "$PAYER_KEYSTORE:/source/payer-keystore.json:ro" \
  -v "$PAYER_KEYSTORE_PASSWORD_FILE:/source/payer-keystore-password:ro" \
  -v "$PAYEE_KEYSTORE:/source/payee-keystore.json:ro" \
  -v "$PAYEE_KEYSTORE_PASSWORD_FILE:/source/payee-keystore-password:ro" \
  --entrypoint sh "${BROKER_IMAGE:?set BROKER_IMAGE}" -c '
    cp /source/payer-keystore.json /state/payer-keystore.json
    cp /source/payer-keystore-password /state/payer-keystore-password
    cp /source/payee-keystore.json /state/payee-keystore.json
    cp /source/payee-keystore-password /state/payee-keystore-password
    chown 65532:65532 /state/*
    chmod 0400 /state/*
  '
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
# The conformance fixture owns a single runner-attach WebSocket and does not
# reconnect it after a broker replacement. Recreate it on every explicit stack
# bring-up so persisted frozen offers cannot mask a detached backend.
"${compose[@]}" up -d --force-recreate runner
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
