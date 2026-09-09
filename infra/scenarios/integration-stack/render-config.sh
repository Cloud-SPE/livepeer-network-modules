#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"
[ -f stack.env ] || { echo "stack.env missing — copy stack.env.example and edit it" >&2; exit 1; }
set -a; . ./stack.env; set +a

: "${ORCH_ETH_ADDRESS:?set ORCH_ETH_ADDRESS}"
: "${EXTERNAL_BASE_URL:?set EXTERNAL_BASE_URL}"
: "${PRICE_WEI:?set PRICE_WEI}"

cat <<YAML
identity:
  orch_eth_address: "${ORCH_ETH_ADDRESS}"
  settlement_key_file: /run/secrets/settlement.key
external_base_url: "${EXTERNAL_BASE_URL%/}"
runner_callback_base_url: "${RUNNER_CALLBACK_BASE_URL%/}"
listen:
  paid: "0.0.0.0:8080"
  metrics: "0.0.0.0:9090"
payment_daemon:
  socket: /var/run/livepeer/payment-daemon.sock
session_store:
  path: /var/lib/livepeer/state.db
  sealing_key_file: /run/secrets/seal.key
  job_retention: 96h
admin_auth:
  method: bearer
  secret_ref: env://BROKER_ADMIN_TOKEN
credential_store:
  path: /var/lib/livepeer/credentials.db
  sealing_key_file: /run/secrets/seal.key
offers_state_path: /var/lib/livepeer/offers.db
offers:
  - offering_id: all
    capability: conformance:job
    protocol: paid-job/v1
    match: { identity.variant: all }
    price: { amount_wei: "${PRICE_WEI}", per_units: ${PER_UNITS:-1000} }
    extra:
      features: { wholesale_accounts: true }
  - offering_id: default
    capability: conformance:session
    protocol: paid-session/v1
    match: { identity.variant: default }
    price: { amount_wei: "${SESSION_PRICE_WEI:-100}", per_units: ${SESSION_PER_UNITS:-1} }
    session_policy:
      refill: extensible
      lease_policy: fixed
      lease_max_seconds: 600
    extra:
      features: { wholesale_accounts: true }
YAML
