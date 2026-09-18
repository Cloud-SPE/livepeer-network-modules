#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"
env_file="${ENV_FILE:-.env}"
[ -f "$env_file" ] || { echo "$env_file missing — copy .env.example and edit it" >&2; exit 1; }
set -a
. "$env_file"
set +a

: "${ORCH_ADDRESS:?set ORCH_ADDRESS}"
: "${BROKER_EXTERNAL_BASE_URL:?set BROKER_EXTERNAL_BASE_URL}"
[[ "$ORCH_ADDRESS" =~ ^0x[0-9a-fA-F]{40}$ ]] || {
  echo "ORCH_ADDRESS is not a 20-byte hex address" >&2
  exit 1
}
[[ "$BROKER_EXTERNAL_BASE_URL" =~ ^https://[A-Za-z0-9.-]+(:[0-9]+)?/?$ ]] || {
  echo "BROKER_EXTERNAL_BASE_URL must be an HTTPS origin without path, query, fragment, or userinfo" >&2
  exit 1
}

broker_label="${BROKER_LABEL:-pool-orch}"
[[ "$broker_label" =~ ^[A-Za-z0-9._-]+$ ]] || {
  echo "BROKER_LABEL may contain only letters, digits, dot, underscore, and dash" >&2
  exit 1
}

cat <<YAML
identity:
  orch_eth_address: "${ORCH_ADDRESS}"
  label: "${broker_label}"
  settlement_key_file: /etc/livepeer/broker-settlement.key

external_base_url: "${BROKER_EXTERNAL_BASE_URL%/}"

listen:
  paid: ":8080"
  metrics: ":9090"
  attach_quic: ":8443"

admin_auth:
  method: bearer
  secret_ref: env://POOL_CONTROLLER_ADMIN_TOKEN

payment_daemon:
  socket: /var/run/livepeer/payment-daemon.sock

credential_store:
  path: /var/lib/livepeer/credentials.db
  sealing_key_file: /etc/livepeer/broker-seal.key

session_store:
  path: /var/lib/livepeer/state.db
  sealing_key_file: /etc/livepeer/broker-seal.key
  job_retention: 96h

offers_state_path: /var/lib/livepeer/offers.db

receipt_sink:
  url: http://pool-controller:8080
  timeout_ms: 1500
  auth:
    method: bearer
    secret_ref: env://POOL_CONTROLLER_ADMIN_TOKEN

pool_snapshot:
  url: http://pool-controller:8080
  timeout_ms: 1500
  poll_interval_ms: 5000
  stale_after_ms: 15000
  expire_after_ms: 60000
  auth:
    method: bearer
    secret_ref: env://POOL_CONTROLLER_ADMIN_TOKEN

offers_source: admin
offers: []
YAML
