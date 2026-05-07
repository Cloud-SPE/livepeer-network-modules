#!/usr/bin/env bash
set -euo pipefail

# Smoke: POST a string, assert 200 + audio body bytes.

PORT="${PORT:-8080}"
HOST="${HOST:-localhost}"

response_size=$(curl -sf -X POST "http://${HOST}:${PORT}/v1/audio/speech" \
  -H "Content-Type: application/json" \
  -d '{"input":"Hello world","voice":"alloy","response_format":"wav"}' \
  | wc -c)

if [ "${response_size}" -lt 100 ]; then
  echo "audio body too small: ${response_size} bytes" >&2
  exit 1
fi
echo "tts smoke passed (${response_size} bytes)"
