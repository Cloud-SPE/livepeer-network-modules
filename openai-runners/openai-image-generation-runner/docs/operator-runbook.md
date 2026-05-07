# openai-image-generation-runner — operator runbook

Diffusers-based image generation. Supports SDXL, RealVisXL, and FLUX
model families.

## Image build + tag pin

amd64-only per OQ4 (CUDA 12.9 base image).

```bash
docker build -t tztcloud/openai-image-generation-runner:v0.8.10 .
```

Tag **frozen at v0.8.10** per user-memory
`feedback_no_image_version_bumps.md`.

## GPU prerequisites

NVIDIA GPU with Ada or Blackwell architecture recommended (RTX 4090 +
5090):

- Driver: NVIDIA 545+ (CUDA 12.8/12.9 wheels).
- nvidia-container-toolkit installed.
- Compose: `runtime: nvidia`.

VRAM by model:

| Model | Family | VRAM | Notes |
|---|---|---|---|
| `SG161222/RealVisXL_V4.0_Lightning` | RealVisXL (SDXL) | ~6 GB | Lightning variant; 6 inference steps |
| `black-forest-labs/FLUX.1-dev` | FLUX | ~12-16 GB peak | Loads with model_cpu_offload |
| `stabilityai/stable-diffusion-xl-base-1.0` | SDXL | ~8 GB | Standard SDXL |

FLUX needs at least 24 GB VRAM (RTX 4090 / RTX 6000 Ada); see
`offering.yaml` for the full notes.

## DEVICE=cpu fallback

CPU diffusion is impractically slow; the fail-fast probe applies. Set
`DEVICE=cpu` only for development sanity checks.

## Model setup

Pre-pull via the model-downloader (FLUX is gated; supply
`HF_TOKEN`):

```bash
docker run --rm \
  -v ai-image-models:/models \
  -e MODEL_IDS="SG161222/RealVisXL_V4.0_Lightning,black-forest-labs/FLUX.1-dev" \
  -e HF_TOKEN=hf_xxx \
  tztcloud/image-model-downloader:v0.8.10
```

Pre-compile Triton kernels for faster cold starts:

```bash
../setup-models.sh   # one-shot from openai-runners/
```

## Multi-arch matrix per OQ4

| Image | Platforms |
|---|---|
| `openai-image-generation-runner:v0.8.10` | linux/amd64 only |

## Prometheus integration (OQ5)

Set `METRICS_ENABLED=true` to expose `/metrics`. Default-off.

## torch.compile + Triton cache

Set `USE_TORCH_COMPILE=true` to enable kernel compilation (faster
steady-state, slower cold start). The Triton cache lives at
`/cache/triton`; mount a volume to persist across restarts.

## Healthcheck

`GET /healthz` returns 200 once the diffusers pipeline finishes
loading + warmup pass. Warmup runs a 2-step generation at 512x512 to
catch OOM at startup rather than on first real request.

## Capability registration

The orch-coordinator scrapes `GET /options` and
`GET /image-generation/options` per plan 0018.
