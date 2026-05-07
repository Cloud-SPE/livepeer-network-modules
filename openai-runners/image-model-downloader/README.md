# image-model-downloader

One-shot Docker image that pre-downloads HuggingFace diffusers / Whisper /
Kokoro models into a shared volume. Run once per host before the actual
runner containers start; not needed during normal operation.

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `MODEL_IDS` | `SG161222/RealVisXL_V4.0_Lightning` | Comma-separated list of HF model IDs |
| `MODEL_DIR` | `/models` | Download directory |
| `HF_TOKEN` | — | HuggingFace token for gated models |

## Usage

```bash
docker run --rm \
  -v ai-image-models:/models \
  -e MODEL_IDS="SG161222/RealVisXL_V4.0_Lightning,black-forest-labs/FLUX.1-dev" \
  tztcloud/image-model-downloader:v0.8.10
```

Or via compose:

```bash
docker compose -f compose/docker-compose.yml run --rm image_model_downloader
```

## Source attribution

Ported from `livepeer-byoc/openai-runners/image-model-downloader/`
(`requirements.txt` → `pyproject.toml`; download.py moved to `src/`).
