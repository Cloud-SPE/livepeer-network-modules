# AGENTS.md

This is `video-runners/` — Go workload binaries that serve VOD transcode
endpoints to the capability broker. Two runner images plus a shared Go
library and a multi-stage Docker base.

- `codecs-builder/` — multi-stage Docker base image with x264, SVT-AV1,
  libopus, libvpx, libzimg compiled from source. All transcode runners
  `FROM codecs-builder:<tag>` for codec libs.
- `transcode-core/` — Go shared library (FFmpeg + GPU + presets + HLS +
  progress + thumbnails + filters).
- `transcode-runner/` — VOD single-rendition transcode binary.
- `abr-runner/` — VOD multi-rendition (ABR ladder) transcode binary.
- `transcode-tester/` — Node integration test harness.

`live-transcode-runner` is **DROPPED** per plan 0011-followup —
capability-broker's mode driver replaces it with a broker-side RTMP +
FFmpeg + LL-HLS pipeline.

Component-local agent map. The repo-root [`../AGENTS.md`](../AGENTS.md) is the
cross-cutting map; this file scopes to runner-specific guidance.

## Operating principles

Inherited from the repo root (agent-first harness pattern). Plus:

- **Runners are blind to customer identity.** No customer auth, no billing,
  no payment validation. The capability broker authenticates upstream and
  forwards a paid request; the runner sees only HTTP method + path +
  body + the informational `Livepeer-Capability` /
  `Livepeer-Offering` headers.
- **Capability identity is image-tag-pinned.** `CAPABILITY_NAME=transcode-vod`
  for `transcode-runner`; `CAPABILITY_NAME=abr-ladder` for `abr-runner`.
  Per OQ1; offering details live at `/etc/runner/offering.yaml`.
- **GPU passthrough is operator-supplied.** Runners support NVENC, QSV,
  and VAAPI; nvidia-container-toolkit and `/dev/dri/renderD128`
  passthrough are operator concerns documented in the runbook.
- **Metrics are opt-in.** `METRICS_ENABLED=true` exposes `/metrics`
  per OQ5; default-off, zero overhead.
- **Multi-arch policy.** Transcode runners ship amd64-only per OQ4 —
  GPU drivers are x86-only in practice for v0.1.
- **Shared `transcode-core` Go library.** Both runners import it via a
  local `replace` directive in their `go.mod`.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Architectural overview | [`DESIGN.md`](./DESIGN.md) |
| Build / run / smoke gestures | [`Makefile`](./Makefile) |
| Compose overlay | [`compose/`](./compose/) |
| Shared Go library | [`transcode-core/`](./transcode-core/) |
| Multi-stage codec base image | [`codecs-builder/`](./codecs-builder/) |
| Plan brief | [`../docs/exec-plans/completed/0013-runners-byoc-migration.md`](../docs/exec-plans/completed/0013-runners-byoc-migration.md) |

## Doing work in this component

- **All gestures are Docker-first** (per repo-root core belief #15).
- **Image tags are frozen at v0.8.10.** Do not bump without explicit user
  approval.
- **No per-runner LICENSE files.** Repo-root MIT applies.
- **Adjust transcode-core import path on port** to
  `github.com/Cloud-SPE/livepeer-network-rewrite/video-runners/transcode-core`.
- **Skip `live-transcode-runner`.** Plan 0011-followup retired it; the
  broker's mode driver + FFmpeg pipeline + LL-HLS server replaces it.
- **`transcode-core/live.go` may carry residual helpers.** Audit
  post-port; delete unreferenced symbols (per Q11).

## What lives elsewhere

- `openai-runners/` — sibling component for OpenAI-shaped capabilities.
- `rerank-runner/` — sibling component for reranker capability.
- `capability-broker/` — the orch-side dispatcher that forwards requests
  to runners. The broker also owns the live RTMP/HLS pipeline that
  retired `live-transcode-runner` (per plan 0011-followup).
