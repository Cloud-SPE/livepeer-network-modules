#!/usr/bin/env bash
# Integration tests for the rerank-runner.
#
# Usage:
#   ./test.sh
#
# Environment:
#   RERANK_BASE_URL  Base URL (default: http://localhost:8081); works with
#                    direct runner or broker proxy.

set -uo pipefail

BASE_URL="${RERANK_BASE_URL:-http://localhost:8081}"

PASSED=0
FAILED=0

pass() { echo "  PASS: $1"; PASSED=$((PASSED + 1)); }
fail() { echo "  FAIL: $1 — $2"; FAILED=$((FAILED + 1)); }

test_healthz() {
  local name="Health check"
  local resp status body

  resp=$(curl -s -w "\n%{http_code}" --max-time 10 "${BASE_URL}/healthz") || { fail "$name" "curl failed"; return; }
  status=$(echo "$resp" | tail -1)
  body=$(echo "$resp" | sed '$d')

  [[ "$status" != "200" ]] && { fail "$name" "Expected 200, got $status"; return; }

  if echo "$body" | jq -e '.' >/dev/null 2>&1; then
    echo "$body" | jq -e '.status == "ok"' >/dev/null 2>&1 || { fail "$name" "Expected status=ok"; return; }
    echo "$body" | jq -e '.model' >/dev/null 2>&1 || { fail "$name" "Missing 'model' in response"; return; }
  else
    [[ "$body" != "ok" ]] && { fail "$name" "Expected 'ok' or JSON with status=ok, got: $body"; return; }
  fi
  pass "$name"
}

test_basic_rerank() {
  local name="Basic rerank"
  local resp status body

  resp=$(curl -s -w "\n%{http_code}" --max-time 60 -X POST "${BASE_URL}/v1/rerank" \
    -H "Content-Type: application/json" \
    -d '{
      "query": "What is deep learning?",
      "documents": [
        "Deep learning is a subfield of machine learning that uses artificial neural networks with multiple layers to progressively extract higher-level features from raw input.",
        "The capital of France is Paris, a city known for the Eiffel Tower and its rich cultural heritage.",
        "Neural networks are computing systems inspired by biological neural networks that constitute animal brains."
      ]
    }') || { fail "$name" "curl failed"; return; }
  status=$(echo "$resp" | tail -1)
  body=$(echo "$resp" | sed '$d')

  [[ "$status" != "200" ]] && { fail "$name" "Expected 200, got $status: $body"; return; }
  echo "$body" | jq -e '.results' >/dev/null 2>&1 || { fail "$name" "Missing 'results'"; return; }
  echo "$body" | jq -e '.id' >/dev/null 2>&1 || { fail "$name" "Missing 'id'"; return; }
  echo "$body" | jq -e '.meta' >/dev/null 2>&1 || { fail "$name" "Missing 'meta'"; return; }

  local count
  count=$(echo "$body" | jq '.results | length') || { fail "$name" "Failed to parse results"; return; }
  [[ "$count" != "3" ]] && { fail "$name" "Expected 3 results, got $count"; return; }

  local sorted
  sorted=$(echo "$body" | jq '[.results[].relevance_score] | . == (. | sort | reverse)') || { fail "$name" "Failed to check sort order"; return; }
  [[ "$sorted" != "true" ]] && { fail "$name" "Results not sorted by score descending"; return; }

  pass "$name"
}

test_top_n() {
  local name="top_n parameter"
  local resp status body

  resp=$(curl -s -w "\n%{http_code}" --max-time 60 -X POST "${BASE_URL}/v1/rerank" \
    -H "Content-Type: application/json" \
    -d '{
      "query": "What is deep learning?",
      "documents": [
        "Deep learning uses neural networks.",
        "The weather is nice.",
        "Machine learning is a field of AI."
      ],
      "top_n": 2
    }') || { fail "$name" "curl failed"; return; }
  status=$(echo "$resp" | tail -1)
  body=$(echo "$resp" | sed '$d')

  [[ "$status" != "200" ]] && { fail "$name" "Expected 200, got $status: $body"; return; }

  local count
  count=$(echo "$body" | jq '.results | length')
  [[ "$count" != "2" ]] && { fail "$name" "Expected 2 results with top_n=2, got $count"; return; }

  pass "$name"
}

test_empty_documents_error() {
  local name="Empty documents → 400"
  local resp status

  resp=$(curl -s -w "\n%{http_code}" --max-time 10 -X POST "${BASE_URL}/v1/rerank" \
    -H "Content-Type: application/json" \
    -d '{
      "query": "What is deep learning?",
      "documents": []
    }') || { fail "$name" "curl failed"; return; }
  status=$(echo "$resp" | tail -1)

  [[ "$status" != "400" ]] && { fail "$name" "Expected 400 for empty documents, got $status"; return; }

  pass "$name"
}

test_empty_query_error() {
  local name="Empty query → 400"
  local resp status

  resp=$(curl -s -w "\n%{http_code}" --max-time 10 -X POST "${BASE_URL}/v1/rerank" \
    -H "Content-Type: application/json" \
    -d '{
      "query": "",
      "documents": ["Some document."]
    }') || { fail "$name" "curl failed"; return; }
  status=$(echo "$resp" | tail -1)

  [[ "$status" != "400" ]] && { fail "$name" "Expected 400 for empty query, got $status"; return; }

  pass "$name"
}

echo "Running rerank integration tests against ${BASE_URL}"
echo

test_healthz
test_basic_rerank
test_top_n
test_empty_documents_error
test_empty_query_error

echo
echo "Results: ${PASSED} passed, ${FAILED} failed"

[[ "$FAILED" -gt 0 ]] && exit 1
exit 0
