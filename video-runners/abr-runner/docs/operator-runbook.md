# abr-runner — operator runbook

VOD multi-rendition (ABR ladder) transcode. Submits one ladder job;
runs FFmpeg as a subprocess for each rung; assembles the final HLS
manifest with all renditions plus the master playlist.

## Image build + tag pin

amd64-only per OQ4. Three multi-stage build targets per vendor:

```bash
docker build --target runtime-nvidia \
  --build-arg CODECS_IMAGE=tztcloud/codecs-builder:v0.8.10 \
  -t tztcloud/abr-runner:v0.8.10 \
  -f abr-runner/Dockerfile ..

docker build --target runtime-intel \
  --build-arg CODECS_IMAGE=tztcloud/codecs-builder:v0.8.10 \
  -t tztcloud/abr-runner-intel:v0.8.10 \
  -f abr-runner/Dockerfile ..

docker build --target runtime-amd \
  --build-arg CODECS_IMAGE=tztcloud/codecs-builder:v0.8.10 \
  -t tztcloud/abr-runner-amd:v0.8.10 \
  -f abr-runner/Dockerfile ..
```

Tag **frozen at v0.8.10** per user-memory
`feedback_no_image_version_bumps.md`.

## GPU prerequisites

Same per-vendor matrix as `transcode-runner`. See
[`../transcode-runner/docs/operator-runbook.md`](../../transcode-runner/docs/operator-runbook.md).

ABR ladder jobs are GPU-heavy: a 5-rung ladder runs 5 concurrent
encodes. Recommend GPUs with multi-NVENC chips (consumer Ada+)
or multi-GPU hosts.

## DEVICE=cpu fallback

Software fallback works but is impractical for ABR ladders (5
concurrent x264 encodes saturate CPUs). Treat CPU-only as a
sanity-check path.

## Multi-arch matrix per OQ4

| Image | Platforms |
|---|---|
| `abr-runner:v0.8.10` (nvidia) | linux/amd64 only |
| `abr-runner-intel:v0.8.10` | linux/amd64 only |
| `abr-runner-amd:v0.8.10` | linux/amd64 only |

## Prometheus integration (OQ5)

Set `METRICS_ENABLED=true` to expose `/metrics`. Default-off.

## Tuning

- `MAX_QUEUE_SIZE` — concurrent ladder cap (default 2; lower than
  `transcode-runner`'s 5 because each ladder runs 5 encodes).
- `TEMP_DIR` — per-job scratch (default `/tmp/abr`).
- `JOB_TTL_SECONDS` — in-memory job record TTL (default 3600).

## Healthcheck

Container HEALTHCHECK invokes `/usr/local/bin/abr-runner -healthcheck`.
HTTP via `GET /healthz` also works.
