#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
docker compose --env-file stack.env -f compose.yaml down
echo "containers removed; durable payer, payee, and broker volumes retained"
