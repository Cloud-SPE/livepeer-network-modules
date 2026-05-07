---
plan: 0013-runners
title: openai-runners + rerank-runner + video-runners — workload-binary migration
status: design-doc
phase: plan-only
opened: 2026-05-07
owner: harness
related:
  - "active plan 0013-shell — customer-portal/ shared SaaS shell library"
  - "active plan 0013-openai — openai-gateway/ collapse"
  - "active plan 0013-vtuber — vtuber product family"
  - "active plan 0013-video — video-gateway/ collapse"
  - "completed plan 0011-followup — broker-side RTMP/FFmpeg/HLS pipeline; live-transcode-runner already covered"
  - "active plan 0018 — orch-coordinator + roster auto-discovery (broker scrapes /options server-side)"
  - "design-doc docs/design-docs/migration-from-suite.md"
  - "user-memory reference_livepeer_byoc.md — gateway-proxy + openai-runners is the canonical adapter"
audience: workload-runner maintainers planning the byoc-tree absorption
---

# Plan 0013-runners — workload-binary migration (design)

> **Paper-only design brief.** No code, no `Dockerfile` edits, no
> `go.mod` / `pyproject.toml` edits ship from this commit. Locks
> recorded in §14 as `DECIDED:` blocks. **Not chain-gated** —
> workload runners only consume broker dispatch; they don't sign
> tickets and don't talk to a payer-side daemon. Implementation can
> ship before plan 0016 closes.

## 1. Status and scope

Scope: **the workload-binary tree at `livepeer-cloud-spe/livepeer-byoc/`**
absorbs into the rewrite as three new components:

1. `openai-runners/` — five Go + Python services serving OpenAI-shaped
   endpoints to the broker:
   - `openai-runner/` (Go) — proxy in front of an upstream
     OpenAI-compatible service (Ollama, vLLM); two `cmd/` builds
     (chat, embeddings).
   - `openai-audio-runner/` (Python) — Whisper-based transcriptions +
     translations (FastAPI + faster-whisper).
   - `openai-tts-runner/` (Python) — Kokoro TTS (FastAPI + transformers).
   - `openai-image-generation-runner/` (Python) — diffusers
     (FastAPI + torch).
   - `image-model-downloader/` — one-shot model downloader image.
2. `rerank-runner/` — Python reranker (FastAPI + sentence-transformers
   CrossEncoder).
3. `video-runners/` — three Go transcode binaries:
   - `transcode-runner/` (Go) — VOD single-rendition transcode.
   - `abr-runner/` (Go) — VOD multi-rendition (ABR ladder) transcode.
   - `transcode-core/` (Go module) — shared transcode library
     (FFmpeg + GPU + presets + HLS + progress + thumbnails + filters).
   - `codecs-builder/` (Dockerfile-only) — multi-stage Docker base
     image with x264 / SVT-AV1 / etc. baked in.

The **byoc directory name retires from the rewrite vocabulary** per
user-memory `feedback_no_byoc_term.md`. The rewrite uses "OpenAI
adapter / paid HTTP adapter / workload binaries". The directory at
`livepeer-cloud-spe/livepeer-byoc/` is cited verbatim in §5 source
maps; narrative refers to "the byoc tree" or "the workload-runner
tree" or simply "the existing workload binaries".

Out of scope:

- `livepeer-byoc/gateway-proxy/` — was for go-livepeer; not needed in
  the rewrite. Per user lock + `migration-from-suite.md`.
- `livepeer-byoc/video-generation/` — not needed per user lock.
- `livepeer-byoc/register-capabilities/` — replaced by orch-coordinator
  scrape per plan 0018; runners' `GET /options` endpoint preserved.
- `livepeer-byoc/deployment-examples/` — not needed.
- `transcode-runners/live-transcode-runner/` — already covered by plan
  0011-followup (capability-broker's mode driver replaces it).
- `livepeer-modules-project/` — left alone; user retires manually.

The runners are **not chain-gated**. They consume broker-dispatched
HTTP requests; the broker owns receiver-mode `payment-daemon/`
integration; the runners are workload binaries with no payment
awareness. They can ship before plan 0016 closes.

The MIT-licensed canonical lock applies (Fastify 5 + Zod 3 …) only to
the rewrite's TS components; the runners are Go and Python under
their own canonical pins (per §6).

## 2. What predecessor work left unfinished

Plan 0011-followup §13 lists the 10-commit broker-side media pipeline
that retires `live-transcode-runner/`. The other byoc-tree binaries
(`transcode-runner`, `abr-runner`) have **no broker-side replacement**
— they remain workload binaries running under broker dispatch. The
broker forwards HTTP `POST /v1/video/transcode` to the runner; the
runner does the work.

Plan 0018 (orch-coordinator UX) shifts capability registration from
the suite's `register-capabilities` sidecar pattern to a broker-side
scrape of each runner's `GET /options` endpoint. **No runner-side
code change** required for the registration shift — runners keep
the existing endpoint shape per plan 0018; the sidecar binary at
`livepeer-byoc/register-capabilities/` retires unmigrated.

Plans 0008 + 0008-followup ship `gateway-adapters/` middleware for
HTTP modes. The runners are workload binaries; they consume the
broker's mode dispatch, not `gateway-adapters/`. The adapters are a
gateway-side concern.

## 3. Reference architecture

```
   capability-broker (orch host)
       │  Livepeer-Mode dispatch
       │  POST /v1/cap → forwards to a configured backend per host-config.yaml
       │
       ├──► /v1/chat/completions      → openai-runner (chat cmd)    → Ollama / vLLM upstream
       ├──► /v1/embeddings            → openai-runner (embed cmd)   → vLLM upstream
       ├──► /v1/audio/transcriptions  → openai-audio-runner          (Whisper)
       ├──► /v1/audio/translations    → openai-audio-runner          (Whisper)
       ├──► /v1/audio/speech          → openai-tts-runner            (Kokoro TTS)
       ├──► /v1/images/generations    → openai-image-generation-runner (diffusers)
       ├──► /v1/rerank                → rerank-runner                (CrossEncoder)
       ├──► /v1/video/transcode       → transcode-runner             (FFmpeg single-rendition)
       └──► /v1/video/transcode/abr   → abr-runner                   (FFmpeg ABR ladder)

   Each runner exposes:
     - GET  /healthz
     - GET  /<capability>/options    ← scraped by orch-coordinator (plan 0018)
     - POST <endpoint>               ← consumed by broker mode dispatch
```

Each runner is a single Docker image; one process per orch host
instance; multiple capabilities per host run as multiple containers
(or one runner with multiple `cmd/` entry points, in the openai-runner
case).

The runners do **not** see customer auth, customer payment, or
customer identity. The broker authenticates the per-request `Livepeer-
Payment` header with the receiver-mode `payment-daemon/`; on success
the broker forwards the payload to the runner's HTTP endpoint. The
runner sees only HTTP method + path + body + the `Livepeer-Capability`
+ `Livepeer-Offering` headers (informational; no enforcement).

## 4. Component layout

### 4.1 openai-runners/

```
openai-runners/
  AGENTS.md
  DESIGN.md
  README.md
  Makefile                          ← `make build|test|smoke|push`
  build.sh                          ← multi-image build orchestrator (kept from byoc)
  setup-models.sh                   ← model-downloader convenience script (kept)
  test.sh                           ← suite-wide smoke runner (kept)
  compose.yaml                      ← dev compose; one service per cmd
  compose.audio.yaml                ← audio + tts runner overlay
  compose.ollama.yaml               ← ollama upstream overlay
  compose.vllm.chat.yaml            ← vllm chat upstream overlay
  compose.vllm.embeddings.yaml      ← vllm embeddings upstream overlay
  python-runner-base/               ← shared Python base image (OQ2); FROM python:3.12-slim + fastapi/pydantic/pydantic-settings/structlog/uvicorn/prometheus-client
    Dockerfile                      ← single base layer; cached once across audio/tts/image/rerank runners
  openai-runner/                    ← Go; two cmd binaries; does NOT use python-runner-base (separate Go runtime per OQ2)
    Dockerfile                      ← multi-stage; chat + embed targets
    go.mod                          ← module openai-runner
    go.sum
    cmd/
      chat/
        main.go                     ← runner.Run with chat capability
      embeddings/
        main.go                     ← runner.Run with embeddings capability
    internal/
      runner/
        runner.go                   ← shared HTTP server + upstream proxy + /options handler
        runner_test.go
        models.go                   ← upstream /models discovery
        progress.go                 ← optional usage-event extraction
  openai-audio-runner/              ← Python (FastAPI + faster-whisper)
    Dockerfile                      ← `FROM python-runner-base:<tag>`; adds faster-whisper + entrypoint
    pyproject.toml                  ← package: openai_audio_runner
    uv.lock
    src/
      openai_audio_runner/
        __init__.py
        __main__.py
        app.py                      ← FastAPI + /v1/audio/transcriptions + /v1/audio/translations + /options
        whisper_loader.py
        models/                     ← per-arch model loaders (fp16/int8 etc.)
    tests/
    test.sh
  openai-tts-runner/                ← Python (FastAPI + Kokoro)
    Dockerfile                      ← `FROM python-runner-base:<tag>`; adds transformers + kokoro-tts
    pyproject.toml
    uv.lock
    src/
      openai_tts_runner/
        app.py                      ← /v1/audio/speech + /options
        kokoro_loader.py
    tests/
    test.sh
  openai-image-generation-runner/   ← Python (FastAPI + diffusers)
    Dockerfile                      ← `FROM python-runner-base:<tag>`; adds diffusers + accelerate
    pyproject.toml
    uv.lock
    src/
      openai_image_generation_runner/
        app.py                      ← /v1/images/generations + /options
        diffusers_loader.py
    tests/
  image-model-downloader/           ← one-shot Docker image; pre-pulls model assets at host setup
    Dockerfile
    pyproject.toml
    src/
      download.py
  openai-tester/                    ← integration tester (kept from byoc)
    package.json
    test-chat-completion.mjs
    test-text-embedding.mjs
    test-audio-transcription.mjs
    test-audio-translation.mjs
    test-audio-speech.mjs
    test-image-generation.mjs
```

### 4.2 rerank-runner/

```
rerank-runner/
  AGENTS.md
  DESIGN.md
  README.md
  Makefile
  build.sh
  test.sh
  compose.yaml
  Dockerfile                        ← `FROM python-runner-base:<tag>` (sibling component; reuses openai-runners/python-runner-base/ output image); adds sentence-transformers
  pyproject.toml                    ← package: rerank_runner
  uv.lock
  src/
    rerank_runner/
      __init__.py
      __main__.py
      app.py                        ← /v1/rerank + /options
      model_loader.py               ← zerank-2 / Qwen3 cross-encoder
  model-downloader/
    Dockerfile
    pyproject.toml
    src/
      download.py
  tests/
```

### 4.3 video-runners/

```
video-runners/
  AGENTS.md
  DESIGN.md
  README.md
  Makefile
  build.sh
  compose.yaml                      ← dev compose; transcode + abr + (optional) hardware accel
  data/                             ← test fixtures (small mp4s); kept from byoc
  docs/                             ← per-runner docs (kept; updated)
  codecs-builder/                   ← Dockerfile-only; produces base image with x264/SVT-AV1/etc.
    Dockerfile
  transcode-core/                   ← Go module shared by both binaries
    go.mod                          ← module transcode-core
    go.sum
    abr_presets.go
    ffmpeg.go
    filters.go
    gpu.go
    hls.go
    io.go
    live.go                         ← live-transcode bits — KEPT for VOD-side reuse but live-transcode-runner discontinued; may delete if unreferenced post-port
    presets.go
    progress.go
    thumbnail.go
    *_test.go                       ← retained
  transcode-runner/                 ← Go binary
    Dockerfile
    go.mod                          ← module transcode-runner; replaces transcode-core sibling via local replace directive
    go.sum
    main.go                         ← Fastify-equivalent: net/http + handlers
    presets.yaml                    ← embedded via go:embed
  abr-runner/                       ← Go binary
    Dockerfile
    go.mod                          ← module abr-runner; replaces transcode-core sibling
    go.sum
    main.go
    presets.yaml
  transcode-tester/                 ← integration tester (kept from byoc)
```

`live-transcode-runner/` from the byoc tree is **dropped** per plan
0011-followup. The `transcode-core/live.go` file may carry residual
live-transcode helpers; if unreferenced post-port, deleted.

## 5. Source-to-destination file map

### 5.1 openai-runners/

| Source | Target |
|---|---|
| **NEW (no source)** | `openai-runners/python-runner-base/Dockerfile` — net-new shared Python base image per OQ2; `FROM python:3.12-slim` + fastapi/pydantic/pydantic-settings/structlog/uvicorn/prometheus-client. Not present in byoc tree. |
| `livepeer-byoc/openai-runners/build.sh` | `openai-runners/build.sh` |
| `livepeer-byoc/openai-runners/setup-models.sh` | `openai-runners/setup-models.sh` |
| `livepeer-byoc/openai-runners/docker-compose.yml` | `openai-runners/compose.yaml` (renamed; standardized on `compose.*` per rewrite convention) |
| `livepeer-byoc/openai-runners/docker-compose.audio.yml` | `openai-runners/compose.audio.yaml` |
| `livepeer-byoc/openai-runners/docker-compose.ollama.yml` | `openai-runners/compose.ollama.yaml` |
| `livepeer-byoc/openai-runners/docker-compose.vllm.chat.yml` | `openai-runners/compose.vllm.chat.yaml` |
| `livepeer-byoc/openai-runners/docker-compose.vllm.embeddings.yml` | `openai-runners/compose.vllm.embeddings.yaml` |
| `livepeer-byoc/openai-runners/openai-runner/cmd/chat/main.go:1-12` | `openai-runners/openai-runner/cmd/chat/main.go` |
| `livepeer-byoc/openai-runners/openai-runner/cmd/embeddings/main.go:1-12` | `openai-runners/openai-runner/cmd/embeddings/main.go` |
| `livepeer-byoc/openai-runners/openai-runner/internal/runner/runner.go:1-200+` | `openai-runners/openai-runner/internal/runner/runner.go` |
| `livepeer-byoc/openai-runners/openai-runner/Dockerfile` | `openai-runners/openai-runner/Dockerfile` |
| `livepeer-byoc/openai-runners/openai-runner/go.mod` | `openai-runners/openai-runner/go.mod` (module name verified `openai-runner` or rename to `openai_runner`; standardize) |
| `livepeer-byoc/openai-runners/openai-audio-runner/app.py:1-399` (full file) | `openai-runners/openai-audio-runner/src/openai_audio_runner/app.py` (refactored: split FastAPI app + whisper_loader) |
| `livepeer-byoc/openai-runners/openai-audio-runner/{Dockerfile,requirements.txt,test.sh,README.md}` | `openai-runners/openai-audio-runner/{Dockerfile,pyproject.toml,test.sh,README.md}` (requirements.txt → pyproject.toml; uv-lock-managed) |
| `livepeer-byoc/openai-runners/openai-tts-runner/app.py:1-290` | `openai-runners/openai-tts-runner/src/openai_tts_runner/app.py` |
| `livepeer-byoc/openai-runners/openai-tts-runner/{Dockerfile,requirements.txt,test.sh,README.md}` | `openai-runners/openai-tts-runner/{Dockerfile,pyproject.toml,test.sh,README.md}` |
| `livepeer-byoc/openai-runners/openai-image-generation-runner/app.py:1-499` | `openai-runners/openai-image-generation-runner/src/openai_image_generation_runner/app.py` |
| `livepeer-byoc/openai-runners/openai-image-generation-runner/{Dockerfile,requirements.txt}` | `openai-runners/openai-image-generation-runner/{Dockerfile,pyproject.toml}` |
| `livepeer-byoc/openai-runners/image-model-downloader/{download.py,requirements.txt,Dockerfile}` | `openai-runners/image-model-downloader/{src/download.py,pyproject.toml,Dockerfile}` |
| `livepeer-byoc/openai-runners/openai-tester/{package.json,test-*.mjs,package-lock.json}` | `openai-runners/openai-tester/{package.json,test-*.mjs}` (lockfile regenerated under pnpm if integrated; otherwise plain npm) |
| `livepeer-byoc/openai-runners/LICENSE` | retired (rewrite-root LICENSE applies; MIT) |
| `livepeer-byoc/openai-runners/README.md` | updated; cross-reference rewrite docs |

### 5.2 rerank-runner/

| Source | Target |
|---|---|
| `livepeer-byoc/rerank-runner/build.sh` | `rerank-runner/build.sh` |
| `livepeer-byoc/rerank-runner/test.sh` | `rerank-runner/test.sh` |
| `livepeer-byoc/rerank-runner/docker-compose.yml` | `rerank-runner/compose.yaml` |
| `livepeer-byoc/rerank-runner/rerank-runner/app.py:1-317` | `rerank-runner/src/rerank_runner/app.py` |
| `livepeer-byoc/rerank-runner/rerank-runner/{Dockerfile,requirements.txt}` | `rerank-runner/{Dockerfile,pyproject.toml}` |
| `livepeer-byoc/rerank-runner/model-downloader/{download.py,requirements.txt,Dockerfile}` | `rerank-runner/model-downloader/{src/download.py,pyproject.toml,Dockerfile}` |
| `livepeer-byoc/rerank-runner/LICENSE` | retired |
| `livepeer-byoc/rerank-runner/README.md` | updated |

### 5.3 video-runners/

| Source | Target |
|---|---|
| `livepeer-byoc/transcode-runners/build.sh` | `video-runners/build.sh` |
| `livepeer-byoc/transcode-runners/docker-compose.yml` | `video-runners/compose.yaml` |
| `livepeer-byoc/transcode-runners/data/` | `video-runners/data/` |
| `livepeer-byoc/transcode-runners/docs/` | `video-runners/docs/` |
| `livepeer-byoc/transcode-runners/codecs-builder/Dockerfile:1-30+` | `video-runners/codecs-builder/Dockerfile` |
| `livepeer-byoc/transcode-runners/transcode-core/{abr_presets,ffmpeg,filters,gpu,hls,io,live,presets,progress,thumbnail}.go` (10 .go files; 10 corresponding `_test.go` files) | `video-runners/transcode-core/*.go` |
| `livepeer-byoc/transcode-runners/transcode-core/go.mod` + `go.sum` | `video-runners/transcode-core/go.mod` + `go.sum` |
| `livepeer-byoc/transcode-runners/transcode-runner/main.go:1-827` | `video-runners/transcode-runner/main.go` |
| `livepeer-byoc/transcode-runners/transcode-runner/{Dockerfile,go.mod,go.sum,presets.yaml}` | `video-runners/transcode-runner/{Dockerfile,go.mod,go.sum,presets.yaml}` |
| `livepeer-byoc/transcode-runners/abr-runner/main.go:1-860` | `video-runners/abr-runner/main.go` |
| `livepeer-byoc/transcode-runners/abr-runner/{Dockerfile,go.mod,go.sum,presets.yaml}` | `video-runners/abr-runner/{Dockerfile,go.mod,go.sum,presets.yaml}` |
| `livepeer-byoc/transcode-runners/transcode-tester/` | `video-runners/transcode-tester/` |
| `livepeer-byoc/transcode-runners/live-transcode-runner/` | **DROPPED** (plan 0011-followup retired this; capability-broker's mode driver replaces) |
| `livepeer-byoc/transcode-runners/t.txt` | dropped (scratch file; not real code) |

### 5.4 Out-of-scope (forwarded; no port)

| Source | Disposition |
|---|---|
| `livepeer-byoc/gateway-proxy/` | **DROPPED.** Was for go-livepeer; not needed in rewrite. |
| `livepeer-byoc/video-generation/` | **DROPPED.** Per user lock; not needed. |
| `livepeer-byoc/register-capabilities/` | **DROPPED.** Replaced by orch-coordinator scrape per plan 0018; runners' `GET /options` preserved. |
| `livepeer-byoc/deployment-examples/` | **DROPPED.** Per user lock. |
| `livepeer-byoc/byoc-high-level-flow.drawio*` | **DROPPED.** Outdated diagram; rewrite has its own architecture overview. |

## 6. Tech-stack lock + variance justification

The runners are deliberately **outside the canonical TS lock** because
they're workload-side workhorses with established Go + Python
ecosystems. Each runner pins its own versions; the rewrite preserves
those pins.

### 6.1 Variance: openai-runner (Go)

Justification: Go for low-overhead HTTP proxy + SSE streaming;
matches existing byoc impl. Pins:

- Go ≥1.22 (matches byoc go.mod current).
- Stdlib-only `net/http` for the proxy (no Fastify/echo); SSE-aware
  `Transport` with `ForceAttemptHTTP2=false` per existing impl
  (`runner.go:51`).
- No third-party deps in `internal/runner/` core; keep weight low.

### 6.2 Variance: openai-audio-runner / openai-tts-runner / openai-image-generation-runner / rerank-runner (Python)

Justification: Python for ML model serving (faster-whisper, Kokoro,
diffusers, sentence-transformers). Pins:

- Python ≥3.12.
- FastAPI ≥0.110.
- Pydantic ≥2.5.
- torch ≥2.4 (CUDA 12.1 in baked Docker images per existing pins).
- Per-runner ML deps (faster-whisper, kokoro-tts via `transformers`,
  diffusers, sentence-transformers) — see existing `requirements.txt`
  files in byoc; rewrite migrates to `pyproject.toml` + `uv.lock`.
- Model assets are pulled by `image-model-downloader/` (or per-runner
  `model-downloader/` for rerank) at host setup time; runner image
  doesn't bundle weights.

### 6.3 Variance: transcode-runner / abr-runner (Go + ffmpeg + GPU)

Justification: Go for orchestration + concurrency; ffmpeg subprocess
for actual transcode; GPU stack (NVENC / QSV / VAAPI) baked in via
`codecs-builder/`. Pins:

- Go ≥1.22.
- ffmpeg 7.x baked in via `codecs-builder/` (matches plan 0011-followup
  §5.2 broker pin).
- Stdlib `net/http` server.
- `transcode-core` Go module shared via local `replace` directive
  in `transcode-runner/go.mod` and `abr-runner/go.mod`.

### 6.4 Variance: image-model-downloader / model-downloader (Python)

One-shot Docker images; not long-running services. Justified by
existing tooling reuse; same Python pins as the Python runners.

### 6.5 No customer-portal/ or gateway-adapters/ dependency

Runners do **not** import `customer-portal/` (no customer auth /
billing) and do **not** import `gateway-adapters/` (no client-side
wire spec; runners are servers, broker is the client). The runners'
HTTP server is hand-rolled per existing byoc impl.

### 6.6 License

**MIT.** Per user lock + repo-root LICENSE. The byoc tree's
per-component LICENSE files retire; rewrite root LICENSE applies.

### 6.7 Python runners share `python-runner-base/` (OQ2)

All Python runners — `openai-audio-runner`, `openai-tts-runner`,
`openai-image-generation-runner`, `rerank-runner` — `FROM` the
shared `python-runner-base` image. The base bakes in Python 3.12-slim
+ the canonical Python pins (fastapi, pydantic, pydantic-settings,
structlog, uvicorn, prometheus-client per OQ5). Per-runner Dockerfiles
add only model-specific deps + entrypoints; common layer is cached
once across builds. The Go-based `openai-runner/` (chat + embeddings
proxy) does **not** use this base — it's a separate Go runtime.

## 7. DB schema

**None.** Workload runners are stateless (per-job in-memory state +
per-process model load; see `transcode-runner/main.go:30+` global
job map). Job records are in-memory; `JOB_TTL_SECONDS` env (default
3600s) bounds memory.

For VOD jobs that need persistent state across restarts (e.g.
operator-side ledger reconciliation), the broker side carries that
state via `host-config.yaml` → broker job records → daemon ledger.
No runner-side DB.

## 8. Customer-facing surfaces

**None.** Runners are not customer-facing. They serve the broker
only. Customers reach a runner via:

```
customer → gateway → capability-broker → runner
```

The gateway (openai-gateway, video-gateway, vtuber-gateway) handles
customer auth + billing + UI.

### 8.1 Runner endpoints (broker-facing)

Per runner: same shape, capability-specific endpoint:

| Method + path | Purpose | Source |
|---|---|---|
| `POST /v1/chat/completions` | OpenAI chat | `openai-runners/openai-runner/cmd/chat/main.go:5-11` |
| `POST /v1/embeddings` | OpenAI embeddings | `openai-runners/openai-runner/cmd/embeddings/main.go:5-11` |
| `POST /v1/audio/transcriptions` | Whisper transcribe | `openai-audio-runner/app.py:CAP_TRANSCRIPTIONS` |
| `POST /v1/audio/translations` | Whisper translate | `openai-audio-runner/app.py:CAP_TRANSLATIONS` |
| `POST /v1/audio/speech` | Kokoro TTS | `openai-tts-runner/app.py:CAP_SPEECH` |
| `POST /v1/images/generations` | Diffusers | `openai-image-generation-runner/app.py:CAPABILITY_NAME` |
| `POST /v1/rerank` | sentence-transformers CrossEncoder | `rerank-runner/app.py` |
| `POST /v1/video/transcode` | FFmpeg single-rendition | `transcode-runner/main.go:796` |
| `POST /v1/video/transcode/abr` | FFmpeg ABR | `abr-runner/main.go:829` |
| `GET /v1/video/transcode/status` | VOD job status | `transcode-runner/main.go:797` |
| `GET /v1/video/transcode/abr/status` | ABR VOD job status | `abr-runner/main.go:830` |
| `GET /v1/video/transcode/presets` | List embedded presets | `transcode-runner/main.go:798` |
| `GET /v1/video/transcode/abr/presets` | List embedded presets | `abr-runner/main.go:831` |

All runners additionally expose:

- `GET /healthz` — 200 when ready; 503 during model load / startup
  (e.g. `runner.go:132` health pattern).
- `GET /<capability>/options` — broker scrape per plan 0018; returns
  JSON with declared offerings + per-offering metadata. Existing pattern:
  - `runner.go:147` (`/{Capability}/options`).
  - `openai-image-generation-runner/app.py:473-474` (`/options` + `/{capability}/options`).
  - `openai-tts-runner/app.py:272` (`/{CAP_SPEECH}/options`).
  - `openai-audio-runner/app.py:376,381` (per-capability /options).

The orch-coordinator (plan 0018) scrapes these `/options` endpoints
to build the runtime-discovered capability roster.

### 8.2 No OAuth / no chat-worker / no egress-worker

These are **product gateway** surfaces, not runner surfaces.

## 9. Cross-component dependencies

```
openai-runners/openai-runner:
  - imports stdlib only
  - no rewrite-internal deps

openai-runners/openai-audio-runner:
  - python deps: fastapi, pydantic, faster-whisper, torch
  - no rewrite-internal deps

openai-runners/openai-tts-runner:
  - python deps: fastapi, pydantic, transformers, torch, kokoro-tts
  - no rewrite-internal deps

openai-runners/openai-image-generation-runner:
  - python deps: fastapi, pydantic, diffusers, torch, accelerate
  - no rewrite-internal deps

rerank-runner:
  - python deps: fastapi, pydantic, sentence-transformers, torch
  - no rewrite-internal deps

video-runners/transcode-runner + abr-runner:
  - go.mod with `replace transcode-core => ../transcode-core`
  - stdlib net/http
  - no rewrite-internal Go deps

video-runners/transcode-core:
  - go.mod (library); stdlib + ffmpeg subprocess
  - no rewrite-internal Go deps
```

The runners depend on:
- **Upstream services** (Ollama, vLLM, model files via `image-
  model-downloader`).
- **Broker** (over HTTP, runner is the server; broker is the client).
- **`livepeer-network-protocol/`** — runners read `Livepeer-Capability`
  + `Livepeer-Offering` headers (informational); no proto stub
  imports needed (the runner doesn't decode `Livepeer-Payment`).
- **`openai-runners/python-runner-base/`** — every Python runner
  (`openai-audio-runner`, `openai-tts-runner`,
  `openai-image-generation-runner`, `rerank-runner`) inherits this
  shared base image per OQ2. The Go-based `openai-runner/` proxy is
  independent; it has no Python-base dependency.

The runners do **not** depend on:
- `customer-portal/` (no auth / billing).
- `gateway-adapters/` (server side; gateway-adapters is client side).
- `payment-daemon/` (broker handles payment validation upstream).

## 10. Configuration surface

### 10.0 Common keys (every runner)

Per OQ1 + OQ3 + OQ5, every runner exposes the same three control
knobs in addition to its per-runner-specific keys:

| Env var | Required | Purpose |
|---|---|---|
| `CAPABILITY_NAME` | yes | Capability identity per OQ1; image-tag-pinned canonical name. Allowed values include `openai-chat-completions`, `openai-embeddings`, `whisper-transcriptions`, `kokoro-tts`, `image-generation`, `rerank`, `transcode-vod`, `abr-ladder`. Operator-friendly; one value per runner image. |
| `DEVICE` | no (default `cuda` for ML runners, `cpu` for the Go proxy) | torch device. ML runners fail-fast at startup if `cuda` is set and no GPU is detected (OQ3); operator falls back via `DEVICE=cpu`. |
| `METRICS_ENABLED` | no (default `false`) | When `true`, exposes `/metrics` (Prometheus exposition format) per OQ5. Cardinality-capped: labels are `model` and `offering` only. Default metrics: `runner_inference_total{outcome,model,offering}`, `runner_inference_duration_seconds{model,offering}`, `runner_active_concurrent_inferences`, `runner_gpu_utilization_pct{gpu_index}`. Zero overhead when unset. |

Offering details (presets, resolutions, model variants, rate-card
hints) live in an embedded YAML manifest at `/etc/runner/offering.yaml`
inside each runner image, mounted read-only with operator overrides
as needed (OQ1). Schema is documented per runner. The `CAPABILITY_NAME`
env names the capability; `offering.yaml` describes the offerings.

### 10.1 openai-runner (chat / embeddings)

| Env var | Required | Purpose |
|---|---|---|
| `RUNNER_ADDR` | no (default `:8080`) | HTTP bind. |
| `UPSTREAM_URL` | yes | Upstream OpenAI-compatible endpoint URL (e.g. `http://ollama:11434/v1/chat/completions`). |
| `MAX_BODY_BYTES` | no (default per cmd; 5MB chat / 1MB embed) | Request size cap. |
| `MODEL_DISCOVERY_RETRIES` | no (default 10) | At-startup retries against upstream `/v1/models`. |

### 10.2 openai-audio-runner

| Env var | Required | Purpose |
|---|---|---|
| `MODEL_ID` | no (default `whisper-large-v3`) | Whisper model id. |
| `MODEL_DIR` | no (default `/models`) | Local model cache. |
| `RUNNER_PORT` | no (default `8080`) | HTTP bind. |
| `MAX_QUEUE_SIZE` | no (default 5) | 429 threshold. |
| `DEVICE` | no (default `cuda`) | torch device. |
| `DTYPE` | no (default `bfloat16`) | torch dtype. |

### 10.3 openai-tts-runner

| Env var | Required | Purpose |
|---|---|---|
| `MODEL_ID` | no (default `hexgrad/Kokoro-82M`) | Kokoro model id. |
| `MODEL_DIR` | no (default `/models`) | Local model cache. |
| `RUNNER_PORT` | no (default `8080`) | HTTP bind. |
| `LANG_CODE` | no (default per model) | TTS language code. |
| `DEVICE` | no (default `cuda`) | torch device. |

### 10.4 openai-image-generation-runner

| Env var | Required | Purpose |
|---|---|---|
| `MODEL_ID` | yes | HuggingFace diffusers model id. |
| `MODEL_DIR` | no (default `/models`) | Local model cache. |
| `RUNNER_PORT` | no (default `8080`) | HTTP bind. |
| `DEVICE` | no (default `cuda`) | torch device. |
| `DTYPE` | no (default `float16`) | torch dtype. |
| `MAX_QUEUE_SIZE` | no (default 5) | 429 threshold. |
| `USE_TORCH_COMPILE` | no (default `false`) | Toggle torch.compile. |
| `DEFAULT_STEPS` | no (model default) | Inference steps default. |
| `DEFAULT_GUIDANCE` | no (model default) | Guidance scale default. |

### 10.5 rerank-runner

| Env var | Required | Purpose |
|---|---|---|
| `MODEL_ID` | no (default `zeroentropy/zerank-2`) | CrossEncoder model id. |
| `MODEL_DIR` | no (default `/models`) | Local model cache. |
| `RUNNER_PORT` | no (default `8080`) | HTTP bind. |
| `MAX_QUEUE_SIZE` | no (default 5) | 429 threshold. |
| `DEVICE` | no (default `cuda`) | torch device. |
| `DTYPE` | no (default `bfloat16`) | torch dtype. |
| `MAX_BATCH_SIZE` | no (default 1000) | Per-request doc cap. |
| `INFERENCE_BATCH_SIZE` | no (default 64) | Internal `model.predict()` batch. |

### 10.6 transcode-runner / abr-runner

| Env var | Required | Purpose |
|---|---|---|
| `RUNNER_ADDR` | no (default `:8080`) | HTTP bind. |
| `MAX_QUEUE_SIZE` | no (default 5 transcode / 2 abr) | Concurrent job cap. |
| `TEMP_DIR` | no (default `/tmp/transcode` or `/tmp/abr`) | Per-job scratch. |
| `JOB_TTL_SECONDS` | no (default 3600) | In-memory job record TTL. |
| `WEBHOOK_SECRET_DEFAULT` | no | Default HMAC secret if request omits. |

### 10.7 YAML

Runners that need preset declarations embed YAML via `go:embed`
(`transcode-runner/main.go:25-26`); runtime override via
`PRESETS_YAML_PATH` env (existing pattern).

Per OQ1, every runner additionally embeds an offering manifest at
`/etc/runner/offering.yaml` inside the image — declares the offerings
presented to the broker (presets, resolutions, model variants,
rate-card hints) for the capability named by `CAPABILITY_NAME`.
Mounted read-only; operators may bind-mount an override file at the
same path. Schema documented per runner in
`<runner>/docs/offering-schema.md`.

## 11. Conformance / smoke tests

### 11.1 Wire-protocol conformance

Per-mode fixtures already live in
`livepeer-network-protocol/conformance/fixtures/{http-reqresp,http-stream,http-multipart}/`.
Runners are *workload* counterparts to mode fixtures; the conformance
runner exercises broker-side behaviour. No new mode fixtures from
this brief.

### 11.2 Per-runner smokes

Each runner ships a smoke (mostly preserved from byoc):

- `openai-runner/test.sh` — POST a canned chat request; assert 200 +
  `usage`. Run against `compose.ollama.yaml` overlay.
- `openai-audio-runner/test.sh` — POST a small wav; assert 200 +
  `text` field.
- `openai-tts-runner/test.sh` — POST a string; assert 200 + audio
  body bytes.
- `openai-image-generation-runner/` — test via `openai-tester/`'s
  `test-image-generation.mjs`.
- `rerank-runner/test.sh` — POST query + 5 docs; assert 200 + scored
  + reordered.
- `video-runners/transcode-tester/` — submit a fixture mp4; poll
  `/status`; assert HLS-shaped output.

The integration-test harness `openai-tester/` is preserved as a
multi-runner exercise tool.

### 11.3 GPU-required smokes

NVENC + diffusers + Whisper + Kokoro require GPU; smokes run
operator-side on real hardware (per plan 0011-followup §11.4
pattern). CI runs only what fits CPU.

### 11.4 Capability registration probe

A separate smoke validates the orch-coordinator scrape (plan 0018):
`GET /<capability>/options` returns the offerings JSON the
coordinator expects. Each runner's smoke includes this assertion.

## 12. Operator runbook deltas

`<runner>/docs/operator-runbook.md` per runner:

1. **Image build** — `make build` produces the runner image; tag
   matches `tztcloud/<runner-name>:v0.8.10` (current pin per user-memory
   `feedback_no_image_version_bumps.md`; do not bump without
   approval).
2. **Model setup** — run `image-model-downloader` (or per-runner
   `model-downloader`) once per host to pre-pull weights into a
   shared volume; document GB sizing per model (Whisper-large-v3 ~3
   GB; Kokoro-82M ~165 MB; RealVisXL-V4 ~6 GB; zerank-2 ~8 GB).
3. **GPU passthrough** — for Docker: `runtime: nvidia` + nvidia-
   container-toolkit. Document QSV + VAAPI device passthrough
   (`/dev/dri/renderD128`).
4. **Compose overlays** — `openai-runners/compose.ollama.yaml` is
   the chat upstream; vllm overlays for chat + embeddings; audio
   overlay for whisper + tts.
5. **Capability registration** — orch-coordinator (plan 0018)
   scrapes runners' `/options` endpoint server-side; no runner
   restart on coordinator-side roster refresh.
6. **Broker pairing** — operator's `host-config.yaml` declares the
   runner's URL as a backend; broker forwards on capability match.
   Runner has no awareness of broker URL; the broker is the client.
7. **Queue cap tuning** — `MAX_QUEUE_SIZE` default 5; raise when host
   has more capacity, lower under memory pressure.
8. **Healthcheck** — `GET /healthz` returns 503 during model load;
   broker should treat 503 as "not ready" and skip dispatch.
9. **Ollama / vLLM upstream lifecycle** — operator manages those
   services (not in this rewrite); openai-runner is the proxy. Document
   `UPSTREAM_URL` value when running side-by-side with Ollama vs vLLM.
10. **Legacy `live-transcode-runner` retirement** — operators on the
    byoc tree's live-transcode-runner stop using it; per plan
    0011-followup, the broker's RTMP listener + FFmpeg pipeline + LL-HLS
    server replaces it.
11. **GPU-probe fail-fast (OQ3)** — at startup ML runners call
    `torch.cuda.is_available()` (or framework equivalent); when
    `DEVICE=cuda` (or unset, defaulting to `cuda`) and no GPU is
    detected, the runner exits non-zero with the message `cuda device
    requested but no GPU detected; set DEVICE=cpu to fall back to CPU
    runtime`. No 503 graceful-degradation path — silent degraded mode
    hides operator misconfig and customers expect GPU-class
    performance. Operator-side fallback: set `DEVICE=cpu` explicitly.
12. **Multi-arch policy (OQ4)** — ML runners
    (`openai-image-generation-runner`, `openai-audio-runner`,
    `openai-tts-runner`, `rerank-runner`, transcode runners) ship
    **amd64-only** for v0.1; NVIDIA arm64 GPU support exists (Jetson,
    GH200) but isn't the default deployment shape. The Go-based
    `openai-runner/` proxy (chat + embeddings; pure-Go, no GPU need)
    ships **multi-arch (linux/amd64 + linux/arm64)** so operators may
    run it on Apple Silicon dev machines or Graviton instances.
13. **Prometheus integration (OQ5)** — set `METRICS_ENABLED=true` to
    expose `/metrics` (Prometheus exposition format). Default-off; zero
    overhead when unset. Cardinality-capped to `model` + `offering`
    labels (no per-request labels, no `customer_id`, no `request_id`).
    Operators wire the scrape endpoint into their existing Prometheus
    stack; non-prometheus deployments leave the flag unset.

## 13. Migration sequence

6 phases. None chain-gated; all pre-1.0.0-shippable.

### Phase 1 — Component scaffold

Create the three component directories (`openai-runners/`,
`rerank-runner/`, `video-runners/`); land Makefiles, AGENTS.md
shells, dummy compose files. Verify the rewrite root recognizes them
as components.

**Acceptance:** repo `make smoke` skips the new components without
error; AGENTS.md lints clean.

### Phase 1b — python-runner-base/ shared image (OQ2)

Land `openai-runners/python-runner-base/Dockerfile` first, before the
per-runner Python ports rebase on it. The base inherits from
`python:3.12-slim` and bakes in the canonical Python pins (fastapi,
pydantic, pydantic-settings, structlog, uvicorn, prometheus-client).
Each subsequent Python-runner port commit (Phase 3, plus rerank-runner
under §4.2) updates its own Dockerfile to `FROM python-runner-base:<tag>`
and adds only model-specific deps + entrypoint. The Go-based
`openai-runner/` proxy is untouched by this phase.

**Acceptance:** `python-runner-base` image builds in CI; downstream
Python runners' Dockerfiles successfully reference it as their base.

### Phase 2 — openai-runner (Go) port

Copy `openai-runners/openai-runner/{cmd,internal,Dockerfile,go.mod,go.sum}`
into `openai-runners/openai-runner/`. Standardize Go module name;
`go build ./...` green. Smoke against an Ollama compose overlay.

**Acceptance:** chat + embeddings return 200; `/options` returns the
expected JSON; `/healthz` responds.

### Phase 3 — Python runners port (audio / tts / images / rerank)

Copy the four Python runners into their target dirs. Each per-runner
Dockerfile inherits from `python-runner-base` (landed in Phase 1b).
Refactor `app.py` into a small `src/<runner_name>/` Python package;
`pyproject.toml` + `uv.lock` replace `requirements.txt`. Add per-runner
`__main__.py` + console-script entry. Compose overlay to test against
fixture audio / image / docs.

**Acceptance:** all four runners respond to canned smoke requests;
`/options` returns canonical offerings; image is buildable in CI
without GPU (only smoke runs on GPU).

### Phase 4 — video-runners (Go) port

Copy `transcode-core/` (the Go module) + `transcode-runner/` +
`abr-runner/` + `codecs-builder/` + `transcode-tester/` + `data/` +
`docs/` into `video-runners/`. Wire the local-replace directives in
`transcode-runner/go.mod` + `abr-runner/go.mod`. Drop
`live-transcode-runner/` per plan 0011-followup. Smoke against
`data/` fixture mp4.

**Acceptance:** transcode-runner produces a valid HLS-shaped output
for the fixture; abr-runner produces a 5-rung ladder; `/options`
returns expected offerings.

### Phase 5 — Integration with broker + orch-coordinator + retire byoc tree

Wire the runners into a compose stack with `capability-broker` +
`payment-daemon` (receiver mode). The broker forwards a paid request
to the runner; the runner returns the response; the broker debits
the daemon. Verify orch-coordinator's `/options` scrape produces the
expected roster.

Suite-side `livepeer-byoc/` tree gets a `DEPRECATED.md` pointing
here. User retires the byoc repo manually (per memory lock —
clean-slate file copies; no git subtree merge).

**Acceptance:** end-to-end smoke against broker + daemon + runner
for at least one runner per family (one openai, one video, rerank);
roster auto-discovery confirmed; legacy live-transcode-runner retired
per plan 0011-followup.

## 14. Resolved decisions

User walks 2026-05-06 (Q1-Q12) + 2026-05-07 (OQ1-OQ5); recorded as
`DECIDED:` blocks.

### Q1. Three runner components vs one mega-component

**DECIDED: three components** (`openai-runners/`, `rerank-runner/`,
`video-runners/`). Each component groups workload-binaries by domain;
operator-of-one-host typically picks one or two domains, not all
three. Three components cleanly separate Docker tags + compose
overlays + per-domain ML deps.

### Q2. live-transcode-runner — keep or drop

**DECIDED: drop.** Plan 0011-followup retires it; broker's mode driver
+ FFmpeg pipeline replaces it.

### Q3. video-generation runner — keep or drop

**DECIDED: drop.** Per user lock; not needed in v1.0.0. Future
re-introduction is its own plan.

### Q4. register-capabilities sidecar — keep or drop

**DECIDED: drop.** Per plan 0018, orch-coordinator scrapes runners'
`/options` endpoints server-side. Sidecar pattern retires.

### Q5. gateway-proxy — keep or drop

**DECIDED: drop.** Was for go-livepeer; rewrite has no go-livepeer
in production path.

### Q6. deployment-examples — keep or drop

**DECIDED: drop.** Per user lock; rewrite ships its own runbooks.

### Q7. Python deps management — `requirements.txt` or `pyproject.toml`?

**DECIDED: `pyproject.toml` + uv-lock.** Matches vtuber-runner +
vtuber-pipeline migrations; uv is the rewrite's Python dep manager.
The byoc tree's `requirements.txt` files convert mechanically.

### Q8. Per-runner compose overlays preserved or consolidated?

**DECIDED: preserved.** Operators with one upstream (Ollama only) want
just the ollama overlay; multi-overlay deployments compose them.
Consolidating into one mega-compose works against operator habit.
The naming standardizes on `compose.<overlay-slug>.yaml`.

### Q9. Image tag pin — bump or freeze?

**DECIDED: freeze at v0.8.10.** Per user-memory
`feedback_no_image_version_bumps.md`. The migration republishes the
same tag with the new code; no bump as part of the migration.

### Q10. License — preserve per-runner LICENSE files?

**DECIDED: drop per-runner LICENSE.** Repo root LICENSE (MIT) applies.
The byoc tree's LICENSE files retire.

### Q11. transcode-core/live.go — keep or delete?

**DECIDED: keep, audit post-port.** The file may carry residual
live-transcode helpers. After port + reference-check, delete unused
symbols. Don't pre-delete — risks breaking ABR or VOD reference
paths.

### Q12. openai-tester multi-runner harness — keep?

**DECIDED: keep.** Useful for cross-runner smoke; preserved at
`openai-runners/openai-tester/`.

### OQ1. Capability identity declaration — env, YAML, or hybrid?

**DECIDED: env var for capability NAME + YAML manifest for offering
DETAILS.** Each runner exposes a `CAPABILITY_NAME` env var
(image-tag-pinned canonical name, e.g. `openai-chat-completions`,
`openai-embeddings`, `whisper-transcriptions`, `kokoro-tts`,
`image-generation`, `rerank`, `transcode-vod`, `abr-ladder`) — simple,
operator-friendly. Offering details (presets, resolutions, model
variants, rate-card hints) live in an embedded YAML manifest at
`/etc/runner/offering.yaml` per runner image, mounted read-only with
operator overrides as needed. Existing impls mixed env-only and
YAML-only; this lock standardizes the hybrid.

### OQ2. Shared Python base image

**DECIDED: ship `openai-runners/python-runner-base/`.** Single
Dockerfile inheriting from `python:3.12-slim` plus the common deps
(`fastapi`, `pydantic`, `pydantic-settings`, `structlog`, `uvicorn`,
`prometheus-client` per OQ5). Per-runner Dockerfiles
`FROM python-runner-base:<tag>` and add only their model-specific deps
+ entrypoints. Cuts per-image build time (common layer cached once);
standardizes versioning across `openai-image-generation-runner`,
`openai-audio-runner`, `openai-tts-runner`, plus `rerank-runner/`
(sibling component). The Go-based `openai-runner` (chat + embeddings
proxy) doesn't use this base — it's a separate Go runtime.

### OQ3. GPU probe at startup

**DECIDED: fail-fast on `DEVICE=cuda` + no GPU detected.** Runner
calls `torch.cuda.is_available()` (or framework equivalent) at
startup; if `false` and `DEVICE` is set to `cuda` (or unset and
defaulting to `cuda`), the runner exits with non-zero code and a
clear error message: `cuda device requested but no GPU detected;
set DEVICE=cpu to fall back to CPU runtime`. No 503 graceful-
degradation path — silent degraded mode hides operator misconfig and
customers consuming the runner expect GPU-class performance. Operator-
side fallback documented in the runbook (§12).

### OQ4. Multi-arch images

**DECIDED: amd64-only for ML runners
(`openai-image-generation-runner`, `openai-audio-runner`,
`openai-tts-runner`, `rerank-runner`, transcode runners); multi-arch
(linux/amd64 + linux/arm64) for the Go-based `openai-runner` (chat +
embeddings proxy).** Rationale: NVIDIA arm64 GPU support exists
(Jetson, GH200) but isn't the default operator deployment shape; ML
runners stay amd64-only for v0.1. The Go-based proxy is pure-Go with
no GPU need — multi-arch builds are cheap and let operators run the
proxy on Apple Silicon dev machines or cost-efficient Graviton
instances.

### OQ5. Prometheus `/metrics` endpoint

**DECIDED: yes, opt-in behind `METRICS_ENABLED=true` flag.** Each
runner exposes `/metrics` (Prometheus exposition format) when the
flag is set. Cardinality-capped: labels are `model` and `offering`
only; no per-request labels (no `customer_id`, no `request_id`, no
full URI). Default metrics:
`runner_inference_total{outcome=ok|error,model,offering}` (counter),
`runner_inference_duration_seconds{model,offering}` (histogram),
`runner_active_concurrent_inferences` (gauge),
`runner_gpu_utilization_pct{gpu_index}` (gauge). Operators wire into
their existing Prometheus stack; non-prometheus deployments leave the
flag unset (zero overhead).

## 15. Out of scope (forwarding addresses)

- **`livepeer-byoc/gateway-proxy/`** — DROPPED; go-livepeer-only.
- **`livepeer-byoc/video-generation/`** — DROPPED per user lock.
- **`livepeer-byoc/register-capabilities/`** — DROPPED; replaced by
  plan 0018 orch-coordinator scrape.
- **`livepeer-byoc/deployment-examples/`** — DROPPED.
- **`livepeer-byoc/transcode-runners/live-transcode-runner/`** —
  DROPPED per plan 0011-followup (broker takes over).
- **Customer auth / billing / ledger** — `customer-portal/` (plan
  0013-shell) + per-product gateways own these. Runners are blind to
  customer identity.
- **Wire-protocol middleware** — `gateway-adapters/` (plans 0008 +
  0008-followup); gateway-side, not runner-side.
- **Chain integration** — plan 0016; runner-side is unaffected.
- **Mode driver implementations** — `capability-broker/` (plans
  0003 + 0006 + 0011 + 0011-followup + 0012 + 0012-followup).
- **Capability roster UX** — plan 0018 orch-coordinator.
- **Operator installer** — out of scope; future
  `livepeer-up-installer/` plan.
- **`livepeer-modules-project/`** — left alone; user retires
  manually.

---

## Appendix A — file paths cited

This monorepo:

- `docs/exec-plans/active/0013-shell-customer-portal-extraction.md` — sibling.
- `docs/exec-plans/active/0013-openai-gateway-collapse.md` — gateway side.
- `docs/exec-plans/active/0013-vtuber-suite-migration.md` — gateway side.
- `docs/exec-plans/active/0013-video-gateway-migration.md` — gateway side.
- `docs/exec-plans/completed/0011-followup-rtmp-media-pipeline.md` —
  retires live-transcode-runner; locks broker-side video pipeline.
- `docs/exec-plans/active/0018-orch-coordinator-design.md` — capability
  registration shift.

byoc paths cited (pre-migration; ports listed in §5):

- `livepeer-byoc/openai-runners/openai-runner/cmd/{chat,embeddings}/main.go:1-12`
- `livepeer-byoc/openai-runners/openai-runner/internal/runner/runner.go:1-200+,21-25,29-36,71,73,132,146-147`
- `livepeer-byoc/openai-runners/openai-audio-runner/app.py:1-399,64,376,381`
- `livepeer-byoc/openai-runners/openai-tts-runner/app.py:1-290,49,272`
- `livepeer-byoc/openai-runners/openai-image-generation-runner/app.py:1-499,48,468-474`
- `livepeer-byoc/openai-runners/image-model-downloader/{download.py,requirements.txt,Dockerfile}`
- `livepeer-byoc/openai-runners/openai-tester/test-*.mjs`
- `livepeer-byoc/openai-runners/{build.sh,setup-models.sh,docker-compose.*.yml,LICENSE,README.md}`
- `livepeer-byoc/rerank-runner/rerank-runner/app.py:1-317`
- `livepeer-byoc/rerank-runner/{Dockerfile,docker-compose.yml,model-downloader/...,build.sh,test.sh,LICENSE,README.md}`
- `livepeer-byoc/transcode-runners/transcode-core/{abr_presets,ffmpeg,filters,gpu,hls,io,live,presets,progress,thumbnail}.go` + `_test.go`
- `livepeer-byoc/transcode-runners/transcode-runner/main.go:1-827,25-26,795-799`
- `livepeer-byoc/transcode-runners/abr-runner/main.go:1-860,828-832`
- `livepeer-byoc/transcode-runners/codecs-builder/Dockerfile:1-30+`
- `livepeer-byoc/transcode-runners/{build.sh,docker-compose.yml,data,docs,transcode-tester}/`
- `livepeer-byoc/transcode-runners/live-transcode-runner/` (DROPPED)
- `livepeer-byoc/{gateway-proxy,video-generation,register-capabilities,deployment-examples}/` (DROPPED)
