# Build system

Cross-cutting build conventions for the workload runners. The rewrite
standardizes on four canonical base images; per-runner Dockerfiles
inherit one of them and add only model-specific deps.

This doc binds three components: `openai-runners/`, `rerank-runner/`,
`video-runners/`. It records the OQ2 lock from plan
0013-runners-byoc-migration.

## Canonical base images

| Base image | Used by | Purpose |
|---|---|---|
| `python:3.12-slim` | `openai-runners/python-runner-base/`, `image-model-downloader`, `rerank-runner/model-downloader/` | Python 3.12 + the canonical FastAPI/pydantic/uvicorn pins. Cached once across CPU-only Python tooling. |
| `golang:1.22-alpine` | `openai-runners/openai-runner/` (build stage), `video-runners/transcode-runner/` + `video-runners/abr-runner/` (go-builder stage) | Go 1.22 build environment; pure Go, no native deps. |
| `ubuntu:24.04` | `video-runners/codecs-builder/`, `video-runners/transcode-runner/` (Intel + AMD runtime stages), `video-runners/abr-runner/` (Intel + AMD runtime stages) | Codec build + Intel/AMD ffmpeg runtime. |
| `nvidia/cuda:12.9.1-devel-ubuntu24.04` | `openai-runners/openai-audio-runner/`, `openai-runners/openai-tts-runner/`, `openai-runners/openai-image-generation-runner/`, `rerank-runner/`, `video-runners/transcode-runner/` (NVIDIA build stage), `video-runners/abr-runner/` (NVIDIA build stage) | CUDA 12.9 toolkit for compiling FFmpeg with NVENC + building Python torch CUDA wheels. |

Plus `nvidia/cuda:12.9.1-runtime-ubuntu24.04` for NVIDIA runtime stages
of the video transcode runners (smaller than the devel image).

## Layer ordering

All Dockerfiles follow the same layer ordering for cache friendliness:

1. `FROM <base>`
2. ENV (DEBIAN_FRONTEND, PYTHONUNBUFFERED, PIP_NO_CACHE_DIR)
3. `apt-get update && apt-get install` for system packages
4. `pip install` for the canonical FastAPI / Pydantic / uvicorn /
   prometheus-client pins (Python runners only)
5. `pip install --index-url cu128 torch` (Python ML runners only)
6. `WORKDIR /app`
7. `COPY pyproject.toml` (or go.mod) — small, rarely-changing
8. `pip install -e .` (or `go mod download`)
9. `COPY src/` (or full tree) — frequently-changing
10. ENV runtime defaults (`RUNNER_ADDR`, `DEVICE`,
    `METRICS_ENABLED`, ...)
12. `EXPOSE 8080`
13. `HEALTHCHECK`
14. `CMD ["python3", "-m", "<runner>"]` (or `ENTRYPOINT`)

Steps 1-9 land cached layers; steps 10-14 are tiny and rebuild fast.
Editing `src/` invalidates only step 9 and downstream.

## Image tags

All runner images are tagged **`v0.8.10`**, frozen per user-memory
`feedback_no_image_version_bumps.md`. Republishing the same tag is the
ship gesture; do not bump the tag without explicit user approval.

| Image | Repository |
|---|---|
| Python base | `tztcloud/python-runner-base:v0.8.10` |
| OpenAI chat proxy (Go) | `tztcloud/openai-runner-chat:v0.8.10` |
| OpenAI embeddings proxy (Go) | `tztcloud/openai-runner-embeddings:v0.8.10` |
| Whisper STT | `tztcloud/openai-audio-runner:v0.8.10` |
| Kokoro TTS | `tztcloud/openai-tts-runner:v0.8.10` |
| Diffusers image gen | `tztcloud/openai-image-generation-runner:v0.8.10` |
| HF model downloader | `tztcloud/image-model-downloader:v0.8.10` |
| OpenAI tester | `tztcloud/openai-tester:v0.8.10` |
| Rerank | `tztcloud/rerank-runner:v0.8.10` |
| Rerank model downloader | `tztcloud/rerank-model-downloader:v0.8.10` |
| Codecs builder | `tztcloud/codecs-builder:v0.8.10` |
| VOD transcode (NVIDIA / Intel / AMD) | `tztcloud/transcode-runner{,-intel,-amd}:v0.8.10` |
| ABR ladder (NVIDIA / Intel / AMD) | `tztcloud/abr-runner{,-intel,-amd}:v0.8.10` |
| Transcode tester | `tztcloud/transcode-tester:v0.8.10` |

## Build orchestrators

Each component ships a `build.sh` script and a `Makefile`:

- `openai-runners/{Makefile,build.sh}` — builds the Python base, the
  Go proxy (multi-arch buildx), the four Python runners, the model
  downloader, and the tester. Smoke gesture validates compose configs.
- `rerank-runner/{Makefile,build.sh}` — runner + model downloader.
- `video-runners/{Makefile,build.sh}` — codecs-builder, both runners
  (default = NVIDIA target), tester. Intel + AMD targets are
  selectable via compose profiles.

All gestures are Docker-first per repo core belief #15; no host
Python or host Go install required.

## Multi-arch policy (per OQ4)

Only `openai-runners/openai-runner/` (the pure-Go chat + embeddings
proxy) ships multi-arch (linux/amd64 + linux/arm64). Every other
runner is amd64-only — NVIDIA arm64 GPU support exists (Jetson, GH200)
but isn't the default operator deployment shape.

The Go proxy uses Docker buildx for the multi-arch build:

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  --target chat \
  -t tztcloud/openai-runner-chat:v0.8.10 \
  openai-runners/openai-runner
```

## What this doc does NOT cover

- Broker capability wiring and pricing — for the active video path this
  lives in `capability-broker` host config rather than runner-local
  `offering.yaml`.
- Per-runner ENV reference — see each runner's `README.md`.
- GPU prerequisites + driver matrix — see
  [`gpu-requirements.md`](./gpu-requirements.md).
- Operator runbooks — see `<runner>/docs/operator-runbook.md`.
