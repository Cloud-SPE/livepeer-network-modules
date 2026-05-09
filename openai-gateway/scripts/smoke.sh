#!/usr/bin/env bash
# End-to-end smoke test for the openai-gateway reference impl.
# Spins up gateway + broker + mock-backend via compose, hits the gateway
# with OpenAI-shaped requests, asserts the response shape.
#
# Per core belief #15: requires only docker on the host.

set -euo pipefail

GATEWAY_PORT="${GATEWAY_PORT:-3000}"
COMPOSE="docker compose -f compose.yaml"
ADMIN_TOKEN="${OPENAI_GATEWAY_ADMIN_TOKENS:-change-me-before-production}"
ADMIN_TOKEN="${ADMIN_TOKEN%%,*}"
ADMIN_ACTOR="${OPENAI_GATEWAY_ADMIN_ACTOR:-smoke-admin}"

PASS=0
FAIL=0

cleanup() {
  echo "==> cleaning up"
  $COMPOSE down --remove-orphans >/dev/null 2>&1 || true
  rm -f /tmp/lcb-smoke-*.tmp
}
trap cleanup EXIT

assert_eq() {
  local label=$1
  local expected=$2
  local actual=$3
  if [ "$actual" = "$expected" ]; then
    echo "  PASS: $label"
    PASS=$((PASS + 1))
  else
    echo "  FAIL: $label — expected '$expected', got '$actual'"
    FAIL=$((FAIL + 1))
  fi
}

assert_contains() {
  local label=$1
  local needle=$2
  local file=$3
  if grep -qF "$needle" "$file"; then
    echo "  PASS: $label"
    PASS=$((PASS + 1))
  else
    echo "  FAIL: $label — body did not contain '$needle'"
    FAIL=$((FAIL + 1))
    echo "    body: $(head -c 300 "$file")"
  fi
}

echo "==> bringing up the stack"
$COMPOSE up -d --quiet-pull

echo "==> waiting for gateway readiness"
for _ in $(seq 1 60); do
  if curl -fs "http://localhost:${GATEWAY_PORT}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done
if ! curl -fs "http://localhost:${GATEWAY_PORT}/healthz" >/dev/null 2>&1; then
  echo "  FAIL: gateway never became ready"
  $COMPOSE logs --tail=30
  exit 1
fi

echo
echo "==> assertions"

# 1. Non-streaming chat completions.
status=$(curl -s -o /tmp/lcb-smoke-chat.tmp -w "%{http_code}" \
  -X POST "http://localhost:${GATEWAY_PORT}/v1/chat/completions" \
  -H "Authorization: Bearer test-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"default","messages":[{"role":"user","content":"hi"}],"stream":false}')
assert_eq "POST /v1/chat/completions (non-streaming) returns 200" "200" "$status"
assert_contains "  body has 'choices' field" '"choices"' /tmp/lcb-smoke-chat.tmp
assert_contains "  body has 'usage' field" '"usage"' /tmp/lcb-smoke-chat.tmp

# 2. Streaming chat completions.
status=$(curl -s -o /tmp/lcb-smoke-stream.tmp -w "%{http_code}" \
  -X POST "http://localhost:${GATEWAY_PORT}/v1/chat/completions" \
  -H "Authorization: Bearer test-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"stream","messages":[{"role":"user","content":"hi"}],"stream":true}')
assert_eq "POST /v1/chat/completions (streaming) returns 200" "200" "$status"
assert_contains "  body has SSE 'data:' frames" 'data:' /tmp/lcb-smoke-stream.tmp
assert_contains "  body has [DONE] terminator" '[DONE]' /tmp/lcb-smoke-stream.tmp

# 3. Embeddings.
status=$(curl -s -o /tmp/lcb-smoke-emb.tmp -w "%{http_code}" \
  -X POST "http://localhost:${GATEWAY_PORT}/v1/embeddings" \
  -H "Authorization: Bearer test-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"default","input":"hello"}')
assert_eq "POST /v1/embeddings returns 200" "200" "$status"
assert_contains "  body has 'embedding' field" '"embedding"' /tmp/lcb-smoke-emb.tmp

# 4. Audio transcriptions (multipart).
boundary="----openai-smoke-boundary"
multipart_body=$'--'"$boundary"$'\r\nContent-Disposition: form-data; name="file"; filename="hi.wav"\r\nContent-Type: audio/wav\r\n\r\nfake-audio-bytes\r\n--'"$boundary"$'\r\nContent-Disposition: form-data; name="model"\r\n\r\nwhisper-test\r\n--'"$boundary"$'--\r\n'
echo -n "$multipart_body" > /tmp/lcb-smoke-mp.tmp
status=$(curl -s -o /tmp/lcb-smoke-trans.tmp -w "%{http_code}" \
  -X POST "http://localhost:${GATEWAY_PORT}/v1/audio/transcriptions" \
  -H "Authorization: Bearer test-key" \
  -H "Content-Type: multipart/form-data; boundary=${boundary}" \
  -H "Livepeer-Model: default" \
  --data-binary @/tmp/lcb-smoke-mp.tmp)
assert_eq "POST /v1/audio/transcriptions returns 200" "200" "$status"
assert_contains "  body has 'text' field" '"text"' /tmp/lcb-smoke-trans.tmp

# 5. Admin console API surface.
status=$(curl -s -o /tmp/lcb-smoke-admin-rate-card.tmp -w "%{http_code}" \
  -X GET "http://localhost:${GATEWAY_PORT}/admin/openai/rate-card" \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H "X-Actor: ${ADMIN_ACTOR}")
assert_eq "GET /admin/openai/rate-card returns 200" "200" "$status"
assert_contains "  body has 'chatTiers' field" '"chatTiers"' /tmp/lcb-smoke-admin-rate-card.tmp

echo
echo "==> result: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -gt 0 ] && exit 1
exit 0
