# openai-tts-runner — operator runbook

Kokoro TTS runner serving `/v1/audio/speech`. Loads
`hexgrad/Kokoro-82M` at startup.

## Image build + tag pin

amd64-only per OQ4.

```bash
docker build -t tztcloud/openai-tts-runner:v0.8.10 .
```

Tag **frozen at v0.8.10** per user-memory
`feedback_no_image_version_bumps.md`.

## GPU prerequisites

NVIDIA GPU recommended (Kokoro is small enough to run on CPU but ~10x
slower). Same setup as `openai-audio-runner`:

- Driver: NVIDIA 535+.
- nvidia-container-toolkit on the host.
- Compose: `runtime: nvidia`.

Kokoro-82M needs ~1 GB VRAM (82-million parameters); fits on any
Pascal+ GPU.

## DEVICE=cpu fallback

Kokoro runs acceptably on CPU; the fail-fast probe still applies if
`DEVICE=cuda` is set explicitly without a GPU available. Operator-side
fallback: `DEVICE=cpu`.

## Model setup

Same pattern as `openai-audio-runner` but smaller (~165 MB on disk):

```bash
docker run --rm \
  -v ai-kokoro-models:/models \
  -e MODEL_IDS="hexgrad/Kokoro-82M" \
  tztcloud/image-model-downloader:v0.8.10
```

## Multi-arch matrix per OQ4

| Image | Platforms |
|---|---|
| `openai-tts-runner:v0.8.10` | linux/amd64 only |

## Prometheus integration (OQ5)

Set `METRICS_ENABLED=true` to expose `/metrics`. Default-off; zero
overhead when unset. Cardinality-capped to `model` + `offering` labels.

## Healthcheck

`GET /healthz` returns 200 once the Kokoro pipeline finishes loading +
warmup synthesis. Warmup runs a 5-character sample and is non-fatal on
failure.

## Capability registration

The orch-coordinator scrapes `GET /openai-audio-speech/options` per
plan 0018.

## Voice mapping

OpenAI voice names (`alloy`, `echo`, `fable`, `onyx`, `nova`, ...) are
mapped to Kokoro voices automatically. See the runner's `offering.yaml`
for the full alias table.
