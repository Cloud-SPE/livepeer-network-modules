#!/usr/bin/env bash
set -euo pipefail

# Smoke: POST a small wav, assert 200 + text field.

PORT="${PORT:-8080}"
HOST="${HOST:-localhost}"
FIXTURE="${FIXTURE:-tests/fixture.wav}"

if [ ! -f "${FIXTURE}" ]; then
  echo "Fixture audio not present at ${FIXTURE}; skipping live smoke." >&2
  exit 0
fi

response=$(curl -sf -X POST "http://${HOST}:${PORT}/v1/audio/transcriptions" \
  -F "model=whisper-large-v3" \
  -F "file=@${FIXTURE}")

echo "${response}" | grep -q '"text"' || { echo "missing text field: ${response}" >&2; exit 1; }
echo "audio smoke passed"
