#!/usr/bin/env bash
set -euo pipefail

OUT_FILE="${1:-test.ogg}"
TEXT="${TEXT:-hello from the livepeer openai gateway transcription test}"

ffmpeg -hide_banner -loglevel error \
  -f lavfi \
  -i "flite=text='${TEXT//:/\\:}':voice=slt" \
  -t 5 \
  -c:a libopus \
  -b:a 32k \
  -y \
  "$OUT_FILE"

echo "Generated $OUT_FILE"
