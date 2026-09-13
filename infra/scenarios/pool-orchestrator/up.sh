#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"
env_file="${ENV_FILE:-.env}"
[ -f "$env_file" ] || { echo "$env_file missing — copy .env.example and edit it" >&2; exit 1; }
set -a
. "$env_file"
set +a
config_path="${POOL_BROKER_CONFIG:-./run/broker-host-config.yaml}"
mkdir -p "$(dirname "$config_path")"
umask 077
temporary="$(mktemp "$(dirname "$config_path")/.broker-host-config.XXXXXX")"
trap 'rm -f "$temporary"' EXIT
ENV_FILE="$env_file" ./render-broker-config.sh > "$temporary"
chmod 0644 "$temporary"
mv "$temporary" "$config_path"
ENV_FILE="$env_file" ./preflight.sh
docker compose --env-file "$env_file" -f docker-compose.yml up -d "$@"
