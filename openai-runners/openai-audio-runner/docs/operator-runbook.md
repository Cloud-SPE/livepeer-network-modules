# openai-audio-runner — operator runbook

Whisper-based speech-to-text (transcriptions + translations). Loads
`openai/whisper-large-v3` at startup.

## Image build + tag pin

amd64-only per OQ4 (NVIDIA arm64 GPU support exists on Jetson + GH200
but isn't the default deployment shape).

```bash
docker build -t tztcloud/openai-audio-runner:v1.1.0 .
```

Default tag: **v1.1.0**.

## GPU prerequisites

NVIDIA GPU with CUDA 12.x:

- Driver: NVIDIA 535+ recommended (matches CUDA 12.9 base image).
- nvidia-container-toolkit installed on the host.
- Compose: `runtime: nvidia` plus `--gpus all` for plain `docker run`.

Whisper-large-v3 needs ~3-4 GB VRAM in bfloat16; runs comfortably on
Pascal+ (per the rewrite-wide GPU lock at
`docs/design-docs/gpu-requirements.md`).

## DEVICE=cpu fallback

If no GPU is detected, the runner exits non-zero at startup with the
fail-fast message:

```
cuda device requested but no GPU detected; set DEVICE=cpu to fall
back to CPU runtime
```

Operator-side fallback: `DEVICE=cpu`. Note CPU inference is ~50x
slower; treat as last-resort.

## Model setup

Pre-pull weights into a shared volume via the
`image-model-downloader` image (or run the runner once and let it pull
via HuggingFace; the `/models` volume persists the cache):

```bash
docker run --rm \
  -v ai-whisper-models:/models \
  -e MODEL_IDS="openai/whisper-large-v3" \
  tztcloud/image-model-downloader:v1.1.0
```

Whisper-large-v3 is ~3 GB on disk.

## Multi-arch matrix per OQ4

| Image | Platforms |
|---|---|
| `openai-audio-runner:v1.1.0` | linux/amd64 only |

## Prometheus integration (OQ5)

Set `METRICS_ENABLED=true` to expose `/metrics`. Default-off; zero
overhead when unset. Cardinality-capped to `model` + `offering` labels.

## Healthcheck

`GET /healthz` returns 200 + `{"status":"ok","model":...,"device":...}`
once the model finishes loading + warmup. The container's HEALTHCHECK
hits this endpoint.

## Capability registration

The orch-coordinator scrapes
`GET /openai-audio-transcriptions/options` and
`GET /openai-audio-translations/options` per plan 0018.
