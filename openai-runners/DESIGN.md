# DESIGN

Component-local design summary. Cross-cutting design lives at the repo root in
[`../docs/design-docs/`](../docs/design-docs/).

This file points at the local design and gives a one-page mental model.

## What this component is

A bundle of Docker images that present OpenAI-shaped HTTP endpoints to the
capability broker. Each image is a workload binary that runs as one process
per broker-dispatched container; the broker is the client, the runner is the
server.

```
   capability-broker (orch host)
       │ Livepeer-Mode dispatch
       │ POST /v1/cap → forwards to a configured backend per host-config.yaml
       │
       ├──► /v1/chat/completions      → openai-runner (chat cmd)    → Ollama / vLLM upstream
       ├──► /v1/embeddings            → openai-runner (embed cmd)   → vLLM upstream
       ├──► /v1/audio/transcriptions  → openai-audio-runner          (Whisper)
       ├──► /v1/audio/translations    → openai-audio-runner          (Whisper)
       ├──► /v1/audio/speech          → openai-tts-runner            (Kokoro TTS)
       └──► /v1/images/generations    → openai-image-generation-runner (diffusers)
```

## Mental model

**One image per capability.** The broker forwards a paid request to the
runner; the runner does the work and returns the response. Runners are
stateless: per-job in-memory state plus a per-process model load. No DB.

**Three control knobs per runner** (per OQ1, OQ3, OQ5):

- `CAPABILITY_NAME` — image-tag-pinned canonical capability identity.
- `DEVICE` — torch device for ML runners (default `cuda`; fail-fast if
  no GPU detected).
- `METRICS_ENABLED` — opt-in `/metrics` exposition.

Plus per-capability keys (see the per-runner READMEs).

## Shared Python base images (OQ2)

`python-runner-base/` is the CPU Python base inheriting from
`python:3.12-slim` with the canonical Python pins baked in: `fastapi`,
`pydantic`, `pydantic-settings`, `structlog`, `uvicorn`,
`prometheus-client`.

`python-gpu-runner-base/` is the CUDA Python base inheriting from
`nvidia/cuda:12.9.1-runtime-ubuntu24.04` with the same canonical Python
pins preinstalled in `/opt/venv`.

`python-gpu-media-runner-base/` is a sibling CUDA media base inheriting from
`python-gpu-runner-base:<tag>` and baking in the shared `ffmpeg` runtime used
by the audio-style runners.

The CUDA-backed runner Dockerfiles split into two groups:

- `openai-audio-runner` and `openai-tts-runner` `FROM
  python-gpu-media-runner-base:<tag>` and add only model-specific deps
  plus remaining runner-local OS packages like `espeak-ng`.
- `openai-image-generation-runner` `FROM python-gpu-runner-base:<tag>`
  directly because it does not need the media stack.

The Go-based `openai-runner/` proxy is independent — it uses
`golang:1.22-alpine` for build and `alpine:3.20` for runtime. No shared
base across Go runners.

## What stays out of this component

- **Customer auth + billing.** Lives in `customer-portal/` and per-product
  gateways (e.g. `openai-gateway/`). Runners are blind to customer
  identity.
- **Payment validation.** Broker-side `payment-daemon/` validates
  `Livepeer-Payment` envelopes; the runner sees only fully-authenticated
  requests.
- **Capability registration.** The orch-coordinator (plan 0018) scrapes
  each runner's `GET /<capability>/options` endpoint server-side. No
  sidecar, no register-capabilities binary.
- **Mode dispatch + extractor logic.** Lives in `capability-broker/`.
  Runners see only HTTP requests at their declared endpoint.
- **Wire-protocol middleware.** `gateway-adapters/` is gateway-side; no
  runner-side import.
