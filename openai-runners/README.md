# openai-runners

Workload binaries that serve OpenAI-shaped HTTP endpoints to the
capability broker. One Docker image per capability; one process per
broker-dispatched container.

> **For agents:** start at [`AGENTS.md`](./AGENTS.md).

## What it is

Five runner images plus a shared Python base:

| Sub-component | Language | Capability |
|---|---|---|
| `python-runner-base/` | Dockerfile-only | shared base for the Python runners (Phase 1b) |
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

**v0.1 scaffold.** Per-runner ports land per [`docs/exec-plans/active/0013-runners-byoc-migration.md`](../docs/exec-plans/active/0013-runners-byoc-migration.md).

## Build

Per repo-root core belief #15, every gesture is Docker-first.

```bash
make build              # build all runner images
make smoke              # cross-runner smoke against compose stack
make help               # show all targets
./build.sh              # multi-image orchestrator
```

No host Python or host Go install required.

## Configuration

Each runner accepts a common set of env vars (`CAPABILITY_NAME`,
`DEVICE`, `METRICS_ENABLED`) plus per-capability keys. See each runner's
own README for the full list.

Offering details (presets, model variants, rate-card hints) live in an
embedded YAML manifest at `/etc/runner/offering.yaml` per image; mount
an override file at the same path to customize.

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
