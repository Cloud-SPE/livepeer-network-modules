#!/usr/bin/env bash
# End-to-end smoke test for the capability broker.
#
# Spins up a mock backend + the broker in Docker on a private network, runs a
# small assertion matrix, prints pass/fail, exits non-zero on any failure.
#
# Per core belief #15: this script requires only docker on the host. No Go,
# no Python locally — the mock backend runs in a python:3.12-alpine container.
#
# Usage:
#   ./scripts/smoke.sh
#   IMAGE=tztcloud/livepeer-capability-broker:dev ./scripts/smoke.sh

set -euo pipefail

IMAGE="${IMAGE:-tztcloud/livepeer-capability-broker:dev}"
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

echo "==> building broker image: $IMAGE"
docker build --build-arg VERSION=smoke -t "$IMAGE" -f Dockerfile . >/dev/null

echo "==> creating private network: $NETWORK"
docker network create "$NETWORK" >/dev/null

echo "==> starting mock backend on $BACKEND:9000"
docker run -d --name "$BACKEND" --network "$NETWORK" \
  -e PYTHONUNBUFFERED=1 \
  python:3.12-alpine \
  python3 -c '
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(n) if n else b""
        livepeer = [k for k in self.headers.keys() if k.lower().startswith("livepeer-")]
        resp = {"bark_count": 42, "echo": body.decode("utf-8", errors="replace"),
                "auth_seen": self.headers.get("Authorization", "<none>"),
                "livepeer_seen": livepeer}
        body_json = json.dumps(resp).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body_json)))
        self.end_headers()
        self.wfile.write(body_json)
    def log_message(self, *a, **k): pass
HTTPServer(("0.0.0.0", 9000), H).serve_forever()
' >/dev/null

# Rewrite the example host-config so backend URLs point at the named container.
# chmod 0644 so the distroless `nonroot` user inside the broker container can
# read the bind-mounted file (mktemp defaults to 0600).
SMOKE_CFG=$(mktemp)
sed "s|http://localhost:9000/count|http://${BACKEND}:9000/count|g; \
     s|http://localhost:9001/echo|http://${BACKEND}:9000/echo|g" \
  examples/host-config.example.yaml > "$SMOKE_CFG"
chmod 0644 "$SMOKE_CFG"

echo "==> starting broker on :$HOST_PORT_PAID (paid) + :$HOST_PORT_METRICS (metrics)"
docker run -d --name "$BROKER" --network "$NETWORK" \
  -p "${HOST_PORT_PAID}:8080" \
  -p "${HOST_PORT_METRICS}:9090" \
  -v "$SMOKE_CFG:/etc/livepeer/host-config.yaml:ro" \
  "$IMAGE" \
  --config /etc/livepeer/host-config.yaml >/dev/null

# Wait for end-to-end readiness: not just the broker, but the broker + mock
# backend together. Poll the actual /v1/cap path until it returns 200.
# (Python alpine container can take 2-3 seconds to bind its listener.)
for i in $(seq 1 30); do
  status=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:${HOST_PORT_PAID}/v1/cap" \
    -H "Livepeer-Capability: kibble:doggo-bark-counter:v1" \
    -H "Livepeer-Offering: default" \
    -H "Livepeer-Payment: ready-check" \
    -H "Livepeer-Spec-Version: 0.1" \
    -H "Livepeer-Mode: http-reqresp@v0" \
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

# 2. happy path
resp=$(curl -s -i -X POST "http://localhost:${HOST_PORT_PAID}/v1/cap" \
  -H "Livepeer-Capability: kibble:doggo-bark-counter:v1" \
  -H "Livepeer-Offering: default" \
  -H "Livepeer-Payment: stub-payment" \
  -H "Livepeer-Spec-Version: 0.1" \
  -H "Livepeer-Mode: http-reqresp@v0" \
  -d '{"woof":"hello"}')
status=$(echo "$resp" | head -1 | awk '{print $2}')
assert_eq "POST /v1/cap (happy path) returns 200" "200" "$status"
units=$(echo "$resp" | grep -i "^Livepeer-Work-Units:" | tr -d '\r' | awk '{print $2}')
assert_eq "  Livepeer-Work-Units == 42" "42" "$units"

# 3. backend confirms Livepeer-* headers stripped + no auth (cap is auth=none)
livepeer_seen=$(echo "$resp" | tail -1 | grep -o '"livepeer_seen": *\[[^]]*\]' || true)
assert_eq "  backend received NO Livepeer-* headers" '"livepeer_seen": []' "$livepeer_seen"

# 4. unknown capability
resp=$(curl -s -i -X POST "http://localhost:${HOST_PORT_PAID}/v1/cap" \
  -H "Livepeer-Capability: nonexistent:cap" \
  -H "Livepeer-Offering: default" \
  -H "Livepeer-Payment: stub" \
  -H "Livepeer-Spec-Version: 0.1" \
  -H "Livepeer-Mode: http-reqresp@v0" -d '{}')
status=$(echo "$resp" | head -1 | awk '{print $2}')
assert_eq "POST /v1/cap (unknown capability) returns 404" "404" "$status"
err=$(echo "$resp" | grep -i "^Livepeer-Error:" | tr -d '\r' | awk '{print $2}')
assert_eq "  Livepeer-Error == capability_not_served" "capability_not_served" "$err"

# 5. unsupported mode
resp=$(curl -s -i -X POST "http://localhost:${HOST_PORT_PAID}/v1/cap" \
  -H "Livepeer-Capability: kibble:doggo-bark-counter:v1" \
  -H "Livepeer-Offering: default" \
  -H "Livepeer-Payment: stub" \
  -H "Livepeer-Spec-Version: 0.1" \
  -H "Livepeer-Mode: http-stream@v0" -d '{}')
status=$(echo "$resp" | head -1 | awk '{print $2}')
assert_eq "POST /v1/cap (mode mismatch) returns 505" "505" "$status"

# 6. metrics endpoint on the metrics listener
status=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:${HOST_PORT_METRICS}/metrics")
assert_eq "GET /metrics returns 200 (metrics listener)" "200" "$status"
series=$(curl -s "http://localhost:${HOST_PORT_METRICS}/metrics" | grep -c "^livepeer_mode_requests_total" || true)
assert_ge "  livepeer_mode_requests_total series count" "1" "$series"

rm -f "$SMOKE_CFG"

echo
echo "==> result: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -gt 0 ] && exit 1
exit 0
