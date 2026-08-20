#!/usr/bin/env bash
# End-to-end smoke test for the capability broker.
#
# Spins up a mock backend + the broker in Docker on a private network, runs a
# small assertion matrix, prints pass/fail, exits non-zero on any failure.
#
# Exercises the v1 paid-job surface (`POST /v1/job`, protocol `paid-job/v1`).
# The paid-session surface needs a real session runner and a persistent state
# volume, so it is covered by the conformance stack rather than here.
#
# Per core belief #15: this script requires only docker on the host. No Go,
# no Python locally — the mock backend runs in a python:3.12-alpine container.
#
# Usage:
#   ./scripts/smoke.sh
#   IMAGE=tztcloud/livepeer-capability-broker:v2.0.0 ./scripts/smoke.sh

set -euo pipefail

IMAGE="${IMAGE:-tztcloud/livepeer-capability-broker:v2.0.0}"
NETWORK="${NETWORK:-lcb-smoke}"
BROKER="${BROKER:-lcb-smoke-broker}"
BACKEND="${BACKEND:-lcb-smoke-backend}"
HOST_PORT_PAID="${HOST_PORT_PAID:-18080}"
HOST_PORT_METRICS="${HOST_PORT_METRICS:-19090}"

PASS=0
FAIL=0

cleanup() {
  echo "==> cleaning up"
  docker rm -f "$BROKER" "$BACKEND" >/dev/null 2>&1 || true
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

echo "==> starting mock backend on $BACKEND:9000"
# GET serves the health probes (http-status and http-jsonpath both point here
# after the sed below); POST serves the paid exchanges.
docker run -d --name "$BACKEND" --network "$NETWORK" \
  -e PYTHONUNBUFFERED=1 \
  python:3.12-alpine \
  python3 -c '
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
class H(BaseHTTPRequestHandler):
    def _send(self, obj):
        body_json = json.dumps(obj).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body_json)))
        self.end_headers()
        self.wfile.write(body_json)
    def do_GET(self):
        self._send({"ready": True})
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(n) if n else b""
        livepeer = [k for k in self.headers.keys() if k.lower().startswith("livepeer-")]
        self._send({"bark_count": 42, "echo": body.decode("utf-8", errors="replace"),
                    "auth_seen": self.headers.get("Authorization", "<none>"),
                    "livepeer_seen": livepeer})
    def log_message(self, *a, **k): pass
HTTPServer(("0.0.0.0", 9000), H).serve_forever()
' >/dev/null

# Rewrite the example host-config so backend AND health-probe URLs point at the
# named container, and switch `payment_daemon` to mock mode so the smoke test
# stays self-contained (no payment-daemon sidecar required). The real-daemon
# path is exercised end-to-end by the conformance compose stack.
#
# The probe URLs matter: an unhealthy backend is ineligible for selection, so
# a broker whose probes cannot reach the backend answers 503
# capacity_exhausted rather than dispatching.
#
# chmod 0644 so the distroless `nonroot` user inside the broker container can
# read the bind-mounted file (mktemp defaults to 0600).
SMOKE_CFG=$(mktemp)
sed "s|http://localhost:9000|http://${BACKEND}:9000|g; \
     s|http://localhost:9001|http://${BACKEND}:9000|g; \
     s|^  # mock: true|  mock: true|" \
  examples/host-config.example.yaml > "$SMOKE_CFG"
chmod 0644 "$SMOKE_CFG"

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
  -v "$SMOKE_CFG:/etc/livepeer/host-config.yaml:ro" \
  "$IMAGE" \
  --config /etc/livepeer/host-config.yaml >/dev/null

# Wait for end-to-end readiness: not just the broker, but the broker + mock
# backend together, including the first successful health probe (an unprobed
# backend is not selectable). Poll the actual /v1/job path until it returns
# 200. (Python alpine container can take 2-3 seconds to bind its listener.)
for i in $(seq 1 40); do
  status=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:${HOST_PORT_PAID}/v1/job" \
    -H "Livepeer-Capability: kibble:doggo-bark-counter:v1" \
    -H "Livepeer-Offering: default" \
    -H "Livepeer-Payment: ${PAY_HAPPY}" \
    -H "Livepeer-Protocol: paid-job/v1" \
    -H "Livepeer-Request-Id: $(rid warmup-$i)" \
    -d '{}' 2>/dev/null)
  if [ "$status" = "200" ]; then
    break
  fi
  sleep 0.5
done

echo
echo "==> assertions"

# 1. registry endpoints
status=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:${HOST_PORT_PAID}/healthz")
assert_eq "GET /healthz returns 200" "200" "$status"

status=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:${HOST_PORT_PAID}/registry/offerings")
assert_eq "GET /registry/offerings returns 200" "200" "$status"

status=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:${HOST_PORT_PAID}/registry/health")
assert_eq "GET /registry/health returns 200" "200" "$status"

# 2. happy path (paid-job/v1, unary transport)
HAPPY_RID=$(rid happy)
resp=$(curl -s -i -X POST "http://localhost:${HOST_PORT_PAID}/v1/job" \
  -H "Livepeer-Capability: kibble:doggo-bark-counter:v1" \
  -H "Livepeer-Offering: default" \
  -H "Livepeer-Payment: ${PAY_HAPPY}" \
  -H "Livepeer-Protocol: paid-job/v1" \
  -H "Livepeer-Request-Id: ${HAPPY_RID}" \
  -d '{"woof":"hello"}')
status=$(echo "$resp" | head -1 | awk '{print $2}')
assert_eq "POST /v1/job (happy path) returns 200" "200" "$status"
units=$(echo "$resp" | grep -i "^Livepeer-Work-Units:" | tr -d '\r' | awk '{print $2}')
assert_eq "  Livepeer-Work-Units == 42" "42" "$units"
unit_name=$(echo "$resp" | grep -i "^Livepeer-Work-Unit:" | tr -d '\r' | awk '{print $2}')
assert_eq "  Livepeer-Work-Unit == barks" "barks" "$unit_name"
job_id=$(echo "$resp" | grep -ci "^Livepeer-Job-Id:" || true)
assert_ge "  Livepeer-Job-Id present" "1" "$job_id"

# 3. backend confirms Livepeer-* headers stripped + no auth (cap is auth=none)
livepeer_seen=$(echo "$resp" | tail -1 | grep -o '"livepeer_seen": *\[[^]]*\]' || true)
assert_eq "  backend received NO Livepeer-* headers" '"livepeer_seen": []' "$livepeer_seen"

# 4. idempotent replay — same Livepeer-Request-Id converges on the recorded
#    outcome without re-executing the backend or re-charging the payment.
resp=$(curl -s -i -X POST "http://localhost:${HOST_PORT_PAID}/v1/job" \
  -H "Livepeer-Capability: kibble:doggo-bark-counter:v1" \
  -H "Livepeer-Offering: default" \
  -H "Livepeer-Payment: ${PAY_HAPPY}" \
  -H "Livepeer-Protocol: paid-job/v1" \
  -H "Livepeer-Request-Id: ${HAPPY_RID}" \
  -d '{"woof":"hello"}')
status=$(echo "$resp" | head -1 | awk '{print $2}')
assert_eq "POST /v1/job (replayed request id) returns 200" "200" "$status"
assert_contains "  response marks the exchange replayed" '"replayed":true' "$resp"

# 5. unknown capability
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

# 6. wrong protocol for this endpoint
resp=$(curl -s -i -X POST "http://localhost:${HOST_PORT_PAID}/v1/job" \
  -H "Livepeer-Capability: kibble:doggo-bark-counter:v1" \
  -H "Livepeer-Offering: default" \
  -H "Livepeer-Payment: ${PAY_HAPPY}" \
  -H "Livepeer-Protocol: paid-session/v1" \
  -H "Livepeer-Request-Id: $(rid wrongproto)" -d '{}')
status=$(echo "$resp" | head -1 | awk '{print $2}')
assert_eq "POST /v1/job (protocol mismatch) returns 505" "505" "$status"
err=$(echo "$resp" | grep -i "^Livepeer-Error:" | tr -d '\r' | awk '{print $2}')
assert_eq "  Livepeer-Error == protocol_unsupported" "protocol_unsupported" "$err"

# 7. transport the offering does not declare (this one is unary-only)
resp=$(curl -s -i -X POST "http://localhost:${HOST_PORT_PAID}/v1/job" \
  -H "Livepeer-Capability: kibble:doggo-bark-counter:v1" \
  -H "Livepeer-Offering: default" \
  -H "Livepeer-Payment: ${PAY_HAPPY}" \
  -H "Livepeer-Protocol: paid-job/v1" \
  -H "Livepeer-Request-Id: $(rid stream)" \
  -H "Accept: text/event-stream" -d '{}')
status=$(echo "$resp" | head -1 | awk '{print $2}')
assert_eq "POST /v1/job (undeclared transport) returns 400" "400" "$status"
err=$(echo "$resp" | grep -i "^Livepeer-Error:" | tr -d '\r' | awk '{print $2}')
assert_eq "  Livepeer-Error == protocol_transport_unsupported" "protocol_transport_unsupported" "$err"

# 8. missing the idempotency key is a client error, never a synthesized id
status=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:${HOST_PORT_PAID}/v1/job" \
  -H "Livepeer-Capability: kibble:doggo-bark-counter:v1" \
  -H "Livepeer-Offering: default" \
  -H "Livepeer-Payment: ${PAY_HAPPY}" \
  -H "Livepeer-Protocol: paid-job/v1" -d '{}')
assert_eq "POST /v1/job (no Livepeer-Request-Id) returns 400" "400" "$status"

# 9. metrics endpoint on the metrics listener
status=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:${HOST_PORT_METRICS}/metrics")
assert_eq "GET /metrics returns 200 (metrics listener)" "200" "$status"
series=$(curl -s "http://localhost:${HOST_PORT_METRICS}/metrics" | grep -c "^livepeer_protocol_job_exchanges_total" || true)
assert_ge "  livepeer_protocol_job_exchanges_total series count" "1" "$series"

rm -f "$SMOKE_CFG"

echo
echo "==> result: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -gt 0 ] && exit 1
exit 0
