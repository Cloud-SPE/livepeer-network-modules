# GPU requirements

Cross-cutting GPU prerequisites for the workload runners. The rewrite
standardizes on **NVIDIA Pascal (sm_60) and newer** as the floor;
Intel and AMD passthrough is preserved for the video transcode family
where the codec stack supports it.

This doc binds three components: `openai-runners/`, `rerank-runner/`,
`video-runners/`. It supersedes per-runner GPU notes and matches plan
0011-followup §5.2 on the broker-side FFmpeg pipeline.

## NVIDIA — primary path

Pascal+ is the rewrite-wide minimum. Recommended:

| Family | sm_arch | Driver minimum | Notes |
|---|---|---|---|
| Pascal | sm_60–sm_61 | NVIDIA 510 | Floor for ML runners; covers GTX 1080, P100 |
| Volta | sm_70 | NVIDIA 510 | V100 |
| Turing | sm_75 | NVIDIA 535 | T4, RTX 2080; first-gen NVENC HEVC + AV1 (decode only) |
| Ampere | sm_80–sm_86 | NVIDIA 535 | A100, RTX 3090; widely deployed |
| Ada | sm_89 | NVIDIA 545 | RTX 4090, RTX 6000 Ada; AV1 NVENC; primary FLUX target |
| Hopper | sm_90 | NVIDIA 545 | H100 |
| Blackwell | sm_120 | NVIDIA 555 | RTX 5090; CUDA 12.8 wheels required |

CUDA toolkit alignment per runner (baked into the runtime image):

| Runner | CUDA base image |
|---|---|
| `openai-runners/openai-audio-runner` | `nvidia/cuda:12.9.1-runtime-ubuntu24.04` via `python-gpu-media-runner-base` |
| `openai-runners/openai-tts-runner` | `nvidia/cuda:12.9.1-runtime-ubuntu24.04` via `python-gpu-media-runner-base` |
| `openai-runners/openai-image-generation-runner` | `nvidia/cuda:12.9.1-runtime-ubuntu24.04` |
| `rerank-runner` | `nvidia/cuda:12.9.1-runtime-ubuntu24.04` |
| `video-runners/transcode-runner` (nvidia target) | `nvidia/cuda:12.9.1-runtime-ubuntu24.04` |
| `video-runners/abr-runner` (nvidia target) | `nvidia/cuda:12.9.1-runtime-ubuntu24.04` |

Operator setup:

```bash
# nvidia-container-toolkit (required for all NVIDIA runners)
sudo apt-get install -y nvidia-container-toolkit
sudo systemctl restart docker
```

Verify:

```bash
docker run --rm --gpus all nvidia/cuda:12.9.1-runtime-ubuntu24.04 nvidia-smi
```

## Intel — video transcode only

Intel QSV (oneVPL) + VAAPI is supported for `transcode-runner` and
`abr-runner` (per OQ4 multi-vendor lock for the Go video runners).

Operator setup:

```bash
sudo apt-get install -y intel-media-va-driver-non-free libvpl2
ls /dev/dri  # expect renderD128
```

Compose:

```yaml
devices:
  - /dev/dri:/dev/dri
```

ML runners (audio, TTS, image generation, rerank) are NVIDIA-only;
Intel ARC + Intel iGPU support is out of scope for v0.1.

## AMD — video transcode only

AMD VAAPI is supported for `transcode-runner` and `abr-runner`.

Operator setup:

```bash
sudo apt-get install -y mesa-va-drivers libva2
ls /dev/dri  # expect renderD128
```

Compose: same `/dev/dri` passthrough as Intel.

## CPU fallback (DEVICE=cpu)

All ML runners (`openai-audio-runner`, `openai-tts-runner`,
`openai-image-generation-runner`, `rerank-runner`) implement OQ3:
fail-fast on `DEVICE=cuda` + no GPU. Operator-side fallback:
`DEVICE=cpu`.

CPU performance per runner (rough, 32-core EPYC reference):

| Runner | GPU baseline | CPU fallback | Slowdown |
|---|---|---|---|
| Whisper-large-v3 | ~1x realtime on RTX 4090 | ~50x slower | impractical for live transcription |
| Kokoro-82M | ~10x realtime on RTX 4090 | ~10x slower | acceptable for batch |
| RealVisXL_V4.0_Lightning | ~3s per image on RTX 4090 | ~5 min per image | impractical |
| FLUX.1-dev | ~30s per image on RTX 4090 | ~30 min per image | impractical |
| zerank-2 | ~50ms per doc on RTX 4090 | ~3s per doc | feasible for small batches |
| transcode-runner / abr-runner | NVENC realtime + multi-stream | x264 software 1-3x realtime | feasible single-stream |

The Go-based `openai-runner/` (chat + embeddings proxy) is pure-Go and
has no GPU dependency — multi-arch (amd64 + arm64) per OQ4.

## Multi-arch policy (per OQ4)

| Component | Platforms |
|---|---|
| `openai-runners/openai-runner` (Go proxy) | linux/amd64 + linux/arm64 |
| All other runners | linux/amd64 only |

NVIDIA arm64 GPU support exists (Jetson, GH200) but isn't the default
operator deployment shape; ML runners stay amd64-only for v0.1.

## Validation

Each runner exposes a fail-fast probe at startup. The exit message
when `DEVICE=cuda` is set without a GPU:

```
cuda device requested but no GPU detected; set DEVICE=cpu to fall
back to CPU runtime
```

This deliberately rejects silent degradation — operators must
explicitly opt into CPU mode (per OQ3 lock).
