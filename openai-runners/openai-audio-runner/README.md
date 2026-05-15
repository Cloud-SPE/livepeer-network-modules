# openai-audio-runner

Python FastAPI workload binary serving `/v1/audio/transcriptions` and
`/v1/audio/translations`. Loads Whisper at startup, keeps the model
warm on GPU.

## Endpoints

| Method | Path | Capability |
|---|---|---|
| POST | `/v1/audio/transcriptions` | `openai-audio-transcriptions` |
| POST | `/v1/audio/translations` | `openai-audio-translations` |
| GET | `/healthz` | — |
| GET | `/openai-audio-transcriptions/options` | — |
| GET | `/openai-audio-translations/options` | — |
| GET | `/metrics` | opt-in via `METRICS_ENABLED=true` (per OQ5) |

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `CAPABILITY_NAME` | `openai-audio-transcriptions` | Capability identity (per OQ1) |
| `MODEL_ID` | `openai/whisper-large-v3` | Whisper HF model id |
| `MODEL_DIR` | `/models` | Local model cache |
| `RUNNER_PORT` | `8080` | HTTP bind |
| `DEVICE` | `cuda` | torch device; fail-fast on `cuda` + no GPU (per OQ3) |
| `DTYPE` | `bfloat16` | torch dtype |
| `MAX_QUEUE_SIZE` | `5` | 429 threshold |
| `MAX_AUDIO_MB` | `50` | Max upload size |
| `CHUNK_LENGTH_S` | `30` | Pipeline chunk length |
| `INFERENCE_BATCH_SIZE` | `16` | Pipeline batch size |
| `METRICS_ENABLED` | `false` | Opt-in `/metrics` Prometheus endpoint (per OQ5) |

Offering details (response formats, sample rate, chunking) live at
`/etc/runner/offering.yaml`.

## Build

amd64-only per OQ4. CUDA 12.9 base image; matches RTX 4090 + 5090
hardware.

```bash
docker build -t tztcloud/openai-audio-runner:v1.1.0 .
```

## Source attribution

Ported from `livepeer-byoc/openai-runners/openai-audio-runner/app.py`
(refactored: split FastAPI app + whisper_loader + gpu_probe;
`requirements.txt` → `pyproject.toml`; added GPU fail-fast probe per OQ3
+ opt-in /metrics endpoint per OQ5).
