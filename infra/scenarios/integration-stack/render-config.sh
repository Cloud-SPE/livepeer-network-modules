#!/usr/bin/env bash
# Render the broker host-config from stack.env. Kept as a script rather
# than a checked-in yaml so keys and paths are never committed and the
# two offerings stay in one place with the ports they bind.
set -euo pipefail
cd "$(dirname "$0")"
set -a; . ./stack.env; set +a
RUN_DIR="${RUN_DIR:-$PWD/run}"

cat <<YAML
identity:
  orch_eth_address: "${ORCH_ETH_ADDRESS}"
  settlement_key_file: ${RUN_DIR}/settlement.key
external_base_url: "${EXTERNAL_BASE_URL}"
listen:
  paid: "${BIND_ADDR}:${PAID_PORT}"
  metrics: "${BIND_ADDR}:${METRICS_PORT}"
payment_daemon:
  socket: "/tmp/lpm-payee.sock"
session_store:
  path: ${RUN_DIR}/state.db
  sealing_key_file: ${RUN_DIR}/seal.key
  # Evidence retention. LOC's pilot requires at least 96h: long enough to
  # outlast their conservative-charge deadline plus an outage window,
  # because that is when they ask whether an exchange happened.
  job_retention: 96h
capabilities:
  # --- OpenAI gateway: paid-job/v1 ---------------------------------------
  - id: openai:chat-completions
    offering_id: default
    protocol: paid-job/v1
    job:
      transports: [unary, stream]
    health: { initial_status: ready }
    work_unit:
      name: tokens
      extractor: { type: openai-usage }
    price: { amount_wei: "100", per_units: 1000 }
    backend: { transport: http, url: "http://127.0.0.1:9411/v1/chat/completions" }
    extra:
      openai: { model: gpt-oss-20b }
      provider: vllm

  # Transcription: the offering whose ceiling a caller cannot derive from
  # its own request, so it advertises the estimator.
  - id: openai:audio-transcriptions
    offering_id: default
    protocol: paid-job/v1
    job:
      transports: [unary, multipart]
    health: { initial_status: ready }
    work_unit:
      name: seconds
      extractor:
        type: multipart-audio-duration
        file_field: file
        unit: seconds
        default: 0
    price: { amount_wei: "1000", per_units: 1 }
    backend: { transport: http, url: "http://127.0.0.1:9411/v1/audio/transcriptions" }
    extra:
      openai: { model: whisper-1 }
      provider: whisper-cpp
      audio: { task: transcription }

  # --- Meetings: paid-session/v1 -----------------------------------------
  - id: livepeer:meet/sfu-room
    offering_id: default
    protocol: paid-session/v1
    session:
      descriptor_schema: sfu-room/v1
      lease_policy: fixed
      lease_max_seconds: 600
      runner:
        create_path: /sessions
        status_path: "/sessions/{id}"
        terminate_path: "/sessions/{id}"
    health: { initial_status: ready }
    work_unit: { name: participant-seconds }
    price: { amount_wei: "100", per_units: 1000 }
    backend: { transport: http, url: "http://127.0.0.1:9500" }
YAML
