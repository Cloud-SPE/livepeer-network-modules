# Container Platform Audit

**Date:** 2026-05-17  
**Status:** active baseline + first consolidation pass landed

## Purpose

Record the current Docker/image platform across the monorepo, identify
duplication and drift, and define the target normalization path.

This document is about **build/runtime platform consistency**, not feature
behavior. The goal is:

- one validated version set for Go / Node / Python / CUDA / Ubuntu / Alpine
- fewer ad hoc base-image literals in Dockerfiles
- less duplicate package/bootstrap logic across runners
- smaller runtime images where that does not harm operability

## Canonical version set

First consolidation pass introduced a central manifest at:

- [`../../infra/build/image-versions.env`](../../infra/build/image-versions.env)

Current canonical values:

| Key | Value |
|---|---|
| `IMAGE_TAG_DEFAULT` | `v1.2.0` |
| `GO_VERSION` | `1.25.7` |
| `NODE_VERSION` | `22` |
| `PYTHON_VERSION` | `3.12` |
| `ALPINE_VERSION` | `3.20` |
| `UBUNTU_VERSION` | `24.04` |
| `CUDA_VERSION` | `12.9.1` |

These are the **validated repo defaults**. They are not necessarily the
newest globally available releases; they are the versions this repo should
converge on until an intentional platform bump is validated.

## Current matrix

### Go images

| Family | Current target |
|---|---|
| Core Go services | `golang:${GO_VERSION}-alpine` builders where supported |
| Pool Go services | `golang:${GO_VERSION}` multi-stage base/test/build |
| Go runtimes | mostly `gcr.io/distroless/static-debian12:nonroot`; some legacy `distroless/static:nonroot`; a few Debian runtimes |

Notes:

- `infra/scripts/build-images.sh` now passes `GO_VERSION` globally.
- `protocol-daemon` and `service-registry-daemon` now accept `ARG GO_VERSION`.
- `openai-chat-runner` and `openai-embeddings-runner` no longer pin `1.22`
  directly; they now accept the canonical `GO_VERSION`.

### Node images

| Family | Current target |
|---|---|
| Most gateways / portals | `node:${NODE_VERSION}-alpine` builder stages |
| Node runtimes | `gcr.io/distroless/nodejs22-debian12:nonroot` where adopted |
| Legacy exceptions | `daydream-gateway` and `vtuber-runner` still use `node:20*` directly |

Notes:

- The canonical target is Node 22.
- `daydream-gateway` and `vtuber-runner` are explicit drift points and should
  be treated as compatibility exceptions until validated on Node 22.

### Python CPU images

| Family | Current target |
|---|---|
| CPU Python base and helpers | `python:${PYTHON_VERSION}-slim` |

This is already relatively consistent.

### Python GPU / CUDA images

| Family | Current target |
|---|---|
| Shared GPU base | `nvidia/cuda:${CUDA_VERSION}-runtime-ubuntu${UBUNTU_VERSION}` |
| Shared GPU media base | derives from shared GPU base |
| GPU Python runners | derive from the shared GPU or GPU-media base |
| Video NVIDIA build/runtime stages | `nvidia/cuda:${CUDA_VERSION}-{devel,runtime}-ubuntu${UBUNTU_VERSION}` |

Notes:

- CUDA is already mostly aligned on `12.9.1`.
- First consolidation pass removed several hardcoded `v1.1.0` base-image
  references from GPU runner Dockerfiles.
- `rerank-runner` now derives from `python-gpu-runner-base` instead of
  duplicating the Python/CUDA bootstrap directly.

## Main duplication / drift findings

### 1. Version literals were spread across too many places

Before the first pass, `GO_VERSION`, image tag defaults, and GPU base-image
tags were repeated across:

- `infra/scripts/build-images.sh`
- component-specific `build.sh` scripts
- Dockerfile `ARG` defaults
- compose snippets
- operator README examples

This made partial upgrades easy and consistent upgrades hard.

### 2. GPU Python runner base inheritance drifted

Several Dockerfiles referenced:

- `tztcloud/python-gpu-runner-base:v1.1.0`
- `tztcloud/python-gpu-media-runner-base:v1.1.0`

directly in their `ARG BASE_IMAGE` defaults. That created silent drift from the
canonical image tag and made local direct builds inconsistent with the repo’s
main build script.

### 3. `rerank-runner` duplicated the GPU Python bootstrap

`rerank-runner/Dockerfile` previously rebuilt the same Python/CUDA base setup
that already existed in `python-gpu-runner-base`, while its `build.sh`
pretended a `BASE_IMAGE` override mattered even though the Dockerfile ignored
it.

That was both duplication and a correctness trap.

### 4. OpenAI runner Go builders lagged behind the repo default

`openai-chat-runner` and `openai-embeddings-runner` were still pinned to
`golang:1.22-alpine` directly. They now accept the central `GO_VERSION`, but
this was a clear example of platform skew.

### 5. Video runner Dockerfiles duplicate large FFmpeg/CUDA stage logic

`video-runners/abr-runner/Dockerfile` and
`video-runners/transcode-runner/Dockerfile` still duplicate most of:

- CUDA builder stage setup
- Intel/AMD FFmpeg stage setup
- FFmpeg source build flow
- runtime family layout

The first consolidation pass parameterized their shared version literals, but
**the stage structure is still duplicated**.

## First consolidation pass that landed

### Central manifest

- Added [`../../infra/build/image-versions.env`](../../infra/build/image-versions.env)

### Build-script normalization

- `infra/scripts/build-images.sh` now sources the central manifest
- it also injects shared build args:
  - `REGISTRY`
  - `TAG`
  - `GO_VERSION`
  - `NODE_VERSION`
  - `PYTHON_VERSION`
  - `ALPINE_VERSION`
  - `UBUNTU_VERSION`
  - `CUDA_VERSION`
- `openai-runners/build.sh`, `video-runners/build.sh`, and
  `rerank-runner/build.sh` now source the same manifest

### Dockerfile parameterization

- `openai-chat-runner` and `openai-embeddings-runner`
  - now accept `GO_VERSION` and `ALPINE_VERSION`
- `python-gpu-runner-base`
  - now accepts `CUDA_VERSION` and `UBUNTU_VERSION`
- `python-gpu-media-runner-base`, `openai-audio-runner`,
  `openai-image-generation-runner`, `openai-tts-runner`
  - now use composable `REGISTRY` / `TAG` / `BASE_IMAGE` defaults
- `rerank-runner`
  - now derives from the shared GPU base instead of reimplementing it
- `video-runners/abr-runner`, `video-runners/transcode-runner`,
  `video-runners/codecs-builder`
  - now accept parameterized `TAG`, `GO_VERSION`, `CUDA_VERSION`,
    and `UBUNTU_VERSION`
- `protocol-daemon` and `service-registry-daemon`
  - now accept `GO_VERSION`

## What still remains

### High-priority next pass

1. Normalize all direct image-tag defaults still pinned to `v1.1.0`
   across compose files, onboarding scenarios, helper scripts, and operator
   docs.
2. Audit all Go Dockerfiles and converge on a single builder pattern:
   - one monorepo-safe dependency-copy pattern for local `replace` modules
   - one runtime family choice per service class
3. Decide whether `distroless/static:nonroot` holdouts should move to
   `distroless/static-debian12:nonroot` for consistency.

### Medium-priority next pass

1. Extract shared FFmpeg/CUDA stage logic used by both video runners.
   - likely either a shared `ffmpeg-platform-base` Dockerfile or a more
     opinionated `codecs-builder` split
2. Revisit legacy Node 20 images:
   - `daydream-gateway`
   - `vtuber-runner`
3. Add CI linting that rejects new ad hoc base-version literals outside the
   central manifest flow.

### Not advised as a blind sweep

- “Upgrade CUDA to the newest public release everywhere” without validating:
  - PyTorch wheel availability
  - vLLM / inference runtime compatibility
  - FFmpeg/NVENC toolchain behavior

The right standard is **latest validated platform version**, not simply
**latest published version**.

## Recommended target architecture

### Build policy

- One canonical version manifest in `infra/build/`
- Repo-wide scripts source it
- Canonical build path injects shared build args to all Docker builds
- Dockerfiles accept those args and keep reasonable local defaults

### Base-image policy

- Go:
  - shared builder family around `golang:${GO_VERSION}`
  - shared runtime family around distroless static where possible
- Node:
  - Node 22 builder family
  - distroless Node 22 runtime where possible
- Python CPU:
  - `python:${PYTHON_VERSION}-slim`
- Python GPU:
  - one shared CUDA runtime base
  - one shared CUDA media base
  - all GPU app images derive from those

### Duplication policy

- no app Dockerfile should re-bootstrap a base that already exists in-repo
- heavy shared native toolchains should live in shared stages or shared images
- direct `FROM nvidia/cuda:...` usage should be limited to:
  - the canonical GPU base(s)
  - genuinely specialized video build/runtime stages

## Practical recommendation

Call the current state:

- **good first platform consolidation**
- **not yet a fully normalized container platform**

The next meaningful implementation step is:

1. eliminate remaining `v1.1.0` deployment-surface defaults repo-wide
2. normalize the remaining Go Dockerfile families
3. then tackle the video-runner FFmpeg/CUDA deduplication as its own focused
   refactor

