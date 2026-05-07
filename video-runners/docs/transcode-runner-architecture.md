# Transcode Runner Architecture — Decisions & Tradeoffs

**Date:** 2026-03-03
**Status:** Planning Phase
**Authors:** Engineering Team

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Project Context](#2-project-context)
3. [Decision 1: Modular Architecture — Hybrid Runner Split](#3-decision-1-modular-architecture--hybrid-runner-split)
4. [Decision 2: GPU Vendor Strategy — Same Code, Per-Vendor Docker Images](#4-decision-2-gpu-vendor-strategy--same-code-per-vendor-docker-images)
5. [Decision 3: Codec & Format Support](#5-decision-3-codec--format-support)
6. [Decision 4: Input/Output Strategy — HTTP Pull + Pre-signed Upload](#6-decision-4-inputoutput-strategy--http-pull--pre-signed-upload)
7. [Decision 5: Configuration — Presets First, Custom Later](#7-decision-5-configuration--presets-first-custom-later)
8. [Decision 6: ffmpeg Build Strategy — Build from Source](#8-decision-6-ffmpeg-build-strategy--build-from-source)
9. [Decision 7: Language Choice — Go](#9-decision-7-language-choice--go)
10. [Decision 8: No CPU Fallback](#10-decision-8-no-cpu-fallback)
11. [Decision 9: Live ABR — Deferred](#11-decision-9-live-abr--deferred)
12. [Decision 10: ABR Output Strategy — fMP4 Byte-Range HLS](#12-decision-10-abr-output-strategy--fmp4-byte-range-hls)
13. [Decision 11: Notification Model — Polling + Webhook Callbacks](#13-decision-11-notification-model--polling--webhook-callbacks)
14. [Decision 12: Proxy Routing — /v1/video/transcode](#14-decision-12-proxy-routing--v1videotranscode)
15. [BYOC Protocol Integration](#15-byoc-protocol-integration)
16. [Preset Definitions](#16-preset-definitions)
17. [API Contract](#17-api-contract)
18. [Phase Plan](#18-phase-plan)
19. [Transcoding Workload Catalog](#19-transcoding-workload-catalog)
20. [GPU Hardware Matrix](#20-gpu-hardware-matrix)
21. [Open Questions & Future Work](#21-open-questions--future-work)
22. [Decision Summary Table](#22-decision-summary-table)

---

## 1. Executive Summary

This document captures the architectural decisions, evaluated options, and tradeoffs for building a GPU-accelerated transcode runner system within the Livepeer BYOC (Bring Your Own Container) framework. The system enables decentralized video transcoding using NVIDIA NVENC, Intel QuickSync (QSV), and AMD AMF/VAAPI hardware encoders, exposed through the Livepeer BYOC capability registration and routing protocol.

The architecture follows a **hybrid modular approach** with three specialized runners sharing a common Go package, GPU-vendor abstraction via per-vendor Docker images, and a zero-trust input/output model using HTTP URL pull and pre-signed upload URLs suitable for decentralized operation.

---

## 2. Project Context

### 2.1 Existing BYOC Architecture

The current BYOC gateway project exposes AI/ML capabilities through OpenAI-compatible APIs:

```
Client -> OpenAI Proxy (Go, :8090) -> Livepeer Gateway -> Runners -> Backends
```

**Existing runners:**

| Runner | Language | BYOC Model | Purpose |
|--------|----------|------------|---------|
| LLM Runner | Go | Job (sync) | Chat completions via Ollama |
| Image Runner | Python | Job (sync) | Image generation via FLUX/RealVisXL |
| Embeddings Runner | Go | Job (sync) | Text embeddings via Ollama |
| Video Pipeline Runner | Python | Job (async) | Multi-clip video generation via LightX2V/Wan 2.2 |
| Rerank Runner | Go | Job (sync) | Document reranking |

Each runner registers a named **capability** with the Livepeer Orchestrator. The Orchestrator handles routing, capacity tracking, and payment.

### 2.2 BYOC Protocol Models

The BYOC protocol (documented in [go-livepeer/doc/byoc-technical-details.md](https://github.com/Cloud-SPE/go-livepeer/blob/releases/livepeer.cloud/master/doc/byoc-technical-details.md)) defines two communication models:

**Job Model (Batch/Request-Response):**
- `POST /process/request/<sub-path>` routed through Gateway -> Orchestrator -> Worker
- Single HTTP request lifecycle (or SSE for long-running)
- Payment charged once at completion: `rate x ceil(seconds)`
- Suitable for: VOD transcoding, file processing, analysis

**Stream Model (Long-Running/Stateful):**
- `POST /process/stream/start` opens a persistent session with `streamId`
- Media exchange via Trickle pub/sub channels (MPEG-TS)
- Payment debited on a running clock: Orchestrator debits every 23s, Gateway tops up every 50s
- Built-in RTMP and WHIP ingest endpoints
- Suitable for: Live transcoding, real-time processing

### 2.3 BYOC Registration

Each runner registers via `POST /capability/register` to the Orchestrator:

```json
{
  "name": "capability-name",
  "description": "human-readable description",
  "url": "http://worker-host:8080",
  "capacity": 4,
  "price_per_unit": 1,
  "price_scaling": 600,
  "currency": "USD"
}
```

Key constraints:
- **Single URL per capability name** — no built-in load balancing
- **Re-registration resets Load counter to 0** — don't re-register while jobs are in flight
- **No per-gateway pricing** for BYOC capabilities
- **`Capability_BYOC` never in bitstring** — Gateways discover via price list, not `CompatibleWith`

---

## 3. Decision 1: Modular Architecture — Hybrid Runner Split

### Options Evaluated

#### Option A: Split by Workload Type (5 runners)

```
transcode-core/
├── vod-transcode-runner/       capability: "transcode-vod"
├── vod-abr-runner/             capability: "transcode-abr"
├── live-transcode-runner/      capability: "transcode-live"
├── live-abr-runner/            capability: "transcode-live-abr"
└── media-processor-runner/     capability: "media-process"
```

| Pros | Cons |
|------|------|
| Each runner is simple and single-purpose | 5 separate services + 5 capability registrations |
| Easy to scale independently (e.g., 3 VOD + 1 live) | Shared GPU contention between co-located runners |
| Capacity maps cleanly to NVENC session limits per runner | More operational complexity |
| Failure isolation — one runner crash doesn't affect others | More Docker images to build and maintain |

#### Option B: Split by BYOC Protocol Model (2 runners)

```
transcode-core/
├── vod-runner/                 capability: "transcode-vod"
│   Routes: /v1/transcode, /v1/abr, /v1/process
└── live-runner/                capability: "transcode-live"
    Routes: stream/start with params
```

| Pros | Cons |
|------|------|
| Only 2 runners — minimal operational overhead | Each runner is complex internally |
| Clean split along BYOC protocol boundaries | Capacity shared across all VOD workload types |
| Simpler deployment | A thumbnail job competes with 4K ABR encode for same capacity slots |
| Fewer Docker images | Harder to scale specific workloads independently |

#### Option C: Hybrid — Split by Complexity + Protocol (3 runners) **[CHOSEN]**

```
transcode-core/               <- shared Go package
├── transcode-runner/          <- Job Model: single transcode + simple processing
│   capability: "transcode"
├── abr-runner/                <- Job Model: ABR ladder generation
│   capability: "transcode-abr"
└── live-transcode-runner/     <- Stream Model: all live transcoding
    capability: "transcode-live"
```

| Pros | Cons |
|------|------|
| 3 runners — balanced complexity vs modularity | Base transcode-runner handles multiple sub-types |
| ABR isolated (heaviest VOD workload) | Still need GPU contention strategy if co-located |
| Live isolated (different BYOC protocol) | 3 Docker images per GPU vendor (9 total) |
| Natural alignment with BYOC Job vs Stream models | |
| Shared core package reduces code duplication | |

### Rationale for Choice

Option C provides the best balance between:
- **Operational simplicity** — 3 runners vs 5, manageable deployment
- **Workload isolation** — ABR ladders are resource-heavy and long-running; they deserve their own capacity pool. Live transcoding uses a fundamentally different protocol.
- **BYOC alignment** — The Job/Stream model split naturally divides VOD from live, and within VOD, simple transcodes from ABR ladder generation are different enough in resource profile and duration to warrant separation.
- **Shared code** — The `transcode-core` Go package eliminates duplication for GPU detection, ffmpeg command building, presets, and I/O.

### Detailed Runner Responsibilities

#### `transcode-runner` (Job Model)

Handles single-input, single-output transcoding and media processing tasks:

- **Codec conversion** — H.264 <-> H.265/HEVC <-> AV1, ProRes -> H.264, etc.
- **Resolution scaling** — 4K -> 1080p, 1080p -> 720p, etc.
- **Bitrate transcoding** — Re-encode at target bitrate (CRF, CBR, VBR modes)
- **Frame rate conversion** — 60fps -> 30fps, 24fps -> 30fps
- **HDR -> SDR tone mapping** — PQ/HLG -> BT.709
- **Audio transcoding** — AAC, Opus, AC3 conversion; channel downmix
- **Thumbnail/sprite extraction** — I-frame thumbnails, timeline preview sprite sheets
- **Subtitle burn-in** — Hardcoded subtitle overlay
- **Watermarking** — Logo/text overlay
- **Container remuxing** — MKV -> MP4, TS -> MP4 (no re-encode)
- **Video trimming** — Extract time segments

BYOC capability: `"transcode"`
Typical duration: seconds to minutes
Capacity: maps to NVENC session limit (e.g., 3-5 on consumer GPU)

#### `abr-runner` (Job Model)

Handles single-input, multi-output adaptive bitrate ladder generation:

- **ABR ladder generation** — Single input -> multiple renditions at different resolutions/bitrates
- **HLS packaging** — Generate `master.m3u8` + per-rendition `.m3u8` + `.ts`/`.fmp4` segments
- **DASH packaging** — Generate `.mpd` manifest + `.m4s` segments
- **Per-rendition encoding** — Each rung encoded with optimal settings for its resolution
- **Audio-only renditions** — Low-bandwidth audio stream for ABR ladder
- **Quality analysis** — Optional VMAF/SSIM/PSNR scoring per rendition

BYOC capability: `"transcode-abr"`
Typical duration: minutes to hours (proportional to input duration x number of renditions)
Capacity: typically 1-2 concurrent (each job uses multiple NVENC sessions)

#### `live-transcode-runner` (Stream Model)

Handles real-time transcoding of live streams via Trickle channels:

- **Single rendition live transcode** — MPEG-TS in -> MPEG-TS out at target codec/resolution/bitrate
- **Codec conversion** — Real-time H.264 -> H.265 or AV1
- **Resolution downscale** — Real-time 4K -> 1080p, etc.
- **Mid-stream parameter changes** — Bitrate/quality adjustments via `stream/params`

BYOC capability: `"transcode-live"`
Duration: indefinite (billed per second on running clock)
Capacity: maps to available NVENC sessions minus overhead

---

## 4. Decision 2: GPU Vendor Strategy — Same Code, Per-Vendor Docker Images

### Options Evaluated

#### Option 1: Same Code, Per-Vendor Docker Images **[CHOSEN]**

```
transcode-runner/
├── Dockerfile.nvidia     <- CUDA + nv-codec-headers + ffmpeg with --enable-nvenc
├── Dockerfile.intel      <- Intel oneVPL + ffmpeg with --enable-libvpl
├── Dockerfile.amd        <- AMDGPU + VAAPI + ffmpeg with --enable-vaapi/amf
└── main.go               <- identical code, auto-detects GPU at startup
```

The shared `transcode-core` package defines a hardware profile:

```go
type HWProfile struct {
    Vendor      string   // "nvidia", "intel", "amd"
    DeviceName  string   // "NVIDIA GeForce RTX 5090", "Intel Arc A770", etc.
    VRAM        int64    // bytes
    DecodeH264  string   // "h264_cuvid" | "h264_qsv" | "h264_vaapi"
    DecodeHEVC  string   // "hevc_cuvid" | "hevc_qsv" | "hevc_vaapi"
    EncodeH264  string   // "h264_nvenc" | "h264_qsv" | "h264_vaapi"
    EncodeHEVC  string   // "hevc_nvenc" | "hevc_qsv" | "hevc_vaapi"
    EncodeAV1   string   // "av1_nvenc"  | "av1_qsv"  | "av1_vaapi" (empty if unsupported)
    HWAccel     string   // "cuda" | "qsv" | "vaapi"
    MaxSessions int      // NVENC limit, QSV limit, etc.
    Filters     []string // available HW filters ("scale_cuda", "scale_qsv", "scale_vaapi")
}
```

At startup:
1. Probe GPU hardware (`nvidia-smi`, `vainfo`, `ffmpeg -hwaccels`)
2. Build `HWProfile` with detected capabilities
3. Register capability with capacity = `MaxSessions`
4. Refuse to start if no GPU detected (no CPU fallback)

| Pros | Cons |
|------|------|
| Single Go codebase — no logic duplication | 3 Docker images per runner (9 total) |
| GPU-specific optimizations in ffmpeg build | Must test across 3 GPU vendors |
| Clean abstraction via HWProfile | Driver version pinning varies per vendor |
| Runtime auto-detection for flexibility | |

#### Option 2: Separate Runners per GPU Vendor (Rejected)

```
transcode-nvenc-runner/    capability: "transcode-nvenc"
transcode-qsv-runner/     capability: "transcode-qsv"
transcode-amf-runner/      capability: "transcode-amf"
```

| Pros | Cons |
|------|------|
| Each runner optimized for one vendor | Triples the codebase |
| Simpler per-runner Docker builds | Same ffmpeg command logic duplicated 3 times |
| | Capability name leaks hardware detail to clients |
| | Client must know which GPU the runner has |

### Rationale for Choice

GPU vendor is a **deployment concern**, not a **code concern**. The ffmpeg command differences between vendors are limited to:
- Hardware acceleration flag (`-hwaccel cuda` vs `-hwaccel qsv` vs `-hwaccel vaapi`)
- Encoder name (`h264_nvenc` vs `h264_qsv` vs `h264_vaapi`)
- Decoder name (`h264_cuvid` vs `h264_qsv` vs `h264_vaapi`)
- Filter names (`scale_cuda` vs `scale_qsv` vs `scale_vaapi`)

These differences are captured in the `HWProfile` struct and used to parameterize ffmpeg command construction. The Go code never changes — only the Docker image and GPU drivers differ.

This also means **clients don't need to know what GPU the runner has.** They submit a transcode request with codec and quality parameters; the runner uses whatever hardware is available.

---

## 5. Decision 3: Codec & Format Support

### Supported Codecs (GPU-Accelerated)

| Codec | NVIDIA (NVENC) | Intel (QSV) | AMD (AMF/VAAPI) | Use Case |
|-------|:-:|:-:|:-:|---|
| **H.264/AVC** | `h264_nvenc` | `h264_qsv` | `h264_vaapi` | Universal compatibility, streaming, social media |
| **H.265/HEVC** | `hevc_nvenc` | `hevc_qsv` | `hevc_vaapi` | 4K/HDR content, 50% bitrate savings over H.264 |
| **AV1** | `av1_nvenc` (Ada+) | `av1_qsv` (Arc/12th+) | `av1_vaapi` (RDNA3+) | Next-gen streaming, best compression |

**Note:** AV1 hardware encoding requires recent GPU generations:
- NVIDIA: Ada Lovelace (RTX 4000 series) or newer
- Intel: 12th gen (Alder Lake) / Arc GPUs or newer
- AMD: RDNA 3 (RX 7000 series) or newer

Older GPUs that lack AV1 encode will not advertise AV1 support in their HWProfile.

### Supported Containers

| Container | Extension | Primary Use |
|-----------|-----------|-------------|
| MP4 (ISOBMFF) | `.mp4` | Universal playback, VOD delivery |
| MPEG-TS | `.ts` | HLS segments, live transport |
| Fragmented MP4 | `.fmp4` | DASH/CMAF segments, low-latency HLS |
| MKV | `.mkv` | Archive, multi-track (input only) |
| WebM | `.webm` | Web delivery with VP9/AV1 (output only if needed) |

### Audio Codecs

| Codec | Library | Use Case |
|-------|---------|----------|
| AAC-LC | `aac` (native ffmpeg) | Universal compatibility |
| Opus | `libopus` | Best quality-per-bit, WebM/DASH |
| AC3/E-AC3 | `ac3` / `eac3` | Broadcast, surround sound passthrough |
| Passthrough | `copy` | When audio re-encode is unnecessary |

### Subtitle Support

| Format | Operation |
|--------|-----------|
| SRT / VTT | Extract from container, or burn-in (hardcode) via `subtitles` filter |
| ASS / SSA | Burn-in with styled rendering |
| Embedded (CEA-608/708) | Passthrough or extract to SRT |

---

## 6. Decision 4: Input/Output Strategy — HTTP Pull + Pre-signed Upload

### The Core Problem

In a decentralized network:
- **The runner operator is untrusted** — you cannot give them credentials to your storage
- **The client is untrusted by the runner** — the runner cannot let arbitrary clients write to its storage
- **No shared infrastructure** exists between client and runner operators
- **S3 credentials cannot be passed** through the BYOC protocol securely

### Options Evaluated

#### Option A: Pre-signed URLs for Both Input and Output

```json
{
  "input_url": "https://s3.amazonaws.com/bucket/source.mp4?X-Amz-Signature=...&Expires=3600",
  "output_url": "https://s3.amazonaws.com/bucket/output.mp4?X-Amz-Signature=...&Expires=7200",
  "preset": "streaming-1080p"
}
```

| Pros | Cons |
|------|------|
| Zero trust — runner never sees credentials | Client must generate pre-signed URLs before each job |
| Time-limited and scoped to specific keys | Pre-signed URLs can expire during long jobs |
| Works with any S3-compatible provider | Requires client-side S3 SDK integration |
| Industry standard pattern | |

#### Option B: HTTP URL Pull + Pre-signed Upload **[CHOSEN]**

```json
{
  "input_url": "https://cdn.example.com/source.mp4",
  "output_urls": {
    "video": "https://s3.amazonaws.com/bucket/output.mp4?X-Amz-Signature=...",
    "manifest": "https://s3.amazonaws.com/bucket/master.m3u8?X-Amz-Signature=..."
  },
  "preset": "streaming-1080p"
}
```

| Pros | Cons |
|------|------|
| Input flexibility — any HTTP(S) URL source | Runner must handle HTTP redirects, range requests |
| Works with CDNs, IPFS gateways, pre-signed S3, any web server | Input URL may require auth headers (future consideration) |
| Output uses pre-signed PUT — zero trust, time-limited | Long jobs may outlive pre-signed URL expiry |
| Decentralized-friendly — no shared infra needed | Client must still generate pre-signed PUT URLs |
| ABR support — multiple output URLs per job | |

#### Option C: Gateway-Mediated Storage (Rejected for Primary Use)

```
Client uploads to Gateway -> Gateway stores in temp -> Runner downloads ->
Runner transcodes -> Runner returns to Gateway -> Gateway stores
```

| Pros | Cons |
|------|------|
| Simplest for client | Gateway becomes storage bottleneck |
| Runner has no storage concerns | Double bandwidth (client->gateway->runner->gateway->client) |
| | Gateway needs significant storage capacity |
| | Not truly decentralized — Gateway is centralized |

#### Option D: Inline Response (Supplementary, Not Primary)

For small outputs (thumbnails, sprites, audio clips), return data in HTTP response body as base64.

| Pros | Cons |
|------|------|
| No storage needed for small outputs | Not viable for full video transcodes |
| Matches existing image runner pattern | Size limited by HTTP response and BYOC timeout |

### Rationale for Choice

**Option B** provides maximum flexibility while maintaining zero trust:

1. **Input:** Any HTTP(S) URL. The client controls where the source lives:
   - Public CDN URL
   - Pre-signed S3 GET URL
   - IPFS gateway URL (`https://gateway.ipfs.io/ipfs/Qm...`)
   - Authenticated API endpoint (future: auth header passthrough)

2. **Output:** Pre-signed PUT URLs. The client controls where results go:
   - AWS S3 pre-signed PUT
   - GCP Cloud Storage signed URL
   - MinIO pre-signed PUT
   - Cloudflare R2 pre-signed PUT
   - Any S3-compatible storage

3. **For ABR jobs:** Client provides multiple output URLs (one per rendition, one for manifest)

4. **For thumbnails/small outputs:** Supplement with inline base64 response (Option D)

### Expiry Mitigation

For long-running jobs (ABR encoding of long videos), pre-signed URL expiry is a risk. Mitigations:
- **Validation at job start:** Runner checks URL expiry and rejects if insufficient time
- **Generous expiry recommendation:** Documentation suggests 24-hour expiry for long jobs
- **Chunked upload:** For very large outputs, use S3 multipart upload with pre-signed URLs per part

---

## 7. Decision 5: Configuration — Presets First, Custom Later

### Options Evaluated

#### Option 1: Full Custom Parameters Only

Client specifies every parameter (codec, bitrate, resolution, profile, level, etc.)

| Pros | Cons |
|------|------|
| Maximum flexibility | Steep learning curve for clients |
| No preset maintenance | Easy to specify invalid combinations |
| | Every client must understand video encoding |

#### Option 2: Presets Only

Client picks from a fixed set of named presets.

| Pros | Cons |
|------|------|
| Simple API — one field | Inflexible for edge cases |
| Guaranteed valid configurations | Preset set may not cover all needs |
| Easy to document and test | |

#### Option 3: Presets First, Custom Parameters in Future Phase **[CHOSEN]**

Phase 1: Ship with configurable presets. Client sends `{"preset": "streaming-1080p"}`.
Phase 2+: Add full custom parameter support alongside presets. Client can send preset with overrides or fully custom params.

| Pros | Cons |
|------|------|
| Fast to ship — simple API surface | Clients needing custom params must wait |
| Presets enforce valid configurations | Preset definitions need careful design |
| Presets are operator-configurable (YAML/JSON config) | |
| Backward compatible — adding custom params doesn't break preset API | |
| Presets serve as documented examples for future custom params | |

### Rationale for Choice

Presets reduce the API surface for Phase 1, ensure valid encoder configurations, and provide a natural on-ramp. Custom parameters will be added as preset overrides, maintaining backward compatibility.

Presets will be **operator-configurable** via a YAML/JSON configuration file, allowing runner operators to define custom presets for their specific use cases and hardware.

### Preset Definition Details

See [Section 15: Open Questions](#15-open-questions--future-work) — preset definitions are a next-step discussion item.

---

## 8. Decision 6: ffmpeg Build Strategy — Build from Source

### Options Evaluated

#### Option 1: Pre-built ffmpeg (jellyfin-ffmpeg, static builds)

| Pros | Cons |
|------|------|
| No build complexity | May include unnecessary codecs (bloat) |
| Well-tested by large communities | GPU SDK version may lag |
| Quick to get started | AV1 NVENC support may be missing |
| | No control over compile-time optimizations |
| | License implications (GPL deps may be included) |
| | ~500MB+ image size with everything |

#### Option 2: NVIDIA Video Codec SDK Docker base

| Pros | Cons |
|------|------|
| Official NVIDIA support | NVIDIA-only (no Intel/AMD) |
| Guaranteed SDK compatibility | Still need custom ffmpeg build on top |
| | Vendor lock-in for Docker base |

#### Option 3: Build from Source, Multi-Stage Docker **[CHOSEN]**

| Pros | Cons |
|------|------|
| Include exactly what we need — no bloat | Build complexity in Dockerfiles |
| Pin SDK versions to match GPU drivers | Build takes longer (~10-15 min) |
| Latest AV1 NVENC support (nv-codec-headers 12.2+) | Must maintain 3 Dockerfiles |
| Latest SVT-AV1 for software reference encoder | Need CI for multi-vendor builds |
| Control GPL/LGPL licensing boundary | |
| Smaller runtime images (strip unused codecs) | |
| Reproducible builds (pinned versions) | |

### Rationale for Choice

Building from source provides:

1. **SDK version control** — nv-codec-headers must match the installed NVIDIA driver version. Mismatches cause runtime failures.
2. **Latest codec support** — AV1 NVENC requires nv-codec-headers 12.1+, which pre-built packages may not have.
3. **Minimal image size** — Disable unused protocols, demuxers, and filters.
4. **License control** — Carefully manage GPL vs LGPL boundaries.

### Build Architecture

Multi-stage Docker build pattern:

```
Stage 1 (builder):  Compile ffmpeg from source with GPU SDK headers
Stage 2 (runtime):  Copy only ffmpeg binary + required .so libraries
                    + Go runner binary
                    + GPU runtime libraries (CUDA runtime, VA-API, etc.)
```

### SDK Version Pinning

| Component | NVIDIA | Intel | AMD |
|-----------|--------|-------|-----|
| **GPU SDK** | nv-codec-headers 12.2.72.0 | oneVPL 2.10+ (replaces legacy Media SDK) | AMF headers 1.4.33+ |
| **Driver** | 535+ (for CUDA 12.x) | i915 kernel module | amdgpu kernel module |
| **CUDA / VA-API** | CUDA 12.6 runtime | libva 2.20+ | libva 2.20+ (VAAPI) |
| **ffmpeg** | n7.1 | n7.1 | n7.1 |
| **SVT-AV1** | v2.3.0 | v2.3.0 | v2.3.0 |

### ffmpeg Configure Flags

#### NVIDIA Build

```bash
./configure \
  --enable-gpl \
  --enable-nonfree \
  --enable-cuda-nvcc \
  --enable-libnpp \
  --enable-nvenc \
  --enable-nvdec \
  --enable-cuvid \
  --enable-libsvtav1 \
  --enable-libx264 \        # reference encoder (not for primary use)
  --enable-libx265 \        # reference encoder (not for primary use)
  --enable-libopus \
  --enable-libvpx \
  --disable-doc \
  --disable-debug
```

#### Intel Build

```bash
./configure \
  --enable-gpl \
  --enable-nonfree \
  --enable-libvpl \         # oneVPL (replaces libmfx for 12th gen+)
  --enable-vaapi \
  --enable-libsvtav1 \
  --enable-libx264 \
  --enable-libx265 \
  --enable-libopus \
  --enable-libvpx \
  --disable-doc \
  --disable-debug
```

#### AMD Build

```bash
./configure \
  --enable-gpl \
  --enable-nonfree \
  --enable-vaapi \
  --enable-amf \
  --enable-libsvtav1 \
  --enable-libx264 \
  --enable-libx265 \
  --enable-libopus \
  --enable-libvpx \
  --disable-doc \
  --disable-debug
```

### Note on libx264/libx265 Inclusion

Even though the system is GPU-only (no CPU transcoding as a primary path), libx264 and libx265 are included in the ffmpeg build for:
- **Quality analysis** — VMAF scoring requires a reference encode
- **Filter chain intermediates** — Some complex filter graphs (subtitle burn-in, HDR tone mapping) may need software decode/encode for intermediate steps
- **Negligible size impact** — ~2MB additional
- **Not exposed as capabilities** — These are internal to ffmpeg, not advertised to clients

---

## 9. Decision 7: Language Choice — Go

### Options Evaluated

#### Go **[CHOSEN]**

| Pros | Cons |
|------|------|
| Matches most existing runners (LLM, embeddings, register, proxy) | Less ergonomic for ML/AI tasks (not relevant here) |
| Excellent subprocess management (`os/exec`, `io.Pipe`) | |
| Native concurrency (goroutines for stream management) | |
| Strong HTTP server/client stdlib | |
| Single static binary — small Docker images | |
| Fast startup | |
| ffmpeg is always a subprocess regardless of language | |

#### Python (Rejected)

| Pros | Cons |
|------|------|
| Matches video pipeline runner and image runner | Heavier runtime (Python interpreter, venv) |
| ffmpeg-python library available | ffmpeg-python just wraps CLI anyway |
| | Slower startup |
| | GIL limits concurrency for stream management |
| | Larger Docker images |

### Rationale for Choice

The transcode runner's primary job is:
1. Receive HTTP request
2. Construct ffmpeg command from parameters
3. Execute ffmpeg subprocess
4. Monitor progress (parse ffmpeg stderr)
5. Upload result
6. Return response

This is subprocess orchestration, not ML inference. Go excels at subprocess management, streaming I/O, and HTTP serving. ffmpeg is always invoked as a CLI subprocess regardless of language, so Python's ffmpeg-python library provides no meaningful advantage.

For the Stream Model (live transcoding), Go's goroutines and `io.Pipe` naturally model the Trickle channel -> ffmpeg pipe -> Trickle channel flow.

---

## 10. Decision 8: No CPU Fallback

### Decision

**The transcode runner requires a GPU and will refuse to start if no supported GPU is detected.**

There is no software (CPU-based) transcoding fallback.

### Rationale

1. **Performance** — CPU transcoding is 10-50x slower than GPU for equivalent quality. It is not competitive for a paid transcoding service.
2. **Pricing** — BYOC pricing is time-based (`rate x seconds`). CPU transcoding would be unprofitable at competitive prices, or uncompetitive at profitable prices.
3. **Scope** — The goal is GPU-accelerated transcoding. CPU support adds complexity without value.
4. **Simplicity** — Eliminating CPU as a runtime option simplifies the codebase, testing matrix, and documentation.

### Exception

libx264/libx265 are compiled into ffmpeg for internal use (quality analysis, filter chain intermediates) but are **never used as the primary encoder** for client-facing transcode jobs. If no GPU encoder is available for a requested codec, the job is rejected with an error, not silently fallen back to CPU.

---

## 11. Decision 9: Live ABR — Deferred

### Decision

**Multi-rendition live ABR transcoding is deferred to a later phase.** The complexity and open questions warrant a separate design discussion.

### Why Deferred

The BYOC Stream Model provides **one Trickle pub/sub pair per stream**. Live ABR requires outputting multiple renditions simultaneously (e.g., 1080p + 720p + 480p). This creates an architectural challenge:

#### Possible Approaches (To Be Evaluated Later)

1. **Multiple streams** — Client starts N streams, each at different rendition. Runner's ffmpeg does `tee` or `split` internally. Pros: Works within current BYOC protocol. Cons: N capability slots consumed, N payment streams.

2. **Single stream, multiplexed output** — Runner muxes all renditions into one MPEG-TS stream (different PIDs per rendition). Gateway demuxes. Pros: Single capability slot. Cons: Requires custom MPEG-TS multiplexing logic and Gateway-side demux support.

3. **Extend Trickle protocol** — Add support for multiple publish URLs per stream. Pros: Clean protocol-level solution. Cons: Requires `go-livepeer` changes.

4. **Runner-side HLS output** — Runner writes HLS segments directly to S3/CDN instead of using Trickle egress. Events channel carries status. Pros: Decouples output from Trickle. Cons: Adds storage dependency to live path, introduces latency.

### What Is In Scope (Live, Single Rendition)

The `live-transcode-runner` in Phase 2 will support **single-rendition live transcoding**:
- MPEG-TS input via Trickle subscribe channel
- GPU-accelerated transcode (codec, resolution, bitrate)
- MPEG-TS output via Trickle publish channel
- Mid-stream parameter changes via control channel

This is a natural building block — once single rendition works, multi-rendition can be layered on top.

---

## 12. Decision 10: ABR Output Strategy — fMP4 Byte-Range HLS

### The Core Problem

In a decentralized network with zero-trust I/O (Decision 4), ABR output is uniquely challenging. A traditional segmented HLS package for a 2-hour video with 4 renditions produces **~4,800 files** (1,200 segments per rendition). Pre-signing 4,800 URLs before job submission is impractical, and S3 does not support pre-signing a "prefix" — each URL is for one specific key.

### Options Evaluated

#### Option A: fMP4 Byte-Range HLS (Modern HLS) **[CHOSEN — Phase 1]**

Modern HLS (v7+, RFC 8216bis) supports `EXT-X-BYTERANGE` — instead of thousands of small `.ts` segments, each rendition is a **single fragmented MP4 file**, and the playlist references byte ranges within it:

```
master.m3u8                  <- 1 file
1080p/playlist.m3u8          <- 1 file (contains BYTERANGE directives)
1080p/stream.mp4             <- 1 file (fragmented MP4, all segments in one file)
720p/playlist.m3u8
720p/stream.mp4
480p/playlist.m3u8
480p/stream.mp4
360p/playlist.m3u8
360p/stream.mp4
```

**Total files: 9** for a 4-rendition ladder. 9 pre-signed URLs is trivial.

ffmpeg supports this natively:
```bash
ffmpeg -i input.mp4 \
  -c:v h264_nvenc -s 1920x1080 -b:v 5M \
  -f hls -hls_segment_type fmp4 \
  -hls_flags single_file \
  -hls_time 6 \
  1080p/playlist.m3u8
```

| Pros | Cons |
|------|------|
| Only ~9 pre-signed URLs needed | Slightly worse CDN cache granularity (can't cache individual segments independently) |
| Works perfectly with existing pre-signed PUT model | Requires HLS v7+ player support (all modern players support it) |
| Simpler upload logic (large sequential files) | Byte-range requests need CDN/server supporting Range headers (all modern ones do) |
| Lower S3 API costs (fewer PUT requests) | Cannot start playback until manifest + init segment are uploaded |
| ffmpeg native support | |
| Faster upload (fewer HTTP round-trips) | |

**Player compatibility:** hls.js (web), AVPlayer (iOS/macOS), ExoPlayer (Android), VLC — all modern players support fMP4 byte-range HLS. Only very old Smart TV players (`.ts`-only) do not.

#### Option B: Temporary Scoped Credentials (STS) **[PLANNED — Phase 1.5+]**

The client generates temporary, prefix-scoped credentials using AWS STS (or equivalent):

```json
{
  "output_storage": {
    "type": "s3",
    "bucket": "my-bucket",
    "prefix": "videos/job-123/",
    "region": "us-east-1",
    "endpoint": "https://s3.amazonaws.com",
    "credentials": {
      "access_key_id": "ASIA...",
      "secret_access_key": "temp...",
      "session_token": "FwoGZX...",
      "expiration": "2026-03-04T00:00:00Z"
    }
  }
}
```

Credentials scoped via IAM policy to `s3:PutObject` on `my-bucket/videos/job-123/*` only.

| Pros | Cons |
|------|------|
| Unlimited files under prefix — works for traditional segmented HLS/DASH | Runner receives actual credentials (temporary, scoped, but still credentials) |
| Most flexible approach — supports any packaging format | Client must implement STS AssumeRole flow |
| Industry standard for delegated cloud access | STS is AWS-specific; other providers have equivalents but not identical |
| Supports DASH (`.mpd` + `.m4s` segments — also hundreds of files) | More trust than pre-signed URLs |
| Runner can create directory structure dynamically | Not all S3-compatible providers support STS (MinIO does, R2 does, some don't) |

#### Option C: Single Archive Upload (tar.gz) (Rejected as Primary)

Runner packages all HLS/DASH output into a single `.tar.gz`, uploads via one pre-signed PUT URL. Client unpacks server-side.

| Pros | Cons |
|------|------|
| Single pre-signed URL — simplest for runner | Client MUST unpack before serving — extra processing step |
| Works with existing I/O model unchanged | Cannot start playback until fully uploaded + unpacked |
| Any packaging format supported | Double storage briefly (archive + extracted files) |
| | Archive could be very large |

#### Option D: Encode-Only, Client Packages (Rejected as Primary)

Runner encodes each rendition as a standalone MP4 (one pre-signed URL per rendition). Client handles HLS/DASH packaging.

| Pros | Cons |
|------|------|
| 4-6 pre-signed URLs — manageable | Client must handle packaging (needs ffmpeg/MP4Box) |
| Clean separation of concerns | Loses "turnkey ABR" value proposition |
| MP4 outputs useful on their own | Two-step process for client |

### Rationale for Choice

**Phase 1:** fMP4 byte-range HLS (Option A) — solves 90%+ of use cases, works with existing pre-signed URL model, requires no credentials passing, all modern players support it. The output URL structure is predictable and manageable (~9 files for 4 renditions).

**Phase 1.5+:** Add temporary credentials (Option B) for clients who need traditional segmented HLS/DASH (broadcast workflows, legacy player support, CDN-optimized per-segment caching).

### Output URL Structure for fMP4 Byte-Range HLS

```json
{
  "output_urls": {
    "manifest": "https://s3.example.com/video/master.m3u8?X-Amz-Signature=...",
    "renditions": {
      "1080p": {
        "playlist": "https://s3.example.com/video/1080p/playlist.m3u8?sig=...",
        "stream": "https://s3.example.com/video/1080p/stream.mp4?sig=..."
      },
      "720p": {
        "playlist": "https://s3.example.com/video/720p/playlist.m3u8?sig=...",
        "stream": "https://s3.example.com/video/720p/stream.mp4?sig=..."
      },
      "480p": {
        "playlist": "https://s3.example.com/video/480p/playlist.m3u8?sig=...",
        "stream": "https://s3.example.com/video/480p/stream.mp4?sig=..."
      },
      "360p": {
        "playlist": "https://s3.example.com/video/360p/playlist.m3u8?sig=...",
        "stream": "https://s3.example.com/video/360p/stream.mp4?sig=..."
      }
    }
  }
}
```

### Pre-signed URL Expiry Mitigation

For long-running ABR jobs, pre-signed URLs may expire before upload completes. Mitigations:

1. **Validation at job start** — Runner checks URL expiry timestamp (embedded in signature) and rejects with `OUTPUT_URL_EXPIRY_TOO_SHORT` if insufficient time remaining
2. **Documentation recommendation** — Suggest 24-hour expiry for long-form content
3. **Progressive upload** — Runner uploads each rendition as it completes (not all at the end), reducing time between URL generation and use
4. **Future: Refresh mechanism** — Webhook callback could request fresh URLs mid-job

---

## 13. Decision 11: Notification Model — Polling + Webhook Callbacks

### Decision

**Support both polling (primary) and webhook callbacks (optional)** for job status notification.

### Polling (Always Available)

Matches the existing video pipeline runner pattern:

```
POST /v1/video/transcode/status
Body: {"job_id": "abc123"}
Response: {status, phase, progress, ...}
```

Clients poll at their desired interval. Simple, stateless, works through firewalls/CDNs/proxies.

### Webhook Callbacks (Optional)

Client provides a `webhook_url` in the job request. The runner sends HTTP POST callbacks at key lifecycle events:

```json
// In job submission request:
{
  "input_url": "...",
  "output_url": "...",
  "preset": "streaming-1080p",
  "webhook_url": "https://client.example.com/hooks/transcode",
  "webhook_secret": "whsec_abc123..."
}
```

#### Webhook Payload

```json
{
  "event": "job.progress",
  "job_id": "a1b2c3d4e5f6",
  "timestamp": "2026-03-03T12:05:30Z",
  "data": {
    "status": "processing",
    "phase": "encoding",
    "progress": 45.2,
    "fps": 142.5,
    "speed": "5.9x"
  }
}
```

#### Webhook Events

| Event | When Fired | Notes |
|-------|------------|-------|
| `job.started` | Job begins processing (picked up from queue) | Includes input probe results |
| `job.progress` | Periodic during encoding | Every 10% or every 30 seconds, whichever comes first |
| `job.rendition.complete` | ABR: one rendition finished | Includes rendition name, output URL |
| `job.complete` | Job fully complete, all outputs uploaded | Includes all output URLs, duration, file sizes |
| `job.error` | Job failed | Includes error code and message |

#### Webhook Security

- **HMAC-SHA256 signature** in `X-Webhook-Signature` header, using `webhook_secret` as key
- **Timestamp** in `X-Webhook-Timestamp` header for replay prevention
- **Retry policy:** 3 retries with exponential backoff (1s, 5s, 25s) on non-2xx response
- **Timeout:** 10 seconds per webhook delivery attempt
- **Failure is non-blocking:** Webhook delivery failure does not affect job execution. Client can always fall back to polling.

#### Signature Verification (Client-Side)

```
expected_sig = HMAC-SHA256(webhook_secret, timestamp + "." + body)
verify: X-Webhook-Signature == expected_sig
verify: abs(now - X-Webhook-Timestamp) < 300 seconds
```

### Rationale

- **Polling** is the primary mechanism — simple, stateless, works everywhere, matches existing BYOC patterns
- **Webhooks** eliminate the need for clients to poll, reducing latency-to-notification and API load
- **Webhook failure is non-blocking** — if the client's endpoint is down, the job still completes and results are retrievable via polling
- **HMAC signing** prevents spoofed webhook deliveries in a decentralized environment

---

## 14. Decision 12: Proxy Routing — /v1/video/transcode

### Decision

Transcode endpoints are namespaced under `/v1/video/transcode` in the OpenAI Gateway Proxy.

### Route Table

| Proxy Route | Runner | BYOC Capability | Purpose |
|---|---|---|---|
| `POST /v1/video/transcode` | transcode-runner | `transcode` | Submit VOD transcode job |
| `POST /v1/video/transcode/status` | transcode-runner | `transcode` | Poll job status |
| `POST /v1/video/transcode/abr` | abr-runner | `transcode-abr` | Submit ABR ladder job |
| `POST /v1/video/transcode/abr/status` | abr-runner | `transcode-abr` | Poll ABR job status |
| `POST /v1/video/transcode/presets` | transcode-runner | `transcode` | List available presets |

### Routing in Proxy (`proxy/main.go`)

The proxy maps these public routes to internal BYOC gateway paths:

```
/v1/video/transcode        -> /process/request/v1/video/transcode
/v1/video/transcode/status -> /process/request/v1/video/transcode/status
/v1/video/transcode/abr    -> /process/request/v1/video/transcode/abr
```

Each route sets the appropriate `capability` in the Livepeer header for BYOC routing.

### Rationale

- `/v1/video/` namespace groups all video-related endpoints (existing: `/v1/video/pipeline/generations`)
- `/transcode` is clear and descriptive
- `/status` suffix matches the existing video pipeline polling pattern
- `/presets` endpoint allows clients to discover available presets without documentation

---

## 15. BYOC Protocol Integration

### Capability Registration

Each runner registers independently:

| Runner | Capability Name | Capacity | BYOC Model |
|--------|----------------|----------|------------|
| `transcode-runner` | `transcode` | GPU session limit (e.g., 3-5) | Job |
| `abr-runner` | `transcode-abr` | 1-2 (each job uses multiple sessions) | Job |
| `live-transcode-runner` | `transcode-live` | GPU session limit minus 1-2 headroom | Stream |

### Request Routing

```
Client -> Proxy -> Gateway -> Orchestrator -> Runner
                              (routes by capability name)
```

For VOD (Job Model):
```
POST /process/request/v1/transcode     -> transcode-runner
POST /process/request/v1/transcode/abr -> abr-runner
```

For Live (Stream Model):
```
POST /process/stream/start             -> live-transcode-runner
```

### Payment Mapping

Transcoding pricing is time-based (how long the GPU works), aligning with BYOC's `rate x seconds` model:

- **VOD jobs:** Charge = `price_per_unit / price_scaling x ceil(processing_seconds)`
- **Live streams:** Debit every 23s, top-up every 50s

Example pricing:
```json
{
  "price_per_unit": 1,
  "price_scaling": 600,
  "currency": "USD"
}
```
= $0.10/minute = $6.00/hour of GPU transcoding time.

Operators can price differently per capability (e.g., ABR more expensive than simple transcode).

---

## 16. Preset Definitions

### Overview

Presets are operator-configurable named encoding profiles shipped as a YAML configuration file. Each preset fully specifies the ffmpeg encoding parameters, ensuring valid configurations and simplifying the client API.

Presets are loaded at runner startup and served via `GET /v1/video/transcode/presets`. Operators can add, modify, or remove presets without code changes.

### Default Preset Catalog

#### Streaming Presets (Single-Output, H.264)

| Preset Name | Codec | Resolution | Bitrate | Rate Control | Profile | GOP | Audio | Container |
|---|---|---|---|---|---|---|---|---|
| `streaming-4k` | H.264 | 3840x2160 | 15 Mbps | VBR (max 22.5M) | High L5.1 | 2s | AAC 128k stereo | MP4 |
| `streaming-1080p` | H.264 | 1920x1080 | 5 Mbps | VBR (max 7.5M) | High L4.1 | 2s | AAC 128k stereo | MP4 |
| `streaming-720p` | H.264 | 1280x720 | 2.5 Mbps | VBR (max 3.75M) | High L3.1 | 2s | AAC 96k stereo | MP4 |
| `streaming-480p` | H.264 | 854x480 | 1 Mbps | VBR (max 1.5M) | Main L3.0 | 2s | AAC 96k stereo | MP4 |
| `streaming-360p` | H.264 | 640x360 | 600 kbps | VBR (max 900k) | Main L3.0 | 2s | AAC 64k stereo | MP4 |

#### Next-Gen Streaming Presets (HEVC & AV1)

| Preset Name | Codec | Resolution | Bitrate | Notes |
|---|---|---|---|---|
| `streaming-4k-hevc` | HEVC Main | 3840x2160 | 10 Mbps VBR | ~40% bitrate savings vs H.264 |
| `streaming-1080p-hevc` | HEVC Main | 1920x1080 | 3 Mbps VBR | |
| `streaming-720p-hevc` | HEVC Main | 1280x720 | 1.5 Mbps VBR | |
| `streaming-4k-av1` | AV1 Main | 3840x2160 | 8 Mbps VBR | Best compression; requires Ada/Arc/RDNA3+ GPU |
| `streaming-1080p-av1` | AV1 Main | 1920x1080 | 2.5 Mbps VBR | |
| `streaming-720p-av1` | AV1 Main | 1280x720 | 1.2 Mbps VBR | |

#### Social Media Presets

| Preset Name | Codec | Resolution | Aspect | Bitrate | Max Duration | Notes |
|---|---|---|---|---|---|---|
| `social-landscape` | H.264 High | 1920x1080 | 16:9 | 8 Mbps VBR | source | YouTube, Twitter/X |
| `social-vertical` | H.264 High | 1080x1920 | 9:16 | 6 Mbps VBR | 60s | TikTok, Reels, Shorts |
| `social-square` | H.264 High | 1080x1080 | 1:1 | 5 Mbps VBR | source | Instagram feed |
| `social-stories` | H.264 High | 1080x1920 | 9:16 | 4 Mbps VBR | 15s | Instagram/FB Stories |

#### Archive & Mezzanine Presets

| Preset Name | Codec | Resolution | Quality | Notes |
|---|---|---|---|---|
| `archive-hevc` | HEVC Main10 | source | CRF 18 | High quality long-term storage |
| `archive-av1` | AV1 Main | source | CRF 24 | Best compression for archive at high quality |
| `proxy-edit` | H.264 Main | 1280x720 | CRF 23 | Lightweight NLE editing proxy |

#### Utility Presets

| Preset Name | Type | Output | Notes |
|---|---|---|---|
| `thumbnail` | extraction | JPEG | Single thumbnail at specified timestamp (default: 10%) |
| `thumbnails-grid` | extraction | JPEG | Sprite sheet (4x4 grid, 16 thumbnails evenly spaced) |
| `audio-extract` | extraction | AAC/MP4 | Audio-only extraction (no video encode) |
| `remux-mp4` | remux | MP4 | Container change only, no re-encode |

#### ABR Ladder Presets (for abr-runner)

| Preset Name | Renditions | Segment Type | Format | Notes |
|---|---|---|---|---|
| `abr-standard` | 1080p / 720p / 480p / 360p | fMP4 single-file | HLS | Standard 4-rung ladder |
| `abr-premium` | 4K / 1080p / 720p / 480p / 360p + audio-only | fMP4 single-file | HLS | Premium 5+1 rung ladder |
| `abr-mobile` | 720p / 480p / 360p | fMP4 single-file | HLS | Bandwidth-constrained |
| `abr-hevc` | 4K-HEVC / 1080p-HEVC / 720p-H264 / 480p-H264 | fMP4 single-file | HLS | HEVC top rungs, H.264 fallback |
| `abr-av1` | 4K-AV1 / 1080p-AV1 / 720p-AV1 / 480p-H264 | fMP4 single-file | HLS | AV1 top rungs, H.264 fallback |

### Preset YAML Configuration File

```yaml
# presets.yaml — Operator-configurable transcoding presets
# Loaded at runner startup. Changes require runner restart (hot-reload planned for future).

version: "1.0"

presets:
  # --- Streaming Presets ---
  streaming-1080p:
    description: "Standard 1080p streaming encode"
    type: transcode
    video:
      codec: h264
      width: 1920
      height: 1080
      bitrate: "5M"
      max_bitrate: "7.5M"
      buffer_size: "10M"
      rate_control: vbr
      profile: high
      level: "4.1"
      fps: source                  # preserve source FPS
      gop_seconds: 2              # 2-second GOP for HLS segment alignment
      b_frames: 3
      ref_frames: 4
    audio:
      codec: aac
      bitrate: "128k"
      channels: 2
      sample_rate: 48000
    container: mp4
    scaling:
      algorithm: lanczos           # lanczos, bilinear, bicubic
      force_divisible_by: 2        # ensure dimensions divisible by 2

  streaming-1080p-hevc:
    description: "1080p HEVC streaming — 40% smaller than H.264"
    type: transcode
    video:
      codec: hevc
      width: 1920
      height: 1080
      bitrate: "3M"
      max_bitrate: "4.5M"
      buffer_size: "6M"
      rate_control: vbr
      profile: main
      fps: source
      gop_seconds: 2
    audio:
      codec: aac
      bitrate: "128k"
      channels: 2
      sample_rate: 48000
    container: mp4

  streaming-1080p-av1:
    description: "1080p AV1 streaming — best compression"
    type: transcode
    requires_gpu_feature: av1      # runner validates GPU supports AV1 encode
    video:
      codec: av1
      width: 1920
      height: 1080
      bitrate: "2.5M"
      max_bitrate: "3.75M"
      buffer_size: "5M"
      rate_control: vbr
      profile: main
      fps: source
      gop_seconds: 2
    audio:
      codec: aac
      bitrate: "128k"
      channels: 2
      sample_rate: 48000
    container: mp4

  # --- Social Media Presets ---
  social-vertical:
    description: "Vertical video for TikTok, Reels, Shorts"
    type: transcode
    video:
      codec: h264
      width: 1080
      height: 1920
      bitrate: "6M"
      max_bitrate: "9M"
      rate_control: vbr
      profile: high
      fps: 30                     # cap at 30fps for social
      gop_seconds: 2
    audio:
      codec: aac
      bitrate: "128k"
      channels: 2
      sample_rate: 48000
    container: mp4

  # --- Archive Presets ---
  archive-hevc:
    description: "High-quality HEVC archive"
    type: transcode
    video:
      codec: hevc
      width: source               # preserve source resolution
      height: source
      rate_control: crf
      crf: 18
      profile: main10
      fps: source
    audio:
      codec: aac
      bitrate: "192k"
      channels: source            # preserve source channels
      sample_rate: 48000
    container: mp4

  # --- ABR Ladder Presets ---
  abr-standard:
    description: "Standard 4-rung HLS ABR ladder"
    type: abr
    format: hls
    hls_mode: fmp4_single_file    # fMP4 byte-range (Decision 10)
    segment_duration: 6
    renditions:
      - name: "1080p"
        video:
          codec: h264
          width: 1920
          height: 1080
          bitrate: "5M"
          max_bitrate: "7.5M"
          profile: high
          level: "4.1"
        audio:
          codec: aac
          bitrate: "128k"
          channels: 2

      - name: "720p"
        video:
          codec: h264
          width: 1280
          height: 720
          bitrate: "2.5M"
          max_bitrate: "3.75M"
          profile: high
          level: "3.1"
        audio:
          codec: aac
          bitrate: "96k"
          channels: 2

      - name: "480p"
        video:
          codec: h264
          width: 854
          height: 480
          bitrate: "1M"
          max_bitrate: "1.5M"
          profile: main
          level: "3.0"
        audio:
          codec: aac
          bitrate: "96k"
          channels: 2

      - name: "360p"
        video:
          codec: h264
          width: 640
          height: 360
          bitrate: "600k"
          max_bitrate: "900k"
          profile: main
          level: "3.0"
        audio:
          codec: aac
          bitrate: "64k"
          channels: 2

  abr-premium:
    description: "Premium 5+1 rung HLS ABR ladder with 4K and audio-only"
    type: abr
    format: hls
    hls_mode: fmp4_single_file
    segment_duration: 6
    renditions:
      - name: "4k"
        video: { codec: h264, width: 3840, height: 2160, bitrate: "15M", profile: high, level: "5.1" }
        audio: { codec: aac, bitrate: "128k", channels: 2 }
      - name: "1080p"
        video: { codec: h264, width: 1920, height: 1080, bitrate: "5M", profile: high, level: "4.1" }
        audio: { codec: aac, bitrate: "128k", channels: 2 }
      - name: "720p"
        video: { codec: h264, width: 1280, height: 720, bitrate: "2.5M", profile: high, level: "3.1" }
        audio: { codec: aac, bitrate: "96k", channels: 2 }
      - name: "480p"
        video: { codec: h264, width: 854, height: 480, bitrate: "1M", profile: main, level: "3.0" }
        audio: { codec: aac, bitrate: "96k", channels: 2 }
      - name: "360p"
        video: { codec: h264, width: 640, height: 360, bitrate: "600k", profile: main, level: "3.0" }
        audio: { codec: aac, bitrate: "64k", channels: 2 }
      - name: "audio-only"
        video: null
        audio: { codec: aac, bitrate: "64k", channels: 2 }

  # --- Utility Presets ---
  thumbnail:
    description: "Extract single thumbnail at 10% mark"
    type: utility
    operation: thumbnail
    format: jpeg
    quality: 85
    position: "10%"               # 10% into the video

  thumbnails-grid:
    description: "4x4 sprite sheet (16 evenly-spaced thumbnails)"
    type: utility
    operation: sprite_sheet
    format: jpeg
    quality: 75
    columns: 4
    rows: 4
    thumb_width: 320
    thumb_height: 180
```

### Preset Validation at Startup

When the runner starts, it validates presets against the detected GPU capabilities:

1. **AV1 presets** — marked with `requires_gpu_feature: av1`. If GPU doesn't support AV1 encode (e.g., Ampere/RDNA2 or older), these presets are disabled and excluded from the `/presets` response.
2. **HEVC presets** — validated against GPU HEVC encode support.
3. **Resolution limits** — validated against known GPU encoder limits (e.g., NVENC max 8K).
4. **Invalid presets** — logged as warnings but don't prevent startup. Only valid presets are registered.

---

## 17. API Contract

### 17.1 `transcode-runner` API (Job Model)

#### Submit Job: `POST /v1/video/transcode`

**Request:**
```json
{
  "input_url": "https://cdn.example.com/source.mp4",
  "output_url": "https://s3.amazonaws.com/bucket/output.mp4?X-Amz-Signature=...",
  "preset": "streaming-1080p",
  "webhook_url": "https://client.example.com/hooks/transcode",
  "webhook_secret": "whsec_abc123def456"
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `input_url` | string (URL) | Yes | HTTP(S) URL of source video |
| `output_url` | string (URL) | Yes | Pre-signed PUT URL for output |
| `preset` | string | Yes | Preset name (from `/presets` endpoint) |
| `webhook_url` | string (URL) | No | URL for webhook callbacks |
| `webhook_secret` | string | No | HMAC-SHA256 secret for webhook signing |

**Response (202 Accepted):**
```json
{
  "job_id": "a1b2c3d4e5f6",
  "status": "queued",
  "created_at": "2026-03-03T12:00:00Z",
  "preset": "streaming-1080p"
}
```

#### Poll Status: `POST /v1/video/transcode/status`

**Request:**
```json
{
  "job_id": "a1b2c3d4e5f6"
}
```

**Response (Processing):**
```json
{
  "job_id": "a1b2c3d4e5f6",
  "status": "processing",
  "phase": "encoding",
  "progress": 45.2,
  "encoding_fps": 142.5,
  "speed": "5.9x",
  "eta_seconds": 12,
  "input": {
    "url": "https://cdn.example.com/source.mp4",
    "duration_seconds": 120.5,
    "resolution": "3840x2160",
    "codec": "h264",
    "fps": 24.0,
    "bitrate_kbps": 45000,
    "file_size_bytes": 678000000
  },
  "output": {
    "preset": "streaming-1080p",
    "codec": "h264",
    "resolution": "1920x1080"
  }
}
```

**Response (Complete):**
```json
{
  "job_id": "a1b2c3d4e5f6",
  "status": "complete",
  "phase": "complete",
  "progress": 100.0,
  "output_url": "https://s3.amazonaws.com/bucket/output.mp4",
  "input": {
    "url": "https://cdn.example.com/source.mp4",
    "duration_seconds": 120.5,
    "resolution": "3840x2160",
    "codec": "h264",
    "fps": 24.0,
    "bitrate_kbps": 45000,
    "file_size_bytes": 678000000
  },
  "output": {
    "preset": "streaming-1080p",
    "codec": "h264",
    "resolution": "1920x1080",
    "bitrate_kbps": 4850,
    "duration_seconds": 120.5,
    "fps": 24.0,
    "file_size_bytes": 72940000
  },
  "processing_time_seconds": 20.4,
  "gpu": "NVIDIA GeForce RTX 5090",
  "created_at": "2026-03-03T12:00:00Z",
  "completed_at": "2026-03-03T12:00:20Z"
}
```

**Response (Error):**
```json
{
  "job_id": "a1b2c3d4e5f6",
  "status": "error",
  "phase": "error",
  "error": "Input URL returned 403 Forbidden after 3 retries",
  "error_code": "INPUT_FETCH_FAILED",
  "created_at": "2026-03-03T12:00:00Z",
  "completed_at": "2026-03-03T12:00:05Z"
}
```

#### List Presets: `GET /v1/video/transcode/presets`

**Response:**
```json
{
  "presets": [
    {
      "name": "streaming-1080p",
      "description": "Standard 1080p streaming encode",
      "type": "transcode",
      "codec": "h264",
      "resolution": "1920x1080",
      "bitrate": "5M"
    },
    {
      "name": "streaming-1080p-av1",
      "description": "1080p AV1 streaming — best compression",
      "type": "transcode",
      "codec": "av1",
      "resolution": "1920x1080",
      "bitrate": "2.5M",
      "requires_gpu_feature": "av1",
      "available": true
    }
  ],
  "gpu": {
    "vendor": "nvidia",
    "name": "NVIDIA GeForce RTX 5090",
    "features": ["h264", "hevc", "av1"]
  }
}
```

#### Health Check: `GET /healthz`

**Response:**
```json
{
  "status": "healthy",
  "gpu": {
    "vendor": "nvidia",
    "name": "NVIDIA GeForce RTX 5090",
    "vram_mb": 32768,
    "driver_version": "570.86.16",
    "hw_accel": "cuda",
    "encoders": ["h264_nvenc", "hevc_nvenc", "av1_nvenc"],
    "decoders": ["h264_cuvid", "hevc_cuvid", "av1_cuvid", "vp9_cuvid"],
    "max_sessions": 5
  },
  "ffmpeg_version": "7.1",
  "active_jobs": 1,
  "capacity": 5,
  "presets_loaded": 15,
  "uptime_seconds": 3600
}
```

### 17.2 `abr-runner` API (Job Model)

#### Submit ABR Job: `POST /v1/video/transcode/abr`

**Request:**
```json
{
  "input_url": "https://cdn.example.com/source.mp4",
  "output_urls": {
    "manifest": "https://s3.example.com/video/master.m3u8?X-Amz-Signature=...",
    "renditions": {
      "1080p": {
        "playlist": "https://s3.example.com/video/1080p/playlist.m3u8?sig=...",
        "stream": "https://s3.example.com/video/1080p/stream.mp4?sig=..."
      },
      "720p": {
        "playlist": "https://s3.example.com/video/720p/playlist.m3u8?sig=...",
        "stream": "https://s3.example.com/video/720p/stream.mp4?sig=..."
      },
      "480p": {
        "playlist": "https://s3.example.com/video/480p/playlist.m3u8?sig=...",
        "stream": "https://s3.example.com/video/480p/stream.mp4?sig=..."
      },
      "360p": {
        "playlist": "https://s3.example.com/video/360p/playlist.m3u8?sig=...",
        "stream": "https://s3.example.com/video/360p/stream.mp4?sig=..."
      }
    }
  },
  "preset": "abr-standard",
  "webhook_url": "https://client.example.com/hooks/transcode",
  "webhook_secret": "whsec_abc123def456"
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `input_url` | string (URL) | Yes | HTTP(S) URL of source video |
| `output_urls` | object | Yes | Pre-signed PUT URLs for manifest + per-rendition playlist and stream |
| `preset` | string | Yes | ABR preset name |
| `webhook_url` | string (URL) | No | Webhook callback URL |
| `webhook_secret` | string | No | Webhook HMAC secret |

**Response (202 Accepted):**
```json
{
  "job_id": "f6e5d4c3b2a1",
  "status": "queued",
  "created_at": "2026-03-03T12:00:00Z",
  "preset": "abr-standard",
  "renditions": ["1080p", "720p", "480p", "360p"]
}
```

#### Poll ABR Status: `POST /v1/video/transcode/abr/status`

**Request:**
```json
{
  "job_id": "f6e5d4c3b2a1"
}
```

**Response (Processing):**
```json
{
  "job_id": "f6e5d4c3b2a1",
  "status": "processing",
  "phase": "encoding",
  "overall_progress": 41.8,
  "renditions": [
    {
      "name": "1080p",
      "status": "complete",
      "progress": 100.0,
      "output_urls": {
        "playlist": "https://s3.example.com/video/1080p/playlist.m3u8",
        "stream": "https://s3.example.com/video/1080p/stream.mp4"
      },
      "bitrate_kbps": 4920,
      "file_size_bytes": 73800000
    },
    {
      "name": "720p",
      "status": "encoding",
      "progress": 67.3,
      "encoding_fps": 210.5,
      "speed": "8.8x"
    },
    {
      "name": "480p",
      "status": "queued",
      "progress": 0.0
    },
    {
      "name": "360p",
      "status": "queued",
      "progress": 0.0
    }
  ],
  "input": {
    "url": "https://cdn.example.com/source.mp4",
    "duration_seconds": 120.5,
    "resolution": "3840x2160",
    "codec": "h264",
    "fps": 24.0
  }
}
```

**Response (Complete):**
```json
{
  "job_id": "f6e5d4c3b2a1",
  "status": "complete",
  "phase": "complete",
  "overall_progress": 100.0,
  "manifest_url": "https://s3.example.com/video/master.m3u8",
  "renditions": [
    {
      "name": "1080p",
      "status": "complete",
      "progress": 100.0,
      "output_urls": {
        "playlist": "https://s3.example.com/video/1080p/playlist.m3u8",
        "stream": "https://s3.example.com/video/1080p/stream.mp4"
      },
      "bitrate_kbps": 4920,
      "file_size_bytes": 73800000
    },
    {
      "name": "720p",
      "status": "complete",
      "progress": 100.0,
      "output_urls": {
        "playlist": "https://s3.example.com/video/720p/playlist.m3u8",
        "stream": "https://s3.example.com/video/720p/stream.mp4"
      },
      "bitrate_kbps": 2380,
      "file_size_bytes": 35700000
    },
    {
      "name": "480p",
      "status": "complete",
      "progress": 100.0,
      "output_urls": {
        "playlist": "https://s3.example.com/video/480p/playlist.m3u8",
        "stream": "https://s3.example.com/video/480p/stream.mp4"
      },
      "bitrate_kbps": 980,
      "file_size_bytes": 14700000
    },
    {
      "name": "360p",
      "status": "complete",
      "progress": 100.0,
      "output_urls": {
        "playlist": "https://s3.example.com/video/360p/playlist.m3u8",
        "stream": "https://s3.example.com/video/360p/stream.mp4"
      },
      "bitrate_kbps": 580,
      "file_size_bytes": 8700000
    }
  ],
  "processing_time_seconds": 85.3,
  "gpu": "NVIDIA GeForce RTX 5090",
  "created_at": "2026-03-03T12:00:00Z",
  "completed_at": "2026-03-03T12:01:25Z"
}
```

### 17.3 Webhook Callback Contract

#### Webhook Delivery

```
POST {webhook_url}
Content-Type: application/json
X-Webhook-Signature: sha256=<HMAC-SHA256(webhook_secret, timestamp.body)>
X-Webhook-Timestamp: 1709467530
X-Webhook-Event: job.progress
X-Webhook-Job-Id: a1b2c3d4e5f6
```

#### Event Payloads

**`job.started`:**
```json
{
  "event": "job.started",
  "job_id": "a1b2c3d4e5f6",
  "timestamp": "2026-03-03T12:00:02Z",
  "data": {
    "status": "processing",
    "phase": "probing",
    "input": {
      "duration_seconds": 120.5,
      "resolution": "3840x2160",
      "codec": "h264",
      "fps": 24.0,
      "bitrate_kbps": 45000
    },
    "preset": "streaming-1080p"
  }
}
```

**`job.progress`:**
```json
{
  "event": "job.progress",
  "job_id": "a1b2c3d4e5f6",
  "timestamp": "2026-03-03T12:00:10Z",
  "data": {
    "status": "processing",
    "phase": "encoding",
    "progress": 45.2,
    "encoding_fps": 142.5,
    "speed": "5.9x",
    "eta_seconds": 12
  }
}
```

**`job.rendition.complete`** (ABR only):
```json
{
  "event": "job.rendition.complete",
  "job_id": "f6e5d4c3b2a1",
  "timestamp": "2026-03-03T12:00:30Z",
  "data": {
    "rendition": "1080p",
    "output_urls": {
      "playlist": "https://s3.example.com/video/1080p/playlist.m3u8",
      "stream": "https://s3.example.com/video/1080p/stream.mp4"
    },
    "bitrate_kbps": 4920,
    "file_size_bytes": 73800000
  }
}
```

**`job.complete`:**
```json
{
  "event": "job.complete",
  "job_id": "a1b2c3d4e5f6",
  "timestamp": "2026-03-03T12:00:20Z",
  "data": {
    "status": "complete",
    "output_url": "https://s3.amazonaws.com/bucket/output.mp4",
    "output": {
      "codec": "h264",
      "resolution": "1920x1080",
      "bitrate_kbps": 4850,
      "duration_seconds": 120.5,
      "file_size_bytes": 72940000
    },
    "processing_time_seconds": 20.4,
    "gpu": "NVIDIA GeForce RTX 5090"
  }
}
```

**`job.error`:**
```json
{
  "event": "job.error",
  "job_id": "a1b2c3d4e5f6",
  "timestamp": "2026-03-03T12:00:05Z",
  "data": {
    "status": "error",
    "error": "Input URL returned 403 Forbidden after 3 retries",
    "error_code": "INPUT_FETCH_FAILED"
  }
}
```

### 17.4 Error Codes

| Code | HTTP Status | Description |
|---|---|---|
| `INPUT_FETCH_FAILED` | 400 | Cannot download input URL (HTTP error, timeout, DNS failure) |
| `INPUT_UNSUPPORTED_FORMAT` | 400 | Input file format not recognized or cannot be decoded |
| `INPUT_CORRUPT` | 400 | Input file is corrupt or truncated |
| `OUTPUT_UPLOAD_FAILED` | 500 | Pre-signed PUT URL rejected, expired, or unreachable |
| `OUTPUT_URL_EXPIRY_TOO_SHORT` | 400 | Pre-signed URL expires before estimated job completion |
| `PRESET_NOT_FOUND` | 400 | Requested preset name does not exist |
| `PRESET_GPU_INCOMPATIBLE` | 400 | Preset requires GPU feature not available (e.g., AV1 on Ampere) |
| `CAPACITY_EXCEEDED` | 503 | All GPU encode sessions in use, job cannot be queued |
| `GPU_ENCODER_ERROR` | 500 | GPU encoder error (driver crash, NVENC error, OOM) |
| `GPU_DECODER_ERROR` | 500 | GPU decoder error (unsupported input codec for HW decode) |
| `ENCODING_FAILED` | 500 | ffmpeg exited with non-zero status (includes stderr excerpt) |
| `JOB_TIMEOUT` | 504 | Job exceeded maximum processing time |
| `JOB_NOT_FOUND` | 404 | Job ID not found (expired or never existed) |
| `WEBHOOK_DELIVERY_FAILED` | - | Webhook delivery failed after retries (non-blocking, logged only) |
| `INVALID_REQUEST` | 400 | Request body validation failed (missing fields, invalid URLs) |

### 17.5 Job Lifecycle Phases

```
queued -> probing -> downloading -> encoding -> uploading -> complete
                                                           -> error (any phase)
```

| Phase | Description |
|---|---|
| `queued` | Job accepted, waiting for GPU session |
| `probing` | Analyzing input file (duration, codec, resolution, fps) via ffprobe |
| `downloading` | Fetching input from `input_url` to local temp storage |
| `encoding` | GPU transcode in progress (progress %, FPS, speed reported) |
| `uploading` | Uploading output to `output_url` via pre-signed PUT |
| `packaging` | (ABR only) Generating HLS manifest after all renditions complete |
| `complete` | All outputs uploaded successfully |
| `error` | Job failed (see `error_code` for details) |

---

## 18. Phase Plan

### Phase 0: `transcode-core` + `transcode-runner` (MVP)

**Goal:** Single-input, single-output GPU transcoding with NVIDIA support.

**Deliverables:**

| Component | Path | Lines (est.) | Description |
|---|---|---|---|
| Shared package | `transcode-core/` | ~1,200 | GPU detection, ffmpeg command builder, preset loader, HTTP I/O, progress parser |
| Transcode runner | `transcode-runner/` | ~800 | HTTP server, job manager, async job execution, webhook sender |
| NVIDIA Dockerfile | `transcode-runner/Dockerfile.nvidia` | ~100 | Multi-stage: ffmpeg source build + Go build |
| Preset config | `transcode-runner/presets.yaml` | ~200 | All single-output presets (streaming, social, archive, utility) |
| Proxy updates | `proxy/main.go` | ~80 (delta) | Add `/v1/video/transcode`, `/status`, `/presets` routes |
| Capability register | Reuse `register/` | 0 (config only) | Register `transcode` capability |
| Docker Compose | `docker-compose.yml` | ~40 (delta) | Add `byoc_transcode_runner` + `register_transcode_capability` services |
| Tester | `tester/test-transcode.mjs` | ~250 | Submit job, poll, validate output |

**Presets included:** `streaming-*`, `streaming-*-hevc`, `streaming-*-av1`, `social-*`, `archive-*`, `proxy-edit`, `thumbnail`, `thumbnails-grid`, `audio-extract`, `remux-mp4`

**Scope boundaries:**
- NVIDIA only (Intel/AMD deferred to P1.5)
- Single output only (ABR deferred to P1)
- Polling + webhook notification
- No custom parameters (presets only)

### Phase 1: `abr-runner` + ABR Presets

**Goal:** Multi-rendition ABR ladder generation with fMP4 byte-range HLS output.

**Deliverables:**

| Component | Path | Lines (est.) | Description |
|---|---|---|---|
| ABR runner | `abr-runner/` | ~1,000 | HTTP server, multi-rendition job orchestration, HLS manifest generation |
| ABR Dockerfile | `abr-runner/Dockerfile.nvidia` | ~100 | Same ffmpeg build as transcode-runner |
| ABR presets | `abr-runner/presets.yaml` | ~150 | ABR ladder presets (standard, premium, mobile, hevc, av1) |
| Proxy updates | `proxy/main.go` | ~40 (delta) | Add `/v1/video/transcode/abr`, `/abr/status` routes |
| Capability register | Reuse `register/` | 0 (config only) | Register `transcode-abr` capability |
| Docker Compose | `docker-compose.yml` | ~30 (delta) | Add `byoc_abr_runner` + `register_abr_capability` services |
| Tester | `tester/test-abr.mjs` | ~300 | Submit ABR job, poll per-rendition progress, validate HLS output |

**Key implementation details:**
- Sequential rendition encoding (encode 1080p, then 720p, then 480p, then 360p) to minimize peak GPU memory
- Progressive upload: each rendition uploaded as it completes
- Manifest generated after all renditions complete, referencing the uploaded fMP4 files
- Per-rendition webhook callbacks (`job.rendition.complete`)

### Phase 1.5: Intel + AMD Docker Images

**Goal:** Multi-vendor GPU support.

**Deliverables:**

| Component | Path | Description |
|---|---|---|
| Intel Dockerfile | `transcode-runner/Dockerfile.intel` | ffmpeg with QSV/oneVPL |
| Intel Dockerfile (ABR) | `abr-runner/Dockerfile.intel` | Same |
| AMD Dockerfile | `transcode-runner/Dockerfile.amd` | ffmpeg with VAAPI/AMF |
| AMD Dockerfile (ABR) | `abr-runner/Dockerfile.amd` | Same |
| GPU detection updates | `transcode-core/gpu.go` | Intel/AMD probe paths (`vainfo`, `/dev/dri`) |
| CI matrix | `.github/workflows/` | Build + test across 3 GPU vendors |

**Validation:** Run all presets on Intel Arc A770 and AMD RX 7900 to confirm encoding quality parity.

### Phase 2: `live-transcode-runner` (Single Rendition)

**Goal:** Real-time single-rendition live transcoding via BYOC Stream Model.

**Deliverables:**

| Component | Path | Lines (est.) | Description |
|---|---|---|---|
| Live runner | `live-transcode-runner/` | ~1,200 | Trickle channel I/O, ffmpeg pipe management, stream lifecycle |
| Live Dockerfile | `live-transcode-runner/Dockerfile.nvidia` | ~100 | Same ffmpeg build |
| Proxy updates | `proxy/main.go` | ~60 (delta) | Stream model routes |
| Capability register | Reuse `register/` | 0 | Register `transcode-live` capability |
| Docker Compose | `docker-compose.yml` | ~30 (delta) | Add `byoc_live_transcode_runner` service |
| Tester | `tester/test-live-transcode.mjs` | ~400 | Start stream, send MPEG-TS, verify transcoded output |

**Key implementation details:**
- ffmpeg runs as a long-lived subprocess with pipe I/O (stdin from Trickle subscribe, stdout to Trickle publish)
- Goroutines manage Trickle channel -> ffmpeg -> Trickle channel data flow
- `stream/params` handler supports mid-stream bitrate/resolution changes (requires ffmpeg restart or filter reinit)
- Graceful shutdown on `stream/stop`
- Health monitoring: detect ffmpeg crashes and attempt restart

### Phase 2.5: Advanced Processing Features

**Goal:** HDR tone mapping, subtitle burn-in, watermarking, thumbnail extraction improvements.

Added to existing `transcode-runner` — no new services.

### Phase 3: Live ABR (Multi-Rendition)

**Goal:** Multi-rendition live ABR transcoding.

**Prerequisite:** Separate architecture discussion (see Decision 9). May require `go-livepeer` protocol changes.

### Phase 3.5: Quality Analysis

**Goal:** VMAF/SSIM/PSNR scoring for encoded outputs.

Added to existing `abr-runner` as optional post-encode analysis step.

### Phase Summary Timeline

```
P0: transcode-core + transcode-runner (NVIDIA)     ████████████
P1: abr-runner + ABR presets                            ████████████
P1.5: Intel + AMD Docker images                             ████████
P2: live-transcode-runner                                       ████████████
P2.5: Advanced processing (HDR, subs, watermark)                    ████████
P3: Live ABR (design discussion first)                                  ████████████
P3.5: Quality analysis (VMAF)                                               ████████
```

---

## 19. Transcoding Workload Catalog

### Complete Workload Matrix

| Workload | Runner | Priority | BYOC Model | GPU Required | Notes |
|----------|--------|----------|------------|:---:|---|
| Codec conversion (H.264/HEVC/AV1) | transcode-runner | P0 | Job | Yes | Core capability |
| Resolution scaling | transcode-runner | P0 | Job | Yes | Part of any transcode |
| Bitrate transcoding (CRF/CBR/VBR) | transcode-runner | P0 | Job | Yes | Core capability |
| Frame rate conversion | transcode-runner | P1 | Job | Yes | With motion compensation |
| Container remuxing | transcode-runner | P1 | Job | No | No re-encode, fast |
| Audio transcoding (AAC/Opus/AC3) | transcode-runner | P1 | Job | No | CPU-based, fast |
| Thumbnail extraction | transcode-runner | P1 | Job | Decode only | I-frame extraction |
| Sprite sheet generation | transcode-runner | P1 | Job | Decode only | Timeline preview |
| Subtitle burn-in | transcode-runner | P2 | Job | Yes | GPU encode with filter |
| Watermark/logo overlay | transcode-runner | P2 | Job | Yes | GPU encode with filter |
| HDR -> SDR tone mapping | transcode-runner | P2 | Job | Yes | Requires tonemap filter |
| Video trimming/cutting | transcode-runner | P2 | Job | Depends | Keyframe-accurate vs re-encode |
| Deinterlacing | transcode-runner | P2 | Job | Yes | yadif/bwdif filter |
| Noise reduction | transcode-runner | P3 | Job | Yes | GPU-accelerated denoise |
| ABR ladder (HLS) | abr-runner | P1 | Job | Yes | Multi-rendition + packaging |
| ABR ladder (DASH) | abr-runner | P1 | Job | Yes | Multi-rendition + packaging |
| Quality analysis (VMAF) | abr-runner | P2 | Job | No | CPU-based libvmaf |
| Live single-rendition transcode | live-transcode-runner | P2 | Stream | Yes | Trickle in/out |
| Live multi-rendition (ABR) | live-transcode-runner | P3 | Stream | Yes | Deferred — design TBD |

---

## 20. GPU Hardware Matrix

### NVIDIA — NVENC Capabilities by Generation

| Generation | Example Cards | H.264 | HEVC | AV1 | Max Sessions (Consumer) | Notes |
|------------|--------------|:---:|:---:|:---:|:---:|---|
| Kepler (2012) | GTX 680 | Limited | No | No | 2 | Legacy, not recommended |
| Maxwell (2014) | GTX 970 | Yes | No | No | 2 | |
| Pascal (2016) | GTX 1080, P4000 | Yes | Yes | No | 3 | |
| Turing (2018) | RTX 2080, T4 | Yes | Yes | No | 3 | B-frame support |
| Ampere (2020) | RTX 3090, A4000 | Yes | Yes | No | 5 | |
| Ada Lovelace (2022) | RTX 4090, L40 | Yes | Yes | **Yes** | 5 | AV1 encode support |
| Blackwell (2025) | RTX 5090, B200 | Yes | Yes | **Yes** | 5 | Enhanced AV1 |

**Important:** A100/H100 (data center compute GPUs) have **no NVENC encoder**. They are designed for AI inference, not video encoding. The transcode runner targets GeForce, Quadro, RTX Pro, and L-series GPUs.

**Consumer session limits** can be removed with the [nvidia-patch](https://github.com/keylase/nvidia-patch) or by using Quadro/Pro drivers.

### Intel — QuickSync Capabilities

| Generation | Example | H.264 | HEVC | AV1 | Notes |
|------------|---------|:---:|:---:|:---:|---|
| Skylake (6th, 2015) | i7-6700K | Yes | Partial | No | |
| Coffee Lake (8th-9th) | i7-9700K | Yes | Yes | No | |
| Ice Lake (10th, 2019) | i7-1065G7 | Yes | Yes | No | |
| Tiger/Alder Lake (12th) | i7-12700K | Yes | Yes | **Yes** | AV1 encode on iGPU |
| Raptor/Arrow Lake (13-15th) | i9-14900K | Yes | Yes | **Yes** | |
| Arc (discrete) | Arc A770, A380 | Yes | Yes | **Yes** | Best Intel encode quality |

### AMD — AMF/VAAPI Capabilities

| Generation | Example | H.264 | HEVC | AV1 | Notes |
|------------|---------|:---:|:---:|:---:|---|
| Polaris (2016) | RX 580 | Yes | Yes | No | VCE |
| Vega (2017) | Vega 56/64 | Yes | Yes | No | VCN 1.0 |
| Navi (RDNA, 2019) | RX 5700 | Yes | Yes | No | VCN 2.0 |
| RDNA 2 (2020) | RX 6800 | Yes | Yes | No | VCN 3.0 |
| RDNA 3 (2022) | RX 7900 | Yes | Yes | **Yes** | VCN 4.0 — AV1 encode |
| RDNA 4 (2025) | RX 9070 | Yes | Yes | **Yes** | VCN 5.0 |

---

## 21. Open Questions & Future Work

### Resolved (This Revision)

1. ~~Preset Definitions~~ — Defined in [Section 16](#16-preset-definitions). Streaming, social, archive, utility, and ABR presets with full YAML config.
2. ~~API Contract~~ — Defined in [Section 17](#17-api-contract). Request/response schemas, error codes, webhook contract, job lifecycle phases.
3. ~~Phase Plan~~ — Defined in [Section 18](#18-phase-plan). P0 -> P1 -> P1.5 -> P2 -> P2.5 -> P3 -> P3.5.
4. ~~Proxy Routing~~ — `/v1/video/transcode` namespace ([Section 14](#14-decision-12-proxy-routing--v1videotranscode)).
5. ~~ABR Output Strategy~~ — fMP4 byte-range HLS Phase 1, STS credentials Phase 1.5+ ([Section 12](#12-decision-10-abr-output-strategy--fmp4-byte-range-hls)).
6. ~~Notification Model~~ — Polling + webhook callbacks ([Section 13](#13-decision-11-notification-model--polling--webhook-callbacks)).

### Future Design Discussions Required

1. **Live ABR architecture** — Multiple streams vs multiplexed output vs Trickle extension vs runner-side HLS. Requires coordination with go-livepeer team.

2. **Multi-GPU support** — Multiple GPUs on one machine. GPU assignment strategy (round-robin, least-loaded, dedicated). `NVIDIA_VISIBLE_DEVICES` / `CUDA_VISIBLE_DEVICES` mapping.

3. **Quality analysis integration** — VMAF/SSIM/PSNR scoring. Per-rendition quality reports. Quality-aware encoding (target VMAF mode).

4. **Custom parameters (post-Phase 1)** — Full parameter schema for codec-specific options (NVENC presets, lookahead, B-frames, GOP structure, rate control modes). Will extend preset model with inline overrides.

5. **Monitoring & observability** — GPU utilization metrics, encode FPS, queue depth, job duration histograms. Prometheus/OpenTelemetry integration.

6. **Operator configuration** — How runner operators customize presets, pricing, capacity, and GPU assignment. Configuration file format and hot-reload.

7. **STS credential support for ABR** — Implement temporary scoped credentials (Decision 10, Option B) for traditional segmented HLS/DASH output in environments requiring per-segment CDN caching.

8. **Preset hot-reload** — Allow operators to update presets without runner restart. File watcher or API endpoint for reload.

---

## 22. Decision Summary Table

| # | Decision | Choice | Alternatives Considered |
|---|----------|--------|------------------------|
| 1 | Architecture | **Hybrid: 3 runners** (transcode + abr + live) | 5 runners (per-workload), 2 runners (per-protocol) |
| 2 | GPU vendor strategy | **Same code, per-vendor Docker images** | Separate runners per vendor |
| 3 | Codecs | **H.264, H.265/HEVC, AV1** (GPU-accelerated) | — |
| 4 | Input/Output | **HTTP URL pull + pre-signed PUT URLs** | Pre-signed both, gateway-mediated, inline |
| 5 | Configuration | **Presets first, custom params later** | Full custom only, presets only |
| 6 | ffmpeg build | **Build from source, multi-stage Docker** | Pre-built (jellyfin-ffmpeg), NVIDIA SDK base |
| 7 | Language | **Go** | Python |
| 8 | CPU fallback | **None — GPU required, refuse to start without** | Software fallback chain |
| 9 | Live ABR | **Deferred — separate design discussion** | — |
| 10 | ABR output | **fMP4 byte-range HLS** (P1), STS credentials (P1.5+) | Single archive, encode-only, pre-signed per-segment |
| 11 | Notifications | **Polling + webhook callbacks** (HMAC-signed) | Polling only, SSE, WebSocket |
| 12 | Proxy routing | **`/v1/video/transcode`** namespace | `/v1/transcode`, `/v1/video/encode` |

---

*Document revision: 2.0 — Added ABR output strategy, webhook callbacks, proxy routing, preset definitions, API contract, and phase plan*
