#!/usr/bin/env bash
# Start the integration stack. Idempotent: refuses to start a second
# copy rather than racing the first for the same unix sockets.
set -euo pipefail

cd "$(dirname "$0")"
[ -f stack.env ] || { echo "stack.env missing — copy stack.env.example and edit it"; exit 1; }
set -a; . ./stack.env; set +a

RUN_DIR="${RUN_DIR:-$PWD/run}"
BIN_DIR="$RUN_DIR/bin"
PID_FILE="$RUN_DIR/pids"
REPO_ROOT="$(cd ../../.. && pwd)"

if [ -f "$PID_FILE" ] && xargs -r -a "$PID_FILE" ps -p >/dev/null 2>&1; then
  echo "already running (see $PID_FILE); ./down.sh first"
  exit 1
fi
mkdir -p "$RUN_DIR" "$BIN_DIR"
: > "$PID_FILE"

echo "building..."
(cd "$REPO_ROOT/payment-daemon" && go build -o "$BIN_DIR/payment-daemon" ./cmd/livepeer-payment-daemon)
(cd "$REPO_ROOT/capability-broker" && go build -o "$BIN_DIR/capability-broker" ./cmd/livepeer-capability-broker)

# Sealing key for the session store, and the delegated settlement key the
# broker signs records with. Generated once and kept: a new settlement
# key invalidates every record a consumer already holds.
[ -f "$RUN_DIR/seal.key" ] || head -c 32 /dev/urandom | xxd -p -c 64 > "$RUN_DIR/seal.key"
[ -f "$RUN_DIR/settlement.key" ] || head -c 32 /dev/urandom | xxd -p -c 64 > "$RUN_DIR/settlement.key"

start() { # name, then command
  local name="$1"; shift
  "$@" > "$RUN_DIR/$name.log" 2>&1 &
  echo $! >> "$PID_FILE"
  echo "  $name pid=$!"
}

echo "starting payment daemons..."
start payer "$BIN_DIR/payment-daemon" --mode=sender \
  --chain-rpc-urls="$CHAIN_RPC_URLS" \
  --keystore-path="$PAYER_KEYSTORE" \
  --keystore-password-file="$PAYER_KEYSTORE_PASSWORD_FILE" \
  --socket=/tmp/lpm-payer.sock --db="$RUN_DIR/payer.db" \
  --max-payment-wei="$MAX_PAYMENT_WEI" --max-price-per-unit="$MAX_PRICE_PER_UNIT"

start payee "$BIN_DIR/payment-daemon" --mode=receiver \
  --chain-rpc-urls="$CHAIN_RPC_URLS" \
  --keystore-path="$PAYEE_KEYSTORE" \
  --keystore-password-file="$PAYEE_KEYSTORE_PASSWORD_FILE" \
  --socket=/tmp/lpm-payee.sock --db="$RUN_DIR/payee.db" \
  --payee-admin-token="$PAYEE_ADMIN_TOKEN"

echo "starting stub backend..."
start backend python3 ./backend.py

# The broker refuses to start until the payee answers Health, so the
# daemons must be up first.
#
# Wait for the sockets rather than guessing a duration. This was
# `sleep 3`, and against a real chain the payee verifies the chain id and
# reads its wallet balance BEFORE it binds — so startup drifted past
# three seconds and the broker dialled 62ms too early and died. A
# guessed duration is a race with a comment on it; a socket either
# exists or it does not.
wait_for_socket() {
  local path="$1" name="$2"
  for _ in $(seq 1 120); do
    [ -S "$path" ] && return 0
    sleep 0.5
  done
  echo "$name socket $path never appeared; see $RUN_DIR/$name.log" >&2
  return 1
}
echo "waiting for payment daemon sockets..."
wait_for_socket /tmp/lpm-payer.sock payer || exit 1
wait_for_socket /tmp/lpm-payee.sock payee || exit 1

./render-config.sh > "$RUN_DIR/host-config.yaml"
echo "starting broker..."
start broker "$BIN_DIR/capability-broker" --config "$RUN_DIR/host-config.yaml"

echo "waiting for the paid surface..."
for i in $(seq 1 30); do
  if curl -sf "http://127.0.0.1:${PAID_PORT}/healthz" >/dev/null 2>&1; then
    echo
    echo "up. advertising ${EXTERNAL_BASE_URL}"
    echo "  offerings   ${EXTERNAL_BASE_URL}/registry/offerings"
    echo "  logs        $RUN_DIR/*.log"
    exit 0
  fi
  sleep 1
done
echo "broker did not become healthy; see $RUN_DIR/broker.log" >&2
exit 1
