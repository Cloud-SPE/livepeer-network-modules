#!/usr/bin/env bash
# Smoke test for the capability broker image.
#
# Boots the released image on the shipped example config and checks that
# its unpaid surfaces answer and its refusals are the documented ones.
#
# WHAT THIS DOES NOT DO, and why: it does not run a paid exchange. In the
# offer-only grammar an offer is not sellable until a runner attaches and
# certifies against it — the transports, work unit and extractor a paid
# request is checked against are the RUNNER's declarations, frozen at
# certification, not config. So a paid happy path needs a runner image
# attached on this network, and the broker release does not ship one.
#
# That is not a gap in coverage, only in this script: the paid path,
# including a real runner attaching over the tunnel, is exercised by the
# conformance suite (livepeer-network-protocol/conformance — `make
# conformance`, or its docker-network example) and by the repo's e2e
# module. What is left here is the thing neither of those checks: that
# the PUBLISHED IMAGE boots on the config operators are handed.
#
# The most load-bearing assertion below is the 404 on a declared offering.
# An offer nobody has certified is not served, and a broker that answered
# anything else would be selling work it cannot route.
#
# Per core belief #15: this script requires only docker on the host.
#
# Usage:
#   ./scripts/smoke.sh
#   IMAGE=tztcloud/livepeer-capability-broker:v2.0.0 ./scripts/smoke.sh

set -euo pipefail

IMAGE="${IMAGE:-tztcloud/livepeer-capability-broker:v2.0.0}"
NETWORK="${NETWORK:-lcb-smoke}"
BROKER="${BROKER:-lcb-smoke-broker}"
HOST_PORT_PAID="${HOST_PORT_PAID:-18080}"
HOST_PORT_METRICS="${HOST_PORT_METRICS:-19090}"

PASS=0
FAIL=0

cleanup() {
  echo "==> cleaning up"
  docker rm -f "$BROKER" >/dev/null 2>&1 || true
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
}
trap cleanup EXIT

assert_eq() {
  local label=$1
  local expected=$2
  local actual=$3
  if [ "$actual" = "$expected" ]; then
    echo "  PASS: $label"
    PASS=$((PASS+1))
  else
    echo "  FAIL: $label — expected '$expected', got '$actual'"
    FAIL=$((FAIL+1))
  fi
}

assert_ge() {
  local label=$1
  local minimum=$2
  local actual=$3
  if [ "$actual" -ge "$minimum" ] 2>/dev/null; then
    echo "  PASS: $label ($actual)"
    PASS=$((PASS+1))
  else
    echo "  FAIL: $label — expected ≥ $minimum, got '$actual'"
    FAIL=$((FAIL+1))
  fi
}

assert_contains() {
  local label=$1
  local needle=$2
  local haystack=$3
  case "$haystack" in
    *"$needle"*)
      echo "  PASS: $label"
      PASS=$((PASS+1))
      ;;
    *)
      echo "  FAIL: $label — expected to contain '$needle'"
      FAIL=$((FAIL+1))
      ;;
  esac
}

# Every paid request needs its own Livepeer-Request-Id: it is the idempotency
# key, and the broker requires it (it is never synthesized server-side).
rid() { printf 'smoke-%s-%s' "$1" "$(date +%s%N)"; }

echo "==> building broker image: $IMAGE"
# Build context is the monorepo root; the broker's go.mod has a `replace`
# directive that references the sibling proto-go module.
docker build --build-arg VERSION=smoke -t "$IMAGE" -f Dockerfile .. >/dev/null

echo "==> creating private network: $NETWORK"
docker network create "$NETWORK" >/dev/null

# The shipped example config, with payment_daemon switched to mock so the
# run is self-contained (no payment-daemon sidecar). Nothing else is
# rewritten: there are no backend URLs left to point anywhere, which is
# the whole point of the offer-only grammar — the broker reaches runners
# down their attach tunnel and nowhere else.
#
# chmod 0644 so the distroless `nonroot` user inside the broker container
# can read the bind-mounted file (mktemp defaults to 0600).
SMOKE_CFG=$(mktemp)
sed "s|^  # mock: true|  mock: true|" \
  examples/host-config.example.yaml > "$SMOKE_CFG"
chmod 0644 "$SMOKE_CFG"

# The sealing key the credential store needs. The example config names
# it and the broker refuses to start without it, which is correct — a
# credential store with no key would hold attach secrets in the clear —
# so generating one is part of standing a broker up, not an extra.
SMOKE_KEY=$(mktemp)
head -c 32 /dev/urandom > "$SMOKE_KEY"
chmod 0644 "$SMOKE_KEY"

# Somewhere for the credential store and the frozen-offer state to live.
# The example config puts them under /var/lib/livepeer/broker/, and the
# stores do not create their own parent — the config says so, under "what
# this file expects to exist". The image ships /var/lib/livepeer but not
# that subdirectory, so a run without this dies at boot on a missing
# path, which is exactly what an operator following the example would
# hit. 0777 because the image runs as nonroot (uid 65532).
SMOKE_STATE=$(mktemp -d)
chmod 0777 "$SMOKE_STATE"

# Pre-encoded base64 Livepeer-Payment envelopes (livepeer.payments.v1.Payment
# protobuf bytes). Generated once via `go run` of a tiny encoder; the (cap,
# offering, max, ticket) tuples never change for this smoke run.
#
#   PAY_HAPPY: cap=kibble:doggo-bark-counter:v1, off=default, max=1000,
#              ticket="smoke-stub" — used for the paid-job exchanges.
#   PAY_UNKNOWN: cap=nonexistent:cap, off=default, max=1000,
#              ticket="smoke-stub" — used for the capability_not_served test.
PAY_HAPPY="ChxraWJibGU6ZG9nZ28tYmFyay1jb3VudGVyOnYxEgdkZWZhdWx0GOgHIgpzbW9rZS1zdHVi"
PAY_UNKNOWN="Cg9ub25leGlzdGVudDpjYXASB2RlZmF1bHQY6AciCnNtb2tlLXN0dWI="

echo "==> starting broker on :$HOST_PORT_PAID (paid) + :$HOST_PORT_METRICS (metrics)"
docker run -d --name "$BROKER" --network "$NETWORK" \
  -p "${HOST_PORT_PAID}:8080" \
  -p "${HOST_PORT_METRICS}:9090" \
  -e BROKER_ADMIN_TOKEN="${BROKER_ADMIN_TOKEN:-smoke-admin-token}" \
  -v "$SMOKE_CFG:/etc/livepeer/host-config.yaml:ro" \
  -v "$SMOKE_KEY:/etc/livepeer/broker-seal.key:ro" \
  -v "$SMOKE_STATE:/var/lib/livepeer/broker" \
  "$IMAGE" \
  --config /etc/livepeer/host-config.yaml >/dev/null

# Wait for the broker to answer. Nothing else has to come up: with no
# runner attached there is nothing to be ready WITH, which is the state
# every assertion below is written against.
for i in $(seq 1 40); do
  if [ "$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:${HOST_PORT_PAID}/healthz" 2>/dev/null)" = "200" ]; then
    break
  fi
  sleep 0.5
done

echo
echo "==> assertions"

# 1. unpaid surfaces
status=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:${HOST_PORT_PAID}/healthz")
assert_eq "GET /healthz returns 200" "200" "$status"

status=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:${HOST_PORT_PAID}/registry/offerings")
assert_eq "GET /registry/offerings returns 200" "200" "$status"

# Nothing is advertised: both offers in the example config are unfrozen,
# because no runner has certified against either. A broker that listed
# them here would be publishing a manifest entry a gateway could route to
# and nothing could serve.
body=$(curl -s "http://localhost:${HOST_PORT_PAID}/registry/offerings")
assert_contains "  manifest carries the orch address" '"orch_eth_address"' "$body"
listed=$(printf '%s' "$body" | grep -c 'kibble:doggo-bark-counter:v1' || true)
assert_eq "  uncertified offering is NOT advertised" "0" "$listed"

status=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:${HOST_PORT_PAID}/registry/health")
assert_eq "GET /registry/health returns 200" "200" "$status"

# 2. the offer-only grammar's central rule: a declared offering with no
#    certified runner is not served. This is the assertion worth having.
resp=$(curl -s -i -X POST "http://localhost:${HOST_PORT_PAID}/v1/job" \
  -H "Livepeer-Capability: kibble:doggo-bark-counter:v1" \
  -H "Livepeer-Offering: default" \
  -H "Livepeer-Payment: ${PAY_HAPPY}" \
  -H "Livepeer-Protocol: paid-job/v1" \
  -H "Livepeer-Request-Id: $(rid uncertified)" -d '{}')
status=$(echo "$resp" | head -1 | awk '{print $2}')
assert_eq "POST /v1/job (declared but uncertified offering) returns 404" "404" "$status"
err=$(echo "$resp" | grep -i "^Livepeer-Error:" | tr -d '\r' | awk '{print $2}')
assert_eq "  Livepeer-Error == capability_not_served" "capability_not_served" "$err"

# 3. a capability the config never declared is the same refusal — a
#    caller cannot tell an unconfigured offering from an uncertified one,
#    and does not need to: neither is for sale.
resp=$(curl -s -i -X POST "http://localhost:${HOST_PORT_PAID}/v1/job" \
  -H "Livepeer-Capability: nonexistent:cap" \
  -H "Livepeer-Offering: default" \
  -H "Livepeer-Payment: ${PAY_UNKNOWN}" \
  -H "Livepeer-Protocol: paid-job/v1" \
  -H "Livepeer-Request-Id: $(rid unknown)" -d '{}')
status=$(echo "$resp" | head -1 | awk '{print $2}')
assert_eq "POST /v1/job (unknown capability) returns 404" "404" "$status"
err=$(echo "$resp" | grep -i "^Livepeer-Error:" | tr -d '\r' | awk '{print $2}')
assert_eq "  Livepeer-Error == capability_not_served" "capability_not_served" "$err"

# 4. protocol is checked before anything else, so this answers 505 even
#    with nothing certified — a gateway speaking the wrong protocol needs
#    to hear that, not "not served".
resp=$(curl -s -i -X POST "http://localhost:${HOST_PORT_PAID}/v1/job" \
  -H "Livepeer-Capability: kibble:doggo-bark-counter:v1" \
  -H "Livepeer-Offering: default" \
  -H "Livepeer-Payment: ${PAY_HAPPY}" \
  -H "Livepeer-Protocol: paid-session/v1" \
  -H "Livepeer-Request-Id: $(rid proto)" -d '{}')
status=$(echo "$resp" | head -1 | awk '{print $2}')
assert_eq "POST /v1/job (protocol mismatch) returns 505" "505" "$status"
err=$(echo "$resp" | grep -i "^Livepeer-Error:" | tr -d '\r' | awk '{print $2}')
assert_eq "  Livepeer-Error == protocol_unsupported" "protocol_unsupported" "$err"

# 5. missing the idempotency key is a client error, never a synthesized id
status=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:${HOST_PORT_PAID}/v1/job" \
  -H "Livepeer-Capability: kibble:doggo-bark-counter:v1" \
  -H "Livepeer-Offering: default" \
  -H "Livepeer-Payment: ${PAY_HAPPY}" \
  -H "Livepeer-Protocol: paid-job/v1" -d '{}')
assert_eq "POST /v1/job (no Livepeer-Request-Id) returns 400" "400" "$status"

# 6. metrics endpoint on the metrics listener
status=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:${HOST_PORT_METRICS}/metrics")
assert_eq "GET /metrics returns 200 (metrics listener)" "200" "$status"

rm -rf "$SMOKE_CFG" "$SMOKE_KEY" "$SMOKE_STATE"

echo
echo "==> result: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -gt 0 ] && exit 1
exit 0
