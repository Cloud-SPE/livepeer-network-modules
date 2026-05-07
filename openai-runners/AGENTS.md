# AGENTS.md

This is `openai-runners/` — the workload-binary tree that serves
OpenAI-shaped HTTP endpoints to the capability broker. Five sub-components:

- `python-runner-base/` — shared Python base image (Phase 1b lock).
- `openai-runner/` — Go proxy for chat + embeddings (Ollama / vLLM upstream).
- `openai-audio-runner/` — Whisper STT (Python).
- `openai-tts-runner/` — Kokoro TTS (Python).
- `openai-image-generation-runner/` — diffusers image generation (Python).
- `image-model-downloader/` — one-shot model downloader image.
- `openai-tester/` — Node integration test harness.

Component-local agent map. The repo-root [`../AGENTS.md`](../AGENTS.md) is the
cross-cutting map; this file scopes to runner-specific guidance.

## Operating principles

Inherited from the repo root (agent-first harness pattern). Plus:

- **Runners are blind to customer identity.** No customer auth, no billing,
  no payment validation. The capability broker authenticates upstream and
  forwards a paid request to the runner; the runner only sees HTTP method
  + path + body + the informational `Livepeer-Capability` /
  `Livepeer-Offering` headers.
- **Capability identity is image-tag-pinned.** Each runner image declares
  its capability via the `CAPABILITY_NAME` env var (per OQ1). Offering
  details live in an embedded YAML manifest at `/etc/runner/offering.yaml`.
- **GPU probe fails fast.** ML runners exit non-zero at startup if
  `DEVICE=cuda` and no GPU is detected (per OQ3); operators set
  `DEVICE=cpu` to fall back.
- **Metrics are opt-in.** `METRICS_ENABLED=true` exposes `/metrics`
  (Prometheus exposition format) per OQ5; default-off, zero overhead.
- **Multi-arch policy.** ML runners ship amd64-only; the Go-based
  `openai-runner/` ships multi-arch (amd64 + arm64) per OQ4.
- **Python runners share a base image.** All Python runners
  (`openai-audio-runner`, `openai-tts-runner`, `openai-image-generation-runner`)
  inherit from `python-runner-base/` per OQ2. The Go-based
  `openai-runner/` is a separate Go runtime; it does not use the Python
  base.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Architectural overview | [`DESIGN.md`](./DESIGN.md) |
| Build / run / smoke gestures | [`Makefile`](./Makefile) |
| Per-backend compose overlays | [`compose/`](./compose/) |
| Multi-image build orchestrator | [`build.sh`](./build.sh) |
| Plan brief | [`../docs/exec-plans/completed/0013-runners-byoc-migration.md`](../docs/exec-plans/completed/0013-runners-byoc-migration.md) |

## Doing work in this component

- **All gestures are Docker-first** (per repo-root core belief #15). Do not
  add steps that require host Python or host Go.
- **Per-runner Dockerfiles inherit the shared Python base.** Add only
  model-specific deps + entrypoints in each runner's Dockerfile; common
  deps (`fastapi`, `pydantic`, `pydantic-settings`, `structlog`,
  `uvicorn`, `prometheus-client`) live in `python-runner-base/`.
- **Capability names are canonical.** Allowed values include
  `openai-chat-completions`, `openai-text-embeddings`,
  `openai-audio-transcriptions`, `openai-audio-translations`,
  `openai-audio-speech`, `image-generation`. One value per image.
- **Image tags are frozen at v0.8.10.** Do not bump without explicit user
  approval (per user-memory `feedback_no_image_version_bumps.md`).
- **No per-runner LICENSE files.** Repo-root MIT applies.

## What lives elsewhere

- `rerank-runner/` — the rerank workload binary, sibling component.
- `video-runners/` — VOD transcode + ABR ladder workload binaries,
  sibling component.
- `capability-broker/` — the orch-side dispatcher that forwards requests
  to runners (broker is the client; runners are the servers).
- Customer auth / billing / ledger — `customer-portal/` + per-product
  gateways (e.g. `openai-gateway/`).
