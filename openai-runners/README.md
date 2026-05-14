# openai-runners

Workload binaries that serve OpenAI-shaped HTTP endpoints to the
capability broker. One Docker image per capability; one process per
broker-dispatched container.

> **For agents:** start at [`AGENTS.md`](./AGENTS.md).

## What it is

Five runner images plus shared Python bases:

| Sub-component | Language | Capability |
|---|---|---|
| `python-runner-base/` | Dockerfile-only | shared CPU Python base |
| `python-gpu-runner-base/` | Dockerfile-only | shared CUDA Python base for GPU runners |
| `python-gpu-media-runner-base/` | Dockerfile-only | shared CUDA Python media base for audio-style GPU runners |
| `openai-runner/` | Go | `openai-chat-completions` + `openai-text-embeddings` (proxy in front of Ollama / vLLM) |
| `openai-audio-runner/` | Python (FastAPI) | `openai-audio-transcriptions` + `openai-audio-translations` (Whisper) |
| `openai-tts-runner/` | Python (FastAPI) | `openai-audio-speech` (Kokoro TTS) |
| `openai-image-generation-runner/` | Python (FastAPI) | `image-generation` (diffusers) |
| `image-model-downloader/` | Python (one-shot) | pre-pulls model weights into a shared volume |
| `openai-tester/` | Node.js | integration smoke harness across runners |

Each runner exposes:

- `POST <endpoint>` — the capability-specific request entry point.
- `GET /healthz` — 200 ready, 503 during model load.
- `GET /<capability>/options` — scraped by the orch-coordinator (plan
  0018) to build the runtime-discovered capability roster.
- `GET /metrics` — Prometheus exposition (opt-in via `METRICS_ENABLED=true`,
  per OQ5).

## Status

**v0.1 scaffold.** Per-runner ports landed per [`docs/exec-plans/completed/0013-runners-byoc-migration.md`](../docs/exec-plans/completed/0013-runners-byoc-migration.md).

## Build

Per repo-root core belief #15, every gesture is Docker-first.

```bash
make build              # build all runner images
make smoke              # cross-runner smoke against compose stack
make help               # show all targets
./build.sh              # multi-image orchestrator
```

No host Python or host Go install required.

`openai-audio-runner/` and `openai-tts-runner/` build on the media base,
which adds the shared `ffmpeg` runtime once instead of repeating it in both
images.

## Configuration

Each runner accepts a common set of env vars (`CAPABILITY_NAME`,
`DEVICE`, `METRICS_ENABLED`) plus per-capability keys. See each runner's
own README for the full list.

## Compose overlays

Per-backend compose overlays live in [`compose/`](./compose/):

- `compose/docker-compose.yml` — base stack.
- `compose/docker-compose.ollama.yml` — Ollama upstream for chat.
- `compose/docker-compose.vllm.chat.yml` — vLLM upstream for chat.
- `compose/docker-compose.vllm.embeddings.yml` — vLLM upstream for embeddings.
- `compose/docker-compose.audio.yml` — audio + TTS overlay.

## License

MIT — repo-root [`../LICENSE`](../LICENSE) applies; no per-runner LICENSE
files.
