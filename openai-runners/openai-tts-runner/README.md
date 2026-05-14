# openai-tts-runner

Python FastAPI workload binary serving `/v1/audio/speech` (Kokoro TTS).

## Endpoints

| Method | Path | Capability |
|---|---|---|
| POST | `/v1/audio/speech` | `openai-audio-speech` |
| GET | `/healthz` | — |
| GET | `/openai-audio-speech/options` | — |
| GET | `/metrics` | opt-in via `METRICS_ENABLED=true` (per OQ5) |

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `CAPABILITY_NAME` | `openai-audio-speech` | Capability identity (per OQ1) |
| `MODEL_ID` | `hexgrad/Kokoro-82M` | Kokoro HF model id |
| `MODEL_DIR` | `/models` | Local model cache |
| `RUNNER_PORT` | `8080` | HTTP bind |
| `DEVICE` | `cuda` | torch device; fail-fast on `cuda` + no GPU (per OQ3) |
| `LANG_CODE` | `a` | Kokoro language code (`a`=American, `b`=British, ...) |
| `MAX_QUEUE_SIZE` | `5` | 429 threshold |
| `MAX_INPUT_CHARS` | `4000` | Max input length |
| `DEFAULT_VOICE` | `af_bella` | Fallback voice when request omits |
| `METRICS_ENABLED` | `false` | Opt-in `/metrics` Prometheus endpoint (per OQ5) |

OpenAI voice names (`alloy`, `echo`, `fable`, ...) are mapped to Kokoro
voices automatically; see `offering.yaml` for the full alias table. The
`/openai-audio-speech/options` endpoint returns the default voice plus the
native voice inventory and alias map so the broker can publish them into
manifest `extra.voices`.

## Build

amd64-only per OQ4. Requires GPU; CPU fallback via `DEVICE=cpu`.

```bash
docker build -t tztcloud/openai-tts-runner:v0.8.10 .
```

## Source attribution

Ported from `livepeer-byoc/openai-runners/openai-tts-runner/app.py`
(refactored: split FastAPI app + kokoro_loader + gpu_probe;
`requirements.txt` → `pyproject.toml`; added GPU fail-fast probe per OQ3
+ opt-in /metrics endpoint per OQ5).
