# DESIGN

Component-local design summary. Cross-cutting design lives at the repo
root in [`../docs/design-docs/`](../docs/design-docs/).

## What this component is

Two Go binaries that run FFmpeg as a subprocess to transcode VOD inputs:

```
   capability-broker
       │ POST /v1/cap → forwards to a configured backend
       │
       ├──► /v1/video/transcode      → transcode-runner   (single rendition)
       └──► /v1/video/transcode/abr  → abr-runner         (5-rung ABR ladder)
```

Both binaries import the shared `transcode-core` Go library via a local
`replace` directive in their `go.mod`. The library wraps FFmpeg
invocation, GPU detection, preset matching, HLS playlist construction,
progress parsing, thumbnail extraction, and filter chain construction.

## Codec base image

`codecs-builder/` is a multi-stage Docker base image. It compiles x264,
SVT-AV1, libopus, libvpx, and libzimg from source against an
`ubuntu:24.04` base, plus FFmpeg 7.x linked against those codecs. Both
runner Dockerfiles `FROM codecs-builder:<tag>` to avoid duplicating the
codec-build cost.

## Job model

Runners are stateless across restarts. A submitted job goes into an
in-memory map keyed by job id; `JOB_TTL_SECONDS` (default 3600s) bounds
memory. For VOD jobs needing persistent state across restarts, the
broker side carries that state via `host-config.yaml` → broker job
records → daemon ledger. No runner-side DB.

## Wire compliance

The runner does not consume `Livepeer-Payment` headers — those are
validated by the broker-side `payment-daemon/`. The runner sees only:

- HTTP method + path + body.
- `Livepeer-Capability` + `Livepeer-Offering` headers (informational).
- The orch-coordinator scrape against `GET /<capability>/options`.

## What stays out of this component

- **Live transcode.** Retired in v0.1; broker-side mode driver + RTMP
  listener + FFmpeg pipeline + LL-HLS server replace it (per plan
  0011-followup). `transcode-core/live.go` may carry residual helpers
  used by VOD; audit post-port.
- **Customer auth + billing.** Lives in `customer-portal/` and
  `video-gateway/`.
- **Payment validation.** Broker-side.
- **Capability registration.** Orch-coordinator scrapes `GET /<capability>/options`
  per plan 0018.
