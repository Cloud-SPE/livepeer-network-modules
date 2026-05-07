# video-runners

Go workload binaries for VOD video transcode. Two runner images plus a
shared Go library and a multi-stage codec base image.

> **For agents:** start at [`AGENTS.md`](./AGENTS.md).

## What it is

| Sub-component | Purpose |
|---|---|
| `codecs-builder/` | Dockerfile-only; produces base image with x264 / SVT-AV1 / libopus / libvpx / libzimg compiled from source |
| `transcode-core/` | Go module: shared FFmpeg + GPU + presets + HLS + progress + thumbnail + filter helpers |
| `transcode-runner/` | Go binary: `POST /v1/video/transcode` — VOD single-rendition |
| `abr-runner/` | Go binary: `POST /v1/video/transcode/abr` — VOD multi-rendition (ABR ladder) |
| `transcode-tester/` | Node integration smoke harness |

Each runner exposes:

- `POST <endpoint>` — submit a transcode job (returns 202 + job id).
- `GET <endpoint>/status?job_id=<id>` — poll job state.
- `GET <endpoint>/presets` — list embedded presets.
- `GET /healthz` — 200 ready.
- `GET /<capability>/options` — scraped by orch-coordinator (plan 0018).
- `GET /metrics` — Prometheus exposition (opt-in via `METRICS_ENABLED=true`).

`live-transcode-runner` is **NOT included** — plan 0011-followup retired
it. Capability-broker's mode driver replaces it with a broker-side RTMP
listener + FFmpeg pipeline + LL-HLS server.

## Status

**v0.1 scaffold.** Code lands per [`docs/exec-plans/active/0013-runners-byoc-migration.md`](../docs/exec-plans/active/0013-runners-byoc-migration.md).

## Build

Per repo-root core belief #15, every gesture is Docker-first.

```bash
make build              # build codecs-builder + both runner images
make smoke              # smoke against fixture mp4 (data/)
make help               # show all targets
```

## GPU passthrough

Operators run runners with one of:

- **NVIDIA NVENC** — `--gpus all` + nvidia-container-toolkit installed.
- **Intel QSV** — `--device /dev/dri/renderD128` + `i965-va-driver` host.
- **AMD VAAPI** — `--device /dev/dri/renderD128` + `mesa-va-drivers` host.

See [`docs/operator-runbook.md`](./docs/operator-runbook.md) per runner.

## License

MIT — repo-root [`../LICENSE`](../LICENSE) applies.
