# transcode-runner — operator runbook

VOD single-rendition transcode. Submits a transcode job over HTTP,
runs FFmpeg as a subprocess against NVENC / QSV / VAAPI / x264, polls
for completion, returns the output manifest.

## Image build + tag pin

amd64-only per OQ4 (GPU drivers are x86-only in practice for v0.1).

Three multi-stage build targets per vendor:

```bash
# NVIDIA NVENC + NVDEC + CUDA
docker build --target runtime-nvidia \
  --build-arg CODECS_IMAGE=tztcloud/codecs-builder:v0.8.10 \
  -t tztcloud/transcode-runner:v0.8.10 \
  -f transcode-runner/Dockerfile ..

# Intel QSV (oneVPL) + VAAPI
docker build --target runtime-intel \
  --build-arg CODECS_IMAGE=tztcloud/codecs-builder:v0.8.10 \
  -t tztcloud/transcode-runner-intel:v0.8.10 \
  -f transcode-runner/Dockerfile ..

# AMD VAAPI
docker build --target runtime-amd \
  --build-arg CODECS_IMAGE=tztcloud/codecs-builder:v0.8.10 \
  -t tztcloud/transcode-runner-amd:v0.8.10 \
  -f transcode-runner/Dockerfile ..
```

Tag **frozen at v0.8.10** per user-memory
`feedback_no_image_version_bumps.md`.

## GPU prerequisites

Per-vendor passthrough:

### NVIDIA (NVENC)

- NVIDIA driver 545+ (CUDA 12.9 base).
- nvidia-container-toolkit installed.
- `--gpus all` or compose `runtime: nvidia`.
- Pascal+ GPU (matches `../../docs/design-docs/gpu-requirements.md`).

### Intel (QSV / VAAPI)

- Intel oneVPL drivers + `intel-media-va-driver-non-free` installed
  on the host.
- Compose: `devices: - /dev/dri:/dev/dri`.
- Plain `docker run`: `--device /dev/dri/renderD128`.

### AMD (VAAPI)

- Mesa VA-API drivers + `libva2` on the host.
- Compose: `devices: - /dev/dri:/dev/dri`.
- Plain `docker run`: `--device /dev/dri/renderD128`.

## DEVICE=cpu fallback

`transcode-runner` falls back to x264 software encode automatically
when no GPU device is present. Slow but functional.

## Multi-arch matrix per OQ4

| Image | Platforms |
|---|---|
| `transcode-runner:v0.8.10` (nvidia) | linux/amd64 only |
| `transcode-runner-intel:v0.8.10` | linux/amd64 only |
| `transcode-runner-amd:v0.8.10` | linux/amd64 only |

## Prometheus integration (OQ5)

Set `METRICS_ENABLED=true` to expose `/metrics`. Default-off.

## Tuning

- `MAX_QUEUE_SIZE` — concurrent job cap (default 5); raise on bigger
  hosts.
- `TEMP_DIR` — per-job scratch (default `/tmp/transcode`); mount a
  volume for persistence.
- `JOB_TTL_SECONDS` — in-memory job record TTL (default 3600). Bounds
  memory; raise for long-running operator workflows.

## Healthcheck

The container's HEALTHCHECK invokes `/usr/local/bin/transcode-runner -healthcheck`.
HTTP healthcheck via `GET /healthz` also works.

## Capability registration

The orch-coordinator scrapes `GET /transcode-vod/options` per plan
0018.
