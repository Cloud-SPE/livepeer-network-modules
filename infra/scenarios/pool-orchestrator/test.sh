#!/usr/bin/env bash
set -euo pipefail

scenario_dir="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
repo_dir="$(CDPATH= cd -- "$scenario_dir/../../.." && pwd)"
image="local/livepeer-capability-broker:lnm-6p2-test"
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT

docker build -t "$image" -f "$repo_dir/capability-broker/Dockerfile" "$repo_dir"
docker run --rm --user 0 -v "$temporary:/keys" "$image" \
  settlement-key generate --out /keys/settlement.key >/dev/null
printf '11%.0s' {1..32} > "$temporary/seal.key"
# The production recommendation is uid 65532 plus mode 0400. World-readable
# temp files let this unprivileged test prove container readability without
# changing ownership on the host; the directory is mode 0700 and is removed.
chmod 0444 "$temporary/settlement.key" "$temporary/seal.key"

cat > "$temporary/valid.env" <<ENV
CHAIN_RPC_URLS=https://arb1.arbitrum.io/rpc
ORCH_ADDRESS=0x1234567890abcdef1234567890abcdef12345678
POOL_CONTROLLER_ADMIN_TOKEN=0123456789abcdef0123456789abcdef
ORCH_COORDINATOR_ADMIN_TOKENS=0123456789abcdef0123456789abcdef
BROKER_EXTERNAL_BASE_URL=https://pool.example.com
BROKER_SETTLEMENT_KEY=$temporary/settlement.key
BROKER_SEAL_KEY=$temporary/seal.key
POOL_BROKER_CONFIG=$temporary/host-config.yaml
REGISTRY=local
TAG=lnm-6p2-test
ENV

ENV_FILE="$temporary/valid.env" "$scenario_dir/render-broker-config.sh" \
  > "$temporary/host-config.yaml"
chmod 0444 "$temporary/host-config.yaml"
ENV_FILE="$temporary/valid.env" "$scenario_dir/preflight.sh"

grep -q 'settlement_key_file: /etc/livepeer/broker-settlement.key' "$temporary/host-config.yaml"
grep -q 'offers_source: admin' "$temporary/host-config.yaml"
grep -q 'offers: \[\]' "$temporary/host-config.yaml"
grep -q 'path: /var/lib/livepeer/state.db' "$temporary/host-config.yaml"

sed "s#BROKER_SETTLEMENT_KEY=.*#BROKER_SETTLEMENT_KEY=$temporary/missing.key#" \
  "$temporary/valid.env" > "$temporary/missing-key.env"
if ENV_FILE="$temporary/missing-key.env" "$scenario_dir/preflight.sh" \
  > "$temporary/missing-key.out" 2>&1; then
  echo "preflight accepted a missing settlement key" >&2
  exit 1
fi
grep -q 'BROKER_SETTLEMENT_KEY must name a readable regular file' "$temporary/missing-key.out"

echo "pool-orchestrator broker bootstrap tests passed"
