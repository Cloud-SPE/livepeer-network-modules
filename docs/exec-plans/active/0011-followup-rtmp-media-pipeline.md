---
plan: 0011-followup
title: rtmp-ingress-hls-egress media pipeline (RTMP listener + FFmpeg + HLS sink) — design
status: design-doc
phase: plan-only
opened: 2026-05-06
owner: harness
related:
  - "completed plan 0011 — session-open phase landed"
  - "active plan 0015 — interim-debit cadence (LiveCounter sibling interface)"
  - "active plan 0016 — chain-integrated payment-daemon (parallel)"
  - "future plan 0008-followup — gateway-side RTMP adapter (parallel)"
  - "livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md (wire spec)"
  - "prior reference impl: livepeer-network-suite/video-worker-node/internal/providers/{ingest/rtmp,ffmpeg,hls}"
audience: broker maintainers, video-pipeline operators
---

# Plan 0011-followup — rtmp-ingress-hls-egress media pipeline (design)

> **This is a paper-only design doc.** No Go code, no `go.mod` edits, no
> proto changes ship from this commit. Output: pinned decisions for the
> implementing agent. All eight open questions were resolved on
> 2026-05-06; see §14. Treat the prior impl in
> `livepeer-network-suite/video-worker-node/` as reference, not as code
> to copy wholesale.

## 1. Status and scope

Scope: **the production media pipeline that completes plan 0011's
session-open implementation.** When this plan lands, the URLs the broker
returns at `POST /v1/cap` (`rtmp_ingest_url` / `hls_playback_url`) are
live: customer encoders push RTMP, FFmpeg runs per-session, HLS
playlists are served, work-units accrue for plan 0015's ticker.

Out of scope:

- Session-open phase (closed in plan 0011).
- Chain integration / mainnet payment lifecycle (plan 0016).
- Interim-debit cadence machinery (plan 0015 — this plan provides the
  `LiveCounter` impl that plugs into 0015's ticker; the ticker itself
  is 0015's deliverable).
- Gateway-side RTMP adapter (plan 0008-followup, parallel — customer-
  facing auth layers — API keys, mTLS, optional AuthWebhookURL-style
  integration — live there).
- WebRTC egress / DASH egress / DRM / recording / VOD.

The wire shape is locked at
`livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md` and does
not change here, except for the `stream_key` field addition in §4.2.

## 2. What plan 0011 left unfinished

Plan 0011 ships the session-open POST → 202 response with publish /
playback URLs (`capability-broker/internal/modes/rtmpingresshlsegress/driver.go:50-100`).
The driver generates a `session_id`, derives URLs from
`Capability.Backend.URL`, returns. **Those URLs are dead** — no
listener on `:1935`, no HTTP server for `/hls/...`. The conformance
fixture `livepeer-network-protocol/conformance/fixtures/rtmp-ingress-hls-egress/happy-path.yaml:54-63`
asserts only the wire shape of the 202 body. This plan makes the URLs
live.

## 3. Reference architecture

```
customer RTMP encoder
  │ rtmp://gateway:1935/<session_id>/<stream_key>
  ▼
gateway RTMP adapter (plan 0008-followup, parallel)
  │ — terminates customer auth (API keys, mTLS), proxies plaintext RTMP
  ▼
broker RTMP listener (this plan, §4)
  │ — constant-time compares <session_id>/<stream_key> against
  │   the open-session record (defense-in-depth)
  │ — io.Pipe FLV bytes into FFmpeg subprocess
  ▼
FFmpeg subprocess per session (this plan, §5)
  │ — reads FLV from -i pipe:0; emits frame= / out_time_us= on stderr
  │ — encodes 5-rung H.264 ABR ladder (NVENC default; QSV/VAAPI/libx264 fallbacks)
  │ — writes LL-HLS playlist + fmp4 segments + parts to scratch dir
  ▼
HLS scratch on tmpfs (this plan, §6)
  │ /var/lib/livepeer/rtmp-hls/<session>/{master.m3u8, <rung>/...}
  ▼
broker HTTP file server (this plan, §6)
  │ https://broker/_hls/<session>/playlist.m3u8
  ▼
customer LL-HLS player (hls.js, Safari native)
```

Stream-key validation is **broker-side defense-in-depth**: the broker
constant-time compares the URL's `<session_id>/<stream_key>` against
its open-session record. Customer-side auth (API keys, mTLS,
AuthWebhookURL-style integration) lives **gateway-side** per plan
0008-followup. v0.1 deployments may expose the broker's `:1935`
directly to customers (smoke-grade); production wraps with the gateway
adapter. Work units are counted **broker-side** — RTMP packets crossing
the broker's listener and FFmpeg's progress output are the canonical
sources for plan 0015's ticker.

## 4. RTMP listener

Accepts RTMP `publish` connections, validates the session, demuxes
audio/video into a single FLV byte stream, pipes to FFmpeg.

**4.1. Port.** Default `:1935` (IANA-reserved). Configurable via
`--rtmp-listen-addr`.

**4.2. Stream-key validation.** Q7 LOCKED 2026-05-06. The session-open
response gains a new field, **`stream_key`** (renamed from the
pre-rewrite shape's `publish_auth` — naming aligns with go-livepeer +
mux + twitch + youtube vocabulary). It's a 32-byte URL-safe random
bearer token (more entropy than go-livepeer's 6-byte `StreamKeyBytes`
— `mediaserver.go:61` — for cheap defense-in-depth) surfaced at the
top of the 202 response body so the gateway can read it without URL
parsing.

URL shape is **path-based**, exactly mux/twitch/youtube's pattern
(and matches go-livepeer's `mediaserver.go:61` `StreamKeyBytes`
model):

```
rtmp://broker:1935/<session_id>/<stream_key>
```

Query-string variants (`?key=<...>`) are explicitly rejected.

The broker's listener parses RTMP's `PublishingName` via yutopp/go-rtmp's
`OnPublish` callback (suite reference at
`livepeer-network-suite/video-worker-node/internal/providers/ingest/rtmp/rtmp.go:162-192`),
splits into `session_id` / `stream_key`, looks up the open-session
record, **constant-time compares** the key. Mismatch → RTMP `_error`.

v0.1 model: broker accepts the stream-key directly with no external
webhook. go-livepeer's `AuthWebhookURL` pattern
(`mediaserver.go:70-290`) is the production-equivalent — we do a
local constant-time compare against the open-session record instead,
which is simpler and removes an external-webhook dependency.
Production deployments wanting an external webhook live in plan
0008-followup's gateway-side adapter (mTLS gateway↔broker,
customer-side API keys, optional AuthWebhookURL integration if
operators want it).

Spec change: `stream_key` field added to
`livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md`; old
`publish_auth` field name retired. Backward-compatible
`--rtmp-require-stream-key=true` (default) flag; when `false`, broker
accepts pushes without a stream key (dev / fixture mode only).

**4.3. Concurrency.** One goroutine per RTMP connection (yutopp/go-rtmp
owns the read loop) + one per FFmpeg subprocess + one per progress
parser. Per-broker cap via `--rtmp-max-concurrent-streams` (default
100). Above the cap, accept TCP, reject in `OnPublish`.

**4.4. RTMPS (TLS).** Q1 LOCKED 2026-05-06: **plaintext only at the
broker boundary.** Gateway terminates customer-facing TLS; broker's
`:1935` is a private interface reachable only from the gateway in
production. v0.1 deployments may expose `:1935` directly to customers
(smoke-grade only). RTMPS at the broker boundary is a future plan if
operators concretely ask.

**4.5. Library.** Q2 LOCKED 2026-05-06: **`github.com/yutopp/go-rtmp`**
— pure Go (matches the broker's no-cgo invariant), MIT-licensed,
suite-validated. Tradeoffs (rejected alternatives kept for the record):

| Library | Pro | Con |
|---|---|---|
| `yutopp/go-rtmp` ✓ | No cgo, suite-tested, MIT. | Sparse maintenance. |
| Hand-rolled handshake | Zero deps; can be RTMPS-native. | ~2-3 weeks to reinvent. |
| Upstream-extract suite's `internal/providers/ingest/rtmp` | Direct reuse. | Touches the suite. |

**4.6. Duplicate stream keys.** Q6 LOCKED 2026-05-06: default
**reject** the second push (safer when URLs leak). Operators opt-in
to `replace` (kick the first, accept the new — friendlier for
auto-reconnect encoders) via `--rtmp-on-duplicate-key=replace`.

**4.7. RTMP→FLV pump.** `OnAudio` / `OnVideo` callbacks deliver
per-tag payloads; the broker reassembles into FLV bytes via `io.Pipe`
into FFmpeg's stdin. Mirrors the suite's pattern at
`livepeer-network-suite/video-worker-node/internal/providers/ingest/rtmp/rtmp.go:203-222`.

## 5. FFmpeg pipeline

One `ffmpeg` subprocess per session; pattern lifted from
`livepeer-network-suite/video-worker-node/internal/providers/ffmpeg/live.go:139-227`.

**5.1. Subprocess management.** `exec.Command("ffmpeg", BuildArgs...)`;
`cmd.Stdin = flvReader`; stderr piped to a progress-parser goroutine;
`cmd.Wait()` wrapped in another goroutine. On `ctx.Done()`: SIGTERM,
wait `--ffmpeg-cancel-grace` (default 5s), SIGKILL.

**5.2. Container image / FFmpeg distribution.** Bake FFmpeg into the
broker's Docker image. Pin to **FFmpeg 7.x** (LL-HLS muxer flags need
4.4+; we go modern). LGPL build (no `--enable-gpl`). Sidecar
container per stream rejected — adds container-hop, complicates
resource accounting.

**5.3. Transcode profile.** Q3 LOCKED 2026-05-06: **GPU-default,
5-rung H.264 ABR ladder.** Passthrough is **CI-smoke-only**, not the
production default.

Four named profiles ship in `media/encoder/presets.go` and are
referenced from `host-config.yaml` `backend.profile`:

| Profile | Use | Encoder | Output |
|---|---|---|---|
| `passthrough` | CI smoke / dev | `-c:v copy -c:a copy` | Single-rung re-mux. **Not production.** |
| `h264-live-1080p-nvenc` | Production default | NVENC (NVIDIA Pascal+) | 5-rung ladder + AAC. |
| `h264-live-1080p-qsv` | First-class fallback | Intel QuickSync (Skylake+) | Same 5-rung ladder. |
| `h264-live-1080p-vaapi` | First-class fallback | AMD/Intel-iGPU VAAPI (RX 5700+ / VCN+) | Same 5-rung ladder. |
| `h264-live-1080p-libx264` | Software fallback | libx264 | Same 5-rung ladder; CPU. |

The 5-rung H.264 + AAC ladder follows the **Apple HLS Authoring Spec**
+ **Mux published encoder recommendations**:

| Rung | Resolution | H.264 profile | Bitrate (kbps) |
|---|---|---|---|
| 1 | 240p | baseline | 400 |
| 2 | 360p | baseline | 800 |
| 3 | 480p | main | 1400 |
| 4 | 720p | main | 2800 |
| 5 | 1080p | high | 5000 |

AAC stereo audio shared across rungs. 4s GOP. Source citations for
each rung's bitrate/profile/GOP: go-livepeer's
`core/playlistmanager.go:34` `VideoProfile` interface +
`core/playlistmanager_test.go:197` `P240p30fps16x9` constant + Apple
HLS Authoring Spec + Mux docs.

Profiles are **defined fresh** in
`capability-broker/internal/media/encoder/presets.go`. The suite's
`presets/h264-live.yaml:8-43` is a sanity-check **template** — do
**not** port it verbatim.

The passthrough fallback args (CI smoke / dev only):

```
ffmpeg -hide_banner -loglevel info \
  -f flv -i pipe:0 \
  -c:v copy -c:a copy \
  -progress pipe:2 \
  -f hls -hls_time 2 -hls_list_size 4 \
  -hls_segment_type fmp4 \
  -hls_flags delete_segments+append_list+omit_endlist+independent_segments \
  -hls_segment_filename /var/lib/livepeer/rtmp-hls/<session>/segment_%05d.m4s \
  /var/lib/livepeer/rtmp-hls/<session>/playlist.m3u8
```

**5.3.1. Encoder selection / auto-probe.** Q3 LOCKED 2026-05-06.

Default encoder priority (auto-probe at broker startup):
**NVENC → QSV → VAAPI → libx264.** NVENC is primary because Livepeer's
transcoder fleet skews toward NVIDIA Pascal-and-newer consumer cards
(GTX 1060/1070/1080 historically; Turing/Ampere/Ada modern). QSV
(Skylake+) and VAAPI (RX 5700+ / VCN-capable) are first-class
fallbacks. **libx264 is software fallback — explicit opt-in only,
never auto-selected when a GPU encoder is available.**

Flag surface:

- `--encoder=auto|nvenc|qsv|vaapi|libx264` (default `auto`). Probe
  walks `ffmpeg -hide_banner -encoders` + `-init_hw_device` at
  startup; the first matching codec wins.
- `--encoder-allow-cpu=false` (default false). When `--encoder=auto`
  finds no GPU encoder AND this flag is false, the broker **refuses
  to start** with a clear error: *"no GPU encoder detected; install
  NVIDIA driver + cuda-toolkit, OR set `--encoder-allow-cpu=true` to
  use libx264 (production deployments should use a GPU)."* Operators
  consciously running CPU-only flip the flag.

Reference for the auto-probe pattern: video-worker-node's `codecFlag`
selection at
`livepeer-network-suite/video-worker-node/internal/providers/ffmpeg/ffmpeg.go:112,120,129`.

**5.4. Progress parsing.** FFmpeg invoked with `-progress pipe:2`
emits `frame=N`, `out_time_us=N`, `progress=continue|end` lines on
stderr. Parser pattern: see suite
`livepeer-network-suite/video-worker-node/internal/providers/ffmpeg/ffmpeg.go:329-376`.
Broker's existing extractor parses the same shape post-hoc at
`capability-broker/internal/extractors/ffmpegprogress/extractor.go:84-109`.

For **live** progress (plan 0015's `LiveCounter`), the encoder wrapper
runs a goroutine that writes two `atomic.Uint64` fields: `frameCount`
(last `frame=N`) and `outTimeUs` (last `out_time_us=N`).

**5.5. Crash handling.** FFmpeg exits non-zero before RTMP push ends →
broker terminates the session, drops HLS scratch, emits
`Livepeer-Error: ffmpeg_subprocess_failed` on the control-WS if open.
**No restart in v0.1** — the gateway's retry policy is the right
place. Spec change: `ffmpeg_subprocess_failed` and
`rtmp_ingest_idle_timeout` are new `Livepeer-Error` codes; added to
`livepeer-network-protocol/headers/livepeer-headers.md` and the Go
constants at
`capability-broker/internal/livepeerheader/headers.go:35-43`.

**5.6. Resource isolation.** Q4 LOCKED 2026-05-06: **bare `exec.Command`
per FFmpeg subprocess; no broker-side cgroups, no
container-per-stream.** Matches video-worker-node's bare-exec model
documented at
`livepeer-network-suite/video-worker-node/docs/subprocess-vs-embed.md:22,34`.
SIGTERM-grace-SIGKILL is the only cancellation primitive (§5.5).
Container-level Docker / K8s limits enforce per-host fairness at the
operator deployment layer. `prlimit(2)` per-subprocess wrappers are
flagged as a future-plan item if real fairness issues surface in
production. The `backend.resources` cgroup config block is dropped
from §10.2.

## 6. HLS output and serving

**6.1. Segment storage.** Per-session scratch under `--hls-scratch-dir`
(default `/var/lib/livepeer/rtmp-hls`). Layout:
`<scratch>/<session_id>/{master.m3u8, <rung>/playlist.m3u8,
<rung>/segment_NNNNN.m4s, <rung>/init.mp4}`. **tmpfs.**

Sizing under the 5-rung 1080p ladder + 2s LL-HLS segments + 4-segment
rolling window: ≈80 MB/session for the 1080p rung alone; ≈150 MB total
across all rungs. 100 concurrent ≈ 15 GB tmpfs. (Markedly larger than
the prior passthrough estimate; size operator scratch accordingly.)

FFmpeg's `-hls_flags delete_segments` auto-prunes; broker's
session-teardown path deletes the whole dir.

**6.2. Playlist shape.** Q5 LOCKED 2026-05-06: **LL-HLS default,
legacy HLS v3 fallback.**

**Default LL-HLS** (`#EXT-X-VERSION:6`, fmp4 segments, 2s segment
duration, 333ms `#EXT-X-PART` duration, 4-segment rolling window —
~1-3s glass-to-glass). FFmpeg flags:
`-hls_segment_type fmp4 -hls_part_duration 0.33 -hls_flags
+iframe_only_partial`. Cite Apple's LL-HLS spec; player compatibility
covered by hls.js + Safari native. The broker pins FFmpeg 7.x (LL-HLS
muxer needs 4.4+).

**Legacy fallback** via `--hls-legacy=true`: flips to
`#EXT-X-VERSION:3`, mpegts segments, 6s segment duration, 5-segment
rolling window, ~12-24s glass-to-glass. For player-compat with rare
older players (older Android stacks); documented in §12.

The suite's HLS-v7 master manifest writer at
`livepeer-network-suite/video-worker-node/internal/providers/hls/hls.go:49`
is a non-LL precedent — v0.1 broker goes further than the suite by
shipping LL-HLS.

WebRTC egress and DASH stay out-of-scope for v0.1 (§15).

**6.3. HTTP server.** Serve from the broker's existing paid listener
under `/_hls/<session_id>/...`. The URL is already a per-session
unguessable path (12 random bytes hex —
`rtmpingresshlsegress/driver.go:110-116`); the spec at
`livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md:88-95`
treats the URL itself as the bearer secret.

LL-HLS adds these endpoints (note `.m4s` for fmp4 + part files):

- `/_hls/<session>/playlist.m3u8` — master / variant manifest.
- `/_hls/<session>/<rung>/segment_NNNNN.m4s` — fmp4 segments.
- `/_hls/<session>/<rung>/init.mp4` — fmp4 init segment.
- `/_hls/<session>/<rung>/part_NNNNN_KK.m4s` — LL-HLS partial segments.

The HTTP handler is a thin wrapper: parse `<session_id>`, look up the
session record (404 if missing), `http.ServeFile` from the scratch
dir. Payment middleware does **not** wrap this handler — playback is
"free" once session-open is paid for. Per-segment metering is not in
scope.

**6.4. Cleanup.** On any of §7's termination triggers: SIGTERM ffmpeg
→ wait grace → SIGKILL → wait `cmd.Wait()` → `os.RemoveAll(scratch)`.
RemoveAll failure is a soft fail (log + metric).

## 7. Lifetime management

Q8 LOCKED 2026-05-06: **four termination triggers**, evaluated at the
broker. The previously-proposed operator-kill admin endpoint is cut
from v0.1; plan 0018's roster UX is the long-term home if real ops
surfaces stuck-session cases.

1. **`expires_at` reached without an RTMP push.** The session-open
   response sets `expires_at` (today: now + 1 hour —
   `rtmpingresshlsegress/driver.go:89`). Broker tracks a no-push
   timer per session; on fire, tear down + full refund per spec
   (`rtmp-ingress-hls-egress.md:113-117`). Reconciles via
   `DebitBalance(0)` + `CloseSession`.

2. **Idle disconnect.** Once RTMP push starts, no packet for
   `--rtmp-idle-timeout` (default 10s; spec recommends 15s — we go
   tighter for the broker default) → terminate. Wire signal:
   `Livepeer-Error: rtmp_ingest_idle_timeout`.

3. **`SufficientBalance` returns false** (plan 0015's signal). The
   ticker cancels the request context; cancellation propagates to
   FFmpeg (SIGTERM via §5.5) and the RTMP listener. Trailer:
   `Livepeer-Error: insufficient_balance` (added by plan 0015's C2
   commit).

4. **Explicit `CloseSession` from gateway.** The spec defines
   `https://broker/v1/cap/{session_id}/end`
   (`rtmp-ingress-hls-egress.md:111-114`). This plan adds the
   handler.

## 8. LiveCounter + interim-debit integration (plan 0015 handshake)

The 5-rung ABR ladder does not change `LiveCounter` semantics — the
work-unit is still per-encoded-second or per-frame on the **source
RTMP push**, summed across rungs at most. The broker counts the
ingress-side, not the rendered ladder.

This plan implements `LiveCounter` (defined by plan 0015 §4.1):

```
type LiveCounter interface {
    CurrentUnits() uint64  // monotonic; safe for concurrent reads
}
```

The mode driver registers a `LiveCounter` on `modes.Params`
(`Params.LiveCounter` is added by plan 0015's C1 commit at
`capability-broker/internal/modes/types.go:33-41`); the middleware
ticker polls every cadence_seconds.

### 8.1. Work-unit choices

The operator picks one of three extractor shapes via
`work_unit.extractor.type` in `host-config.yaml`:

| `extractor.type` (+ unit) | `CurrentUnits()` returns | When |
|---|---|---|
| `ffmpeg-progress` (`out_time_seconds`) | `outTimeUs / 1_000_000` ceiled. | Time per encoded second. **Default.** |
| `ffmpeg-progress` (`frame_megapixel`) | `frame × w × h / 1_000_000`. | Resolution-aware per-frame. |
| `ffmpeg-progress` (`frame`) | Last `frame=N`. | Simple per-frame. |
| `seconds-elapsed` | `time.Since(open) / granularity`. | Wall-clock. |
| `bytes-counted` (response) | Cumulative HLS segment bytes. | Per-byte egress. |

All map into existing extractor packages
(`capability-broker/internal/extractors/{ffmpegprogress,secondselapsed,bytescounted}/extractor.go`).
Plan 0015 C1 wires the `LiveCounter` siblings for the trivial two
(`bytes-counted` atomic, `seconds-elapsed` `time.Since`); this plan
wires the `ffmpeg-progress` sibling on top of the parser goroutine's
atomic fields per §5.4.

### 8.2. Concurrency

`atomic.Uint64` for both fields; single load per `CurrentUnits()`
call. Width/height immutable from session-open. Pattern from
`livepeer-network-suite/video-worker-node/internal/providers/ffmpeg/live.go:131-134`
(suite uses `atomic.Int64` for the analogous `processed` field).

### 8.3. End-of-session reconciliation

For rtmp there is no buffered response body — `LiveCounter.CurrentUnits()`
IS the canonical count. Middleware reads once more at handler exit,
`delta_final = current - last_tick_total`, debits with `seq=N+1`, then
`CloseSession` (per plan 0015 §3.3).

## 9. Component layout

**Recommend a separate `media/...` package set, mode-agnostic** so
`session-control-plus-media` (plan 0012-followup) can reuse:

```
capability-broker/internal/
  modes/rtmpingresshlsegress/
    driver.go            — extends Serve with session-record write
    sessions.go          — open-session record store (sync.Map; lifetime = broker process)
  media/rtmp/            — NEW
    listener.go          — wraps yutopp/go-rtmp
    handler.go           — connHandler (per-conn state + key validation)
    flvpipe.go           — RTMP-tag → FLV-byte adapter
  media/encoder/         — NEW
    encoder.go           — Encoder interface + LiveCounter glue
    ffmpeg.go            — SystemEncoder (real subprocess)
    presets.go           — 4 named profiles (passthrough + h264-live-1080p-{nvenc,qsv,vaapi,libx264})
    probe.go             — runtime auto-probe (NVENC → QSV → VAAPI → libx264)
    progress.go          — stderr parser → atomic fields
    nvenc/builder.go     — NEW per-vendor args builder
    qsv/builder.go       — NEW
    vaapi/builder.go     — NEW
    libx264/builder.go   — NEW
  media/hls/             — NEW
    server.go            — http.Handler for /_hls/<sess>/...
    scratch.go           — per-session dir lifecycle
```

The session-record store (`sessions.go`) is an in-memory `sync.Map`
keyed by `session_id` → `{stream_key, expires_at, cancel func(),
liveCounter}`. **Not persisted across broker restarts** — restart
terminates all in-flight RTMP sessions, matching the daemon's BoltDB
behaviour for in-flight tickets.

The mode driver becomes thin: `Serve` extends to write the record
before returning 202; everything else is wired by the broker's
composition root.

## 10. Configuration

### 10.1. Flags (broker)

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--rtmp-listen-addr` | string | `:1935` | RTMP listener bind. |
| `--rtmp-max-concurrent-streams` | uint | `100` | Hard cap. |
| `--rtmp-idle-timeout` | duration | `10s` | Per-stream idle. |
| `--rtmp-on-duplicate-key` | enum | `reject` | `reject` \| `replace`. §4.6 / Q6. |
| `--rtmp-require-stream-key` | bool | `true` | Dev override. §4.2 / Q7. |
| `--encoder` | enum | `auto` | `auto` \| `nvenc` \| `qsv` \| `vaapi` \| `libx264`. §5.3.1 / Q3. |
| `--encoder-allow-cpu` | bool | `false` | Permit libx264 fallback when probe finds no GPU. §5.3.1 / Q3. |
| `--ffmpeg-binary` | string | `ffmpeg` | Path override. |
| `--ffmpeg-cancel-grace` | duration | `5s` | SIGTERM-to-SIGKILL window. |
| `--hls-legacy` | bool | `false` | Flips to mpegts HLS v3 (~12-24s glass-to-glass). §6.2 / Q5. |
| `--hls-part-duration` | duration | `333ms` | LL-HLS `#EXT-X-PART` duration. §6.2 / Q5. |
| `--hls-segment-duration` | duration | `2s` | `-hls_time` (LL-HLS default; legacy uses 6s). |
| `--hls-playlist-window` | uint | `4` | `-hls_list_size` (LL-HLS default; legacy uses 5). |
| `--hls-scratch-dir` | string | `/var/lib/livepeer/rtmp-hls` | Per-session scratch root. |

### 10.2. Per-capability YAML

`host-config.yaml` gains optional fields for capabilities whose
`interaction_mode` is `rtmp-ingress-hls-egress@v0`:

```yaml
- id: "video:transcode.live.rtmp:1080p-nvenc"
  offering_id: "h264-live-1080p-nvenc"
  interaction_mode: "rtmp-ingress-hls-egress@v0"
  work_unit:
    name: "out_time_seconds"
    extractor:
      type: "ffmpeg-progress"
      unit: "out_time_seconds"
  price:
    amount_wei: "1000000"
    per_units: 1
  backend:
    transport: "ffmpeg-subprocess"     # NEW transport type
    profile: "h264-live-1080p-nvenc"   # one of the 4 named profiles in §5.3
```

`backend.transport: ffmpeg-subprocess` signals the composition root
to wire a FFmpeg-backed pipeline rather than an HTTP forwarder.
`backend.profile` references one of the 4 named profiles in §5.3
(`passthrough` | `h264-live-1080p-nvenc` | `h264-live-1080p-qsv` |
`h264-live-1080p-vaapi` | `h264-live-1080p-libx264`). The existing
`backend.url` continues to hold the broker's external host (so the
URL-derivation in `rtmpingresshlsegress/driver.go:64-82` is
unchanged).

Per Q4, no `backend.resources` cgroup block — container-level Docker
/ K8s limits enforce per-host fairness in v0.1 (§5.6).

## 11. Conformance fixture

**11.1. `end-to-end.yaml`** at
`livepeer-network-protocol/conformance/fixtures/rtmp-ingress-hls-egress/end-to-end.yaml`.
The fixture explicitly sets `backend.profile: passthrough` to skip
the encoder — CI hosts have no GPU, and the goal here is to validate
the wire-shape + RTMP listener + HLS server end-to-end without
exercising the GPU encoder. A separate operator-driven smoke
(post-merge, hardware-required) validates the 5-rung NVENC profile
on real hardware (see §13 C5).

1. Runner sends `POST /v1/cap` (matches existing `happy-path.yaml`
   shape).
2. Reads `rtmp_ingest_url`, `hls_playback_url`, `stream_key`.
3. Publishes a 5s synthetic RTMP stream (`ffmpeg -re -f lavfi -i
   testsrc=duration=5:size=320x240:rate=30 -f flv
   rtmp://broker:1935/<session_id>/<stream_key>`).
4. Waits ≤8s, GETs `hls_playback_url`. Asserts: 200, body starts with
   `#EXTM3U`, contains ≥1 `segment_*.m4s` reference (LL-HLS default;
   legacy mode would assert `.ts`).
5. GETs first segment. Asserts: 200, fmp4 box header (`ftyp` /
   `moof`) at offset 0.
6. Closes RTMP. Asserts: daemon ledger received ≥1 `DebitBalance`
   with `work_units > 0`; session closed status `closed_clean`.

**11.2. Test infrastructure.** Bake real `ffmpeg` into the runner
image for the synthetic RTMP source (the suite's runner already has
FFmpeg in its CI image). Pure-Go RTMP publisher rejected — adds dep
when a one-line FFmpeg invocation suffices.

**11.3. Smoke time budget.** ~10s wall (5s publish + ~3s playlist
materialization + 2s checks); compose-up overhead dominates.

**11.4. Hardware-required GPU smoke.** Operator-driven, post-merge.
A separate runbook entry (§12) documents how to run the 5-rung
`h264-live-1080p-nvenc` profile against a real NVIDIA host and
verify the variant playlists materialize.

## 12. Operator runbook updates

**12.1. Cross-reference in `payment-daemon/docs/operator-runbook.md`.**
A bullet under §"Long-running session billing" (added by plan 0015)
noting that rtmp sessions emit work-units via the FFmpeg progress
extractor per this plan's §8.

**12.2. New `capability-broker/docs/operator-runbook.md`** (does not
exist today). Sections:

1. **RTMP port exposure.** Default `:1935`. Production: reachable
   from the gateway only; cloud security group rules. v0.1
   smoke-grade deployments may expose directly to customers
   (plaintext only — Q1). Don't expose plaintext RTMP across the
   public internet at scale; the gateway adapter terminates customer
   TLS in production.
2. **GPU encoder hardware.** Production deployments **should use
   NVIDIA NVENC**; libx264 is operator-opt-in for hardware-less
   environments (Q3). NVIDIA Pascal+ is the Livepeer transcoder norm
   (GTX 1060/1070/1080 historically; Turing/Ampere/Ada modern). QSV
   (Skylake+) and VAAPI (RX 5700+ / VCN-capable) are first-class but
   less common. Driver / runtime install:
   - **NVENC:** NVIDIA driver matching the broker image's CUDA
     toolkit; `nvidia-container-toolkit` for Docker / K8s.
   - **QSV:** `intel-media-driver` + `libmfx` runtime; iGPU device
     passed through (`/dev/dri/renderD128`).
   - **VAAPI:** `mesa-va-drivers` (or vendor-specific equivalent);
     `/dev/dri/renderD128` passed through.
   Auto-probe detection mirrors video-worker-node's `codecFlag`
   pattern at
   `livepeer-network-suite/video-worker-node/internal/providers/ffmpeg/ffmpeg.go:112,120,129`.
3. **FFmpeg licensing.** Broker ships LGPL FFmpeg by default (no
   `--enable-gpl`). Operators wanting GPL libs (x264 / x265 default
   encoders) supply their own via `--ffmpeg-binary`; relicensing
   implication is theirs.
4. **Resource sizing per concurrent stream.** 5-rung
   `h264-live-1080p-nvenc` ladder ≈ 0.5-1.5 cores + ~250 MB RAM +
   ~150 MB tmpfs scratch per stream + ~1 NVENC engine slot.
   `passthrough` (CI smoke) ≈ 0.1-0.3 cores + ~50 MB RAM + ~25 MB
   tmpfs. Container-level Docker / K8s limits cap per-stream
   resources (Q4).
5. **LL-HLS player compatibility.** hls.js + Safari native both
   support LL-HLS. Older Android players may need
   `--hls-legacy=true` (Q5).
6. **Common failure modes.** Stream-key auth fail → encoder gets
   RTMP `_error`; broker logs `rtmp.publish_rejected` with redacted
   key prefix (suite's `redactKey` pattern at
   `livepeer-network-suite/video-worker-node/internal/providers/ingest/rtmp/rtmp.go:194-201`).
   FFmpeg crash → `Livepeer-Error: ffmpeg_subprocess_failed`; inspect
   captured stderr (128KB ring buffer). Broker refuses to start with
   `--encoder=auto` on a hardware-less host (set
   `--encoder-allow-cpu=true`). Disk-full on segment write → tmpfs
   sized too small (15GB for 100 concurrent at the 5-rung ladder).
7. **Observability metrics.** `livepeer_rtmp_active_sessions`
   (gauge); `livepeer_rtmp_bytes_in_total{capability,offering}`,
   `livepeer_hls_segments_written_total{capability,offering,rung}`,
   `livepeer_ffmpeg_subprocess_failures_total{capability,reason}`,
   `livepeer_rtmp_idle_timeouts_total`,
   `livepeer_mode_hls_cleanup_failed_total` (counters).

## 13. Migration sequence

Estimated 8-10 commits. The 5-rung ABR ladder + LL-HLS + 4 encoder
profiles + auto-probe make the cadence longer than the original
passthrough plan. Each commit is independently reviewable.

1. **`feat(media/rtmp): RTMP listener scaffolding (C1)`** —
   `internal/media/rtmp/` package wraps yutopp/go-rtmp; flags
   `--rtmp-listen-addr`, `--rtmp-max-concurrent-streams`,
   `--rtmp-idle-timeout`, `--rtmp-on-duplicate-key`,
   `--rtmp-require-stream-key`. Session record store. Spec change:
   `stream_key` field added to the mode spec. **No FFmpeg yet.**
   Smoke: RTMP push lands, FLV bytes reach `io.Discard`.

2. **`feat(media/encoder): FFmpeg subprocess wrapper + LiveCounter
   (C2)`** — `internal/media/encoder/` package; bare `exec.Command`
   subprocess plumbing; `progress.go` parses stderr into atomic
   fields; `LiveCounter` impl on `ffmpeg-progress`. Flags
   `--ffmpeg-binary`, `--ffmpeg-cancel-grace`. New `Livepeer-Error`
   codes `ffmpeg_subprocess_failed` and `rtmp_ingest_idle_timeout`
   added to spec + Go constants. **No encoder profile yet.** Smoke:
   subprocess starts, exits cleanly under cancellation.

3. **`feat(media/encoder): probe + selection (C3)`** —
   `internal/media/encoder/probe.go`; flags `--encoder=auto|nvenc|qsv|vaapi|libx264`,
   `--encoder-allow-cpu`. Refuse-to-start when probe finds no GPU
   AND `--encoder-allow-cpu=false`. Smoke: probe correctly identifies
   the encoder available on test hosts.

4. **`feat(media/encoder): passthrough + libx264 profiles (C4)`** —
   `presets.go` defines `passthrough` (`-c:v copy -c:a copy`) and
   `h264-live-1080p-libx264` (5-rung CPU ladder); `libx264/builder.go`.
   Smoke: passthrough RTMP→HLS works without GPU; libx264 5-rung
   ladder works on CI runners.

5. **`feat(media/encoder): NVENC profile (C5)`** —
   `nvenc/builder.go` + `h264-live-1080p-nvenc` preset (5-rung NVENC).
   **Operator-driven smoke** on real GPU hardware (post-merge); CI
   exercises only the passthrough profile (§11).

6. **`feat(media/encoder): QSV + VAAPI profiles (C6)`** —
   `qsv/builder.go` + `vaapi/builder.go` + `h264-live-1080p-{qsv,vaapi}`
   presets. Operator-driven smoke. C5 + C6 may be split or merged
   depending on hardware availability for testing.

7. **`feat(media/hls): LL-HLS muxer + legacy fallback (C7)`** —
   `internal/media/hls/` package; HTTP handler at `/_hls/<sess>/...`
   on the existing paid listener. Flags `--hls-legacy`,
   `--hls-part-duration`, `--hls-segment-duration`,
   `--hls-playlist-window`, `--hls-scratch-dir`. Mode driver wires
   RTMP → encoder → HLS scratch end-to-end. End-to-end smoke: push
   RTMP, GET LL-HLS playlist, see `#EXT-X-VERSION:6`.

8. **`feat(modes/rtmpingresshlsegress): lifetime management (C8)`**
   — `expires_at` no-push timer, idle-timeout watchdog,
   `CloseSession` handler at `/v1/cap/{session_id}/end`, scratch
   cleanup. **No operator-kill admin endpoint** (Q8).

9. **`test(conformance): end-to-end fixture (C9)`** — fixture file +
   runner image gets `ffmpeg` baked in. `make test-compose` includes
   the new fixture; `backend.profile: passthrough` for CI.

10. **`docs: runbook + close 0011-followup (C10)`** — new
    `capability-broker/docs/operator-runbook.md` per §12.2;
    cross-reference in `payment-daemon/docs/operator-runbook.md` per
    §12.1; PLANS.md refreshed; plan moved to `completed/`.

## 14. Resolved decisions

All eight open questions were resolved on 2026-05-06. The implementing
agent works against these locks; rationale captured for future readers.

### Q1. RTMPS at the broker boundary

**DECIDED: plaintext only.** The broker's `:1935` is a private
interface reachable only from the gateway in production; v0.1
deployments may expose `:1935` directly to customers (smoke-grade
only). Gateway terminates customer-facing TLS in production. RTMPS
at the broker boundary is a future plan if operators concretely ask
(§4.4).

### Q2. RTMP library

**DECIDED: `github.com/yutopp/go-rtmp`.** Pure Go (no cgo),
MIT-licensed, suite-validated. Hand-rolled handshake (~2-3 weeks to
reinvent) and suite-extraction (touches the suite) both rejected
(§4.5).

### Q3. Encoder selection — GPU-default, 5-rung ABR

**DECIDED: GPU-default, 5-rung H.264 ABR ladder; passthrough is
CI-smoke-only, not the production default.** Auto-probe priority
**NVENC → QSV → VAAPI → libx264**; libx264 is opt-in only
(`--encoder-allow-cpu=true`) and never auto-selected when a GPU
encoder is available. Four named profiles: `passthrough`,
`h264-live-1080p-nvenc` (production default),
`h264-live-1080p-qsv`, `h264-live-1080p-vaapi`,
`h264-live-1080p-libx264`. NVIDIA Pascal+ is the Livepeer transcoder
norm. Source for ladder rungs: go-livepeer's `VideoProfile`
constants + Apple HLS Authoring Spec + Mux published encoder
recommendations. The suite's `presets/h264-live.yaml:8-43` is a
sanity-check template — not ported verbatim. Reference for the
auto-probe pattern: video-worker-node's `codecFlag` selection at
`internal/providers/ffmpeg/ffmpeg.go:112,120,129` (§5.3, §5.3.1).

### Q4. Resource isolation

**DECIDED: bare `exec.Command` per FFmpeg subprocess; no broker-side
cgroups, no container-per-stream.** Matches video-worker-node's
bare-exec model documented at
`livepeer-network-suite/video-worker-node/docs/subprocess-vs-embed.md:22,34`.
Container-level Docker / K8s limits enforce per-host fairness at the
operator deployment layer. `prlimit(2)` per-subprocess wrappers are
flagged as a future-plan item if real fairness issues surface in
production. The previously-proposed `backend.resources` cgroup
config block is dropped (§5.6, §10.2).

### Q5. HLS variant — LL-HLS default

**DECIDED: LL-HLS default, legacy HLS v3 fallback.** LL-HLS:
`#EXT-X-VERSION:6`, fmp4 segments, 2s segment duration, 333ms
`#EXT-X-PART` duration, 4-segment rolling window (~1-3s
glass-to-glass). FFmpeg 7.x. Legacy fallback via `--hls-legacy=true`:
mpegts + HLS v3 + 6s segments + 5-segment rolling (~12-24s
glass-to-glass) for older Android players. hls.js + Safari native
both support LL-HLS (§6.2).

### Q6. Stream-key collision policy

**DECIDED: `reject` default; `replace` opt-in.** Reject is safer
when URLs leak. Operators with auto-reconnect encoders flip to
`replace` via `--rtmp-on-duplicate-key=replace` (§4.6).

### Q7. Stream-key naming + URL shape

**DECIDED: rename `publish_auth` → `stream_key`; URL is path-based,
not query-string.** New name aligns with go-livepeer + mux + twitch
+ youtube vocabulary. URL shape:
`rtmp://broker:1935/<session_id>/<stream_key>` — exactly mux /
twitch / youtube's pattern, and matches go-livepeer's
`mediaserver.go:61` `StreamKeyBytes` model. Length is 32 bytes
URL-safe random (more entropy than go-livepeer's 6-byte; cheap).
Broker parses RTMP `PublishingName`, splits, constant-time compares
against the open-session record. v0.1 model: broker accepts the
stream-key directly with no external webhook; go-livepeer's
`AuthWebhookURL` pattern (`mediaserver.go:70-290`) is the
production-equivalent — local constant-time compare is simpler.
External webhook integration lives in plan 0008-followup's
gateway-side adapter. Spec change: `stream_key` field added to
`livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md`; old
`publish_auth` field name retired. Backward-compatible
`--rtmp-require-stream-key=true` (default) flag (§4.2).

### Q8. Operator-kill admin endpoint

**DECIDED: drop from v0.1 entirely.** Rely on the existing four
termination triggers (§7): `expires_at` (1h hard cap),
`--rtmp-idle-timeout` (10s no-packet), `SufficientBalance` ticker
(plan 0015), customer `CloseSession` HTTP. Plan 0018's roster UX is
the long-term home for operator session controls if real ops
surfaces stuck-session cases. No operator-kill admin endpoint or
flag ships in v0.1.

## 15. Out of scope (deferred)

- **Gateway-side RTMP adapter** — plan 0008-followup, parallel.
  Customer-facing auth (API keys, mTLS, optional AuthWebhookURL-style
  integration) lives there.
- **WebRTC egress** — separate plan; HLS-only for v0.1.
- **DASH egress** — separate plan; HLS-only for v0.1.
- **Operator-kill admin endpoint** — Q8 lock; defer to plan 0018's
  roster UX.
- **`prlimit(2)` per-subprocess fairness** — Q4 lock; rely on
  container-level limits in v0.1; revisit if real fairness issues
  surface.
- **RTMPS at the broker boundary** — Q1 lock; followup if operators
  ask. Plaintext between gateway and broker; gateway terminates
  customer TLS in production.
- **External `AuthWebhookURL` integration** — Q7 lock; broker uses
  constant-time compare against open-session record locally.
  External webhook is plan 0008-followup gateway-adapter territory
  or a future enhancement.
- **Verifiable-receipt extractor** — chain-anchored proof-of-work;
  future plan post-0016.
- **Per-stream chain-anchored billing** — lands when 0016 closes.
- **DRM / token-gated playback** — operator concern, not broker
  architecture.
- **Recording / VOD sink** — live-only for v0.1.
- **Per-session FFmpeg version selection** — single
  `--ffmpeg-binary` per broker.

---

## Appendix A — file paths cited

This monorepo:

- `livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md` (wire
  spec; lines 88-95 URL-as-bearer; 111-114 session-end endpoint;
  113-117 expires_at refund; 165-168 stall recommendation).
- `livepeer-network-protocol/conformance/fixtures/rtmp-ingress-hls-egress/happy-path.yaml:54-63`
  (existing v0.1 assertions).
- `livepeer-network-protocol/headers/livepeer-headers.md` (extended
  with `ffmpeg_subprocess_failed`, `rtmp_ingest_idle_timeout`).
- `capability-broker/internal/modes/rtmpingresshlsegress/driver.go:50-100`
  (existing Serve; this plan extends), `:64-82` (URL derivation),
  `:89` (`expires_at`), `:110-116` (session_id generation).
- `capability-broker/internal/modes/types.go:33-41` (`Params`; plan
  0015 adds `LiveCounter`).
- `capability-broker/internal/extractors/ffmpegprogress/extractor.go:84-109`
  (existing parser; this plan reuses for live progress).
- `capability-broker/internal/extractors/bytescounted/extractor.go:19-58`
  (sibling — plan 0015's atomic counter).
- `capability-broker/internal/livepeerheader/headers.go:35-43` (error
  code list; extended).
- `capability-broker/examples/host-config.example.yaml` (config
  schema; this plan extends with `backend.transport: ffmpeg-subprocess`
  + `backend.profile`).

Prior reference impl
(`livepeer-cloud-spe/livepeer-network-suite/video-worker-node/`):

- `internal/providers/ingest/rtmp/rtmp.go:11-258` (RTMP listener
  pattern), `:162-192` (`OnPublish` flow + stream-key extraction),
  `:194-201` (`redactKey`), `:203-222` (RTMP→pipe pump).
- `internal/providers/ffmpeg/ffmpeg.go:142-229` (single-shot
  `SystemRunner` cmd/cancellation reference), `:329-376`
  (`ParseProgressStream`).
- `internal/providers/ffmpeg/ffmpeg.go:112,120,129` (`codecFlag`
  selection — auto-probe pattern reference per §5.3.1).
- `internal/providers/ffmpeg/live.go:60-111` (`BuildLiveArgs` ABR
  shape — sanity-check template, not a verbatim port),
  `:131-134` (`atomic.Int64` for `processed` — `LiveCounter`
  substrate), `:139-227` (`LiveSystemEncoder`),
  `:235-272` (`parseLiveProgress` monotonic-CAS).
- `internal/providers/hls/hls.go:13-58` (HLS-v7 master manifest
  builder — non-LL precedent; v0.1 broker goes further with LL-HLS).
- `presets/h264-live.yaml:8-43` (sanity-check template for the
  5-rung ladder — not ported verbatim).
- `docs/subprocess-vs-embed.md:22,34` (bare-exec model rationale —
  no cgroups in v0.1 per §5.6).
- `internal/service/liverunner/encoder.go:14-39` (`Encoder`
  interface + `EncoderInput`; mirrors here).
- `internal/service/liverunner/ffmpeg_adapter.go:14-69` (factory
  pattern bridging runner to encoder).
- `docs/design-docs/live-rtmp-protocol.md` (rationale that informs
  this plan's auth model).

Cross-plan references in this monorepo:

- `docs/exec-plans/completed/0011-rtmp-ingress-hls-egress-driver.md`
  (parent).
- `docs/exec-plans/active/0015-interim-debit-cadence-design.md`
  (`LiveCounter` contract; §3.3 final-flush ordering; §4.1
  interface; §4.4 concurrency).
- `docs/exec-plans/active/0016-chain-integrated-payment-design.md`
  (parallel; chain integration lands behind it).
- `docs/exec-plans/active/0018-orch-coordinator-design.md` (roster
  UX; long-term home for operator session controls per Q8).
