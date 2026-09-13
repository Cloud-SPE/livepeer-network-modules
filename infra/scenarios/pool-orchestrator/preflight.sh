#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"
env_file="${ENV_FILE:-.env}"
[ -f "$env_file" ] || { echo "$env_file missing — copy .env.example and edit it" >&2; exit 1; }
set -a
. "$env_file"
set +a

required=(ORCH_ADDRESS POOL_CONTROLLER_ADMIN_TOKEN BROKER_EXTERNAL_BASE_URL BROKER_SETTLEMENT_KEY BROKER_SEAL_KEY)
missing=()
for name in "${required[@]}"; do
  [ -n "${!name:-}" ] || missing+=("$name")
done
if [ "${#missing[@]}" -ne 0 ]; then
  printf 'missing required pool-orchestrator settings: %s\n' "${missing[*]}" >&2
  exit 1
fi

[[ "$ORCH_ADDRESS" =~ ^0x[0-9a-fA-F]{40}$ ]] || {
  echo "ORCH_ADDRESS is not a 20-byte hex address" >&2
  exit 1
}
[[ "$BROKER_EXTERNAL_BASE_URL" =~ ^https://[A-Za-z0-9.-]+(:[0-9]+)?/?$ ]] || {
  echo "BROKER_EXTERNAL_BASE_URL must be an HTTPS origin without path, query, fragment, or userinfo" >&2
  exit 1
}
[ "${#POOL_CONTROLLER_ADMIN_TOKEN}" -ge 32 ] || {
  echo "POOL_CONTROLLER_ADMIN_TOKEN must contain at least 32 characters" >&2
  exit 1
}

for name in BROKER_SETTLEMENT_KEY BROKER_SEAL_KEY; do
  path="${!name}"
  [ -f "$path" ] && [ -r "$path" ] || {
    echo "$name must name a readable regular file" >&2
    exit 1
  }
done

settlement_key="$(tr -d '[:space:]' < "$BROKER_SETTLEMENT_KEY")"
settlement_key="${settlement_key#0x}"
settlement_key="${settlement_key#0X}"
[[ "$settlement_key" =~ ^[0-9a-fA-F]{64}$ ]] || {
  echo "BROKER_SETTLEMENT_KEY must contain one 32-byte hex secp256k1 private key" >&2
  exit 1
}

seal_compact="$(tr -d '[:space:]' < "$BROKER_SEAL_KEY")"
seal_bytes="$(wc -c < "$BROKER_SEAL_KEY" | tr -d ' ')"
if ! [[ "$seal_compact" =~ ^[0-9a-fA-F]{64}$ ]] && [ "$seal_bytes" -ne 32 ]; then
  echo "BROKER_SEAL_KEY must contain 32 raw bytes or 64 hex characters" >&2
  exit 1
fi

config_path="${POOL_BROKER_CONFIG:-./run/broker-host-config.yaml}"
[ -f "$config_path" ] || {
  echo "$config_path is missing — run ./render-broker-config.sh > $config_path" >&2
  exit 1
}

broker_image="${REGISTRY:-tztcloud}/livepeer-capability-broker:${TAG:-v2.0.0}"
config_abs="$(cd "$(dirname "$config_path")" && pwd)/$(basename "$config_path")"
docker run --rm \
  -v "$config_abs:/tmp/host-config.yaml:ro" \
  "$broker_image" config validate --config /tmp/host-config.yaml
docker run --rm \
  -v "$BROKER_SETTLEMENT_KEY:/tmp/settlement.key:ro" \
  "$broker_image" settlement-key pubkey --file /tmp/settlement.key >/dev/null
docker run --rm --entrypoint /bin/sh \
  -v "$BROKER_SEAL_KEY:/tmp/seal.key:ro" \
  "$broker_image" -c 'test -r /tmp/seal.key' || {
    echo "BROKER_SEAL_KEY is not readable by the broker container user (uid 65532)" >&2
    exit 1
  }
docker compose --env-file "$env_file" -f docker-compose.yml config --quiet

echo "preflight ok: signed v2 pool broker $ORCH_ADDRESS at ${BROKER_EXTERNAL_BASE_URL%/}"
