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
> proto changes ship from this commit. Output: pinned decisions + open
> questions the user must answer before implementation begins. Treat the
> prior impl in `livepeer-network-suite/video-worker-node/` as reference,
> not as code to copy wholesale.

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
- Gateway-side RTMP adapter (plan 0008-followup, parallel — auth
  enforcement on the customer-facing side lives there).
- ABR transcoding (passthrough only for v0.1).
- LL-HLS / WebRTC egress / DRM / recording / VOD.

The wire shape is locked at
`livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md` and does
not change here, except for the small `publish_auth` addition in §4.2.

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
  │ rtmp://gateway:1935/<session>?key=<publish_auth>
  ▼
gateway RTMP adapter (plan 0008-followup, parallel)
  │ — strips publish_auth, validates, proxies plaintext RTMP
  ▼
broker RTMP listener (this plan, §4)
  │ — re-validates session_id + publish_auth
  │ — io.Pipe FLV bytes into FFmpeg subprocess
  ▼
FFmpeg subprocess per session (this plan, §5)
  │ — reads FLV from -i pipe:0; emits frame= / out_time_us= on stderr
  │ — writes HLS segments + playlist to scratch dir
  ▼
HLS scratch on tmpfs (this plan, §6)
  │ /var/lib/livepeer/rtmp-hls/<session>/playlist.m3u8
  ▼
broker HTTP file server (this plan, §6)
  │ https://broker/hls/<session>/playlist.m3u8
  ▼
customer HLS player
```

Auth enforcement lives **gateway-side** (plan 0008-followup); the
broker re-validates as defense-in-depth but treats the gateway as
trusted-on-its-side-of-the-wire. Work units are counted **broker-side**
— RTMP packets crossing the broker's listener and FFmpeg's progress
output are the canonical sources for plan 0015's ticker.

## 4. RTMP listener

Accepts RTMP `publish` connections, validates the session, demuxes
audio/video into a single FLV byte stream, pipes to FFmpeg.

**4.1. Port.** Default `:1935` (IANA-reserved). Configurable via
`--rtmp-listen-addr`.

**4.2. Stream-key validation.** This plan adds **one optional new
field** to the session-open response: `publish_auth`, a 32-byte
URL-safe random bearer token. It's embedded in `rtmp_ingest_url` as a
query string (`?key=<publish_auth>`) and surfaced at the top level so
the gateway can read it without URL parsing. The broker's listener
parses RTMP's `PublishingName` (yutopp/go-rtmp's `OnPublish` callback
— see suite at
`livepeer-network-suite/video-worker-node/internal/providers/ingest/rtmp/rtmp.go:162-192`),
splits into `session_id` + token, looks up the open-session record,
constant-time compares. Mismatch → RTMP `_error`.

Spec change: `publish_auth` field added to
`livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md`.
Backward-compatible: when `--rtmp-require-publish-auth=false`, broker
accepts pushes without a token (dev / fixture mode). Open question 1.

**4.3. Concurrency.** One goroutine per RTMP connection (yutopp/go-rtmp
owns the read loop) + one per FFmpeg subprocess + one per progress
parser. Per-broker cap via `--rtmp-max-concurrent-streams` (default
100). Above the cap, accept TCP, reject in `OnPublish`.

**4.4. RTMPS (TLS).** **Recommend defer for v0.1.** Gateway terminates
TLS for the customer-facing path; broker's `:1935` is a private
interface reachable only from the gateway. If we want broker-side
RTMPS later, add `--rtmps-listen-addr` + cert plumbing as a followup.
Open question 1.

**4.5. Library.** **Recommend `github.com/yutopp/go-rtmp`** — pure-Go
(matches the broker's no-cgo invariant), suite-validated. Tradeoffs:

| Library | Pro | Con |
|---|---|---|
| `yutopp/go-rtmp` | No cgo, suite-tested, MIT. | Sparse maintenance. |
| Hand-rolled handshake | Zero deps; can be RTMPS-native. | ~2-3 weeks to reinvent. |
| Upstream-extract suite's `internal/providers/ingest/rtmp` | Direct reuse. | Touches the suite. |

**4.6. Duplicate stream keys.** Default **reject** the second push
(safer when URLs leak). Operators can opt-in to `replace` (kick the
first, accept the new — friendlier for auto-reconnect encoders) via
`--rtmp-on-duplicate-key=replace`. Open question 6.

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

**5.2. Container image / FFmpeg distribution.** **Recommend baking
FFmpeg into the broker's Docker image.** Pin to **latest stable FFmpeg
release** (currently 7.x). LGPL build (no `--enable-gpl`). Sidecar
container per stream rejected — adds container-hop, complicates
resource accounting.

**5.3. Transcode profile.** **Recommend passthrough for v0.1** (no
ABR, no codec change):

```
ffmpeg -hide_banner -loglevel info \
  -f flv -i pipe:0 \
  -c:v copy -c:a copy \
  -progress pipe:2 \
  -f hls \
  -hls_time 6 -hls_list_size 5 \
  -hls_flags delete_segments+append_list+omit_endlist+independent_segments \
  -hls_segment_type mpegts \
  -hls_segment_filename /var/lib/livepeer/rtmp-hls/<session>/segment_%05d.ts \
  /var/lib/livepeer/rtmp-hls/<session>/playlist.m3u8
```

The suite's full ABR ladder shape (`BuildLiveArgs` at
`livepeer-network-suite/video-worker-node/internal/providers/ffmpeg/live.go:60-111`)
is the v0.2 target. Passthrough exercises the entire pipeline without
GPU dependencies. Open question 3.

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

**5.6. Resource isolation.** **Recommend cgroups for v0.1**, not
container-per-stream. Each FFmpeg subprocess runs in a transient
cgroup with CPU+memory caps from the capability config (new
`backend.resources` block — see §10.2). Container-per-stream adds
~500ms cold start + ~50MB RAM each — overkill for v0.1. Open
question 4.

## 6. HLS output and serving

**6.1. Segment storage.** Per-session scratch under `--hls-scratch-dir`
(default `/var/lib/livepeer/rtmp-hls`). Layout:
`<scratch>/<session_id>/{playlist.m3u8, segment_NNNNN.ts}`.
**Recommend tmpfs.** Sizing: passthrough at 6 Mbps with 5×6s window
≈ 22 MB/session; 100 concurrent ≈ 2.2 GB tmpfs. FFmpeg's
`-hls_flags delete_segments` auto-prunes; broker's session-teardown
path deletes the whole dir.

**6.2. Playlist shape.** **HLS v3** (`#EXT-X-VERSION:3`, mpegts
segments — broad compatibility), 6s segments, 5-segment rolling
window, `#EXT-X-TARGETDURATION:6`. Glass-to-glass latency ~6-12s
(typical for HLS v3). For ABR (deferred) the suite uses fMP4
byte-range HLS at v0.2 — see
`livepeer-network-suite/video-worker-node/internal/providers/hls/hls.go:48-58`.
Open question 5.

**6.3. HTTP server.** **Recommend serving from the broker's existing
paid listener** at `/hls/<session_id>/...`. The URL is already a
per-session unguessable path (12 random bytes hex —
`rtmpingresshlsegress/driver.go:110-116`); the spec at
`livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md:88-95`
treats the URL itself as the bearer secret. The HTTP handler is a
thin wrapper: parse `<session_id>`, look up the session record (404
if missing), `http.ServeFile` from the scratch dir. Payment
middleware does **not** wrap this handler — playback is "free" once
session-open is paid for. Per-segment metering is not in scope.

**6.4. Cleanup.** On any of §7's termination triggers: SIGTERM ffmpeg
→ wait grace → SIGKILL → wait `cmd.Wait()` → `os.RemoveAll(scratch)`.
RemoveAll failure is a soft fail (log + metric).

## 7. Lifetime management

Five termination triggers, evaluated at the broker:

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

5. **Operator kill (admin).** `POST /admin/sessions/{session_id}/kill`
   gated behind `--admin-listen-addr` (default disabled). Plan 0018's
   roster UX is the long-term home; flag-gated raw HTTP is the v0.1
   interim. Open question 8.

## 8. LiveCounter + interim-debit integration (plan 0015 handshake)

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
    args.go              — BuildArgs (passthrough v0.1)
    progress.go          — stderr parser → atomic fields
  media/hls/             — NEW
    server.go            — http.Handler for /hls/<sess>/...
    scratch.go           — per-session dir lifecycle
```

The session-record store (`sessions.go`) is an in-memory `sync.Map`
keyed by `session_id` → `{publish_auth, expires_at, cancel func(),
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
| `--rtmp-require-publish-auth` | bool | `true` | Dev override. §4.2. |
| `--ffmpeg-binary` | string | `ffmpeg` | Path override. |
| `--ffmpeg-cancel-grace` | duration | `5s` | SIGTERM-to-SIGKILL window. |
| `--hls-segment-duration` | duration | `6s` | `-hls_time`. |
| `--hls-playlist-window` | uint | `5` | `-hls_list_size`. |
| `--hls-scratch-dir` | string | `/var/lib/livepeer/rtmp-hls` | Per-session scratch root. |
| `--admin-listen-addr` | string | `""` (disabled) | Operator-kill endpoint. §7 #5. |

### 10.2. Per-capability YAML

`host-config.yaml` gains optional fields for capabilities whose
`interaction_mode` is `rtmp-ingress-hls-egress@v0`:

```yaml
- id: "video:transcode.live.rtmp:passthrough"
  offering_id: "passthrough-1080p"
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
    transport: "ffmpeg-subprocess"   # NEW transport type
    profile: "passthrough"            # passthrough | (v0.2) ladder names
    resources:                         # NEW optional cgroup block
      cpu_quota: "1.0"
      mem_max: "1Gi"
```

`backend.transport: ffmpeg-subprocess` signals the composition root to
wire a FFmpeg-backed pipeline rather than an HTTP forwarder. The
existing `backend.url` continues to hold the broker's external host
(so the URL-derivation in `rtmpingresshlsegress/driver.go:64-82`
unchanged).

## 11. Conformance fixture

**11.1. `end-to-end.yaml`** at
`livepeer-network-protocol/conformance/fixtures/rtmp-ingress-hls-egress/end-to-end.yaml`:

1. Runner sends `POST /v1/cap` (matches existing `happy-path.yaml`
   shape).
2. Reads `rtmp_ingest_url`, `hls_playback_url`, `publish_auth`.
3. Publishes a 5s synthetic RTMP stream (`ffmpeg -re -f lavfi -i
   testsrc=duration=5:size=320x240:rate=30 -f flv rtmp://...`) with
   `publish_auth` in PublishingName.
4. Waits ≤8s, GETs `hls_playback_url`. Asserts: 200, body starts with
   `#EXTM3U`, contains ≥1 `segment_*.ts` reference.
5. GETs first segment. Asserts: 200, MPEG-TS sync byte (`0x47` at
   offset 0 + 188).
6. Closes RTMP. Asserts: daemon ledger received ≥1 `DebitBalance`
   with `work_units > 0`; session closed status `closed_clean`.

**11.2. Test infrastructure.** **Recommend baking real `ffmpeg` into
the runner image** for the synthetic RTMP source (the suite's runner
already has FFmpeg in its CI image). Pure-Go RTMP publisher rejected —
adds dep when a one-line FFmpeg invocation suffices.

**11.3. Smoke time budget.** ~10s wall (5s publish + ~3s playlist
materialization + 2s checks); compose-up overhead dominates.

## 12. Operator runbook updates

**12.1. Cross-reference in `payment-daemon/docs/operator-runbook.md`.**
A bullet under §"Long-running session billing" (added by plan 0015)
noting that rtmp sessions emit work-units via the FFmpeg progress
extractor per this plan's §8.

**12.2. New `capability-broker/docs/operator-runbook.md`** (does not
exist today). Sections:

1. **RTMP port exposure.** Default `:1935` reachable from the gateway
   only; cloud security group rules. Don't expose directly to public
   internet — gateway terminates TLS and enforces auth.
2. **FFmpeg licensing.** Broker ships LGPL FFmpeg by default (no
   `--enable-gpl`). Operators wanting GPL libs (x264 / x265 default
   encoders) supply their own via `--ffmpeg-binary`; relicensing
   implication is theirs.
3. **Resource sizing per concurrent stream.** Passthrough ≈ 0.1-0.3
   cores + ~50 MB RAM + ~25 MB tmpfs scratch per stream. ABR (future)
   multiplies by ladder cardinality + adds GPU.
4. **Common failure modes.** Stream-key auth fail → encoder gets RTMP
   `_error`; broker logs `rtmp.publish_rejected` with redacted key
   prefix (suite's `redactKey` pattern at
   `livepeer-network-suite/video-worker-node/internal/providers/ingest/rtmp/rtmp.go:194-201`).
   FFmpeg crash → `Livepeer-Error: ffmpeg_subprocess_failed`; inspect
   captured stderr (128KB ring buffer). Disk-full on segment write →
   tmpfs sized too small.
5. **Observability metrics.** `livepeer_rtmp_active_sessions` (gauge);
   `livepeer_rtmp_bytes_in_total{capability,offering}`,
   `livepeer_hls_segments_written_total{capability,offering}`,
   `livepeer_ffmpeg_subprocess_failures_total{capability,reason}`,
   `livepeer_rtmp_idle_timeouts_total`,
   `livepeer_mode_hls_cleanup_failed_total` (counters).

## 13. Migration sequence

Estimated 6 commits, each independently reviewable:

1. **`feat(media/rtmp): RTMP listener scaffolding (no FFmpeg yet)
   (C1)`** — `internal/media/rtmp/` package wraps yutopp/go-rtmp;
   flags `--rtmp-listen-addr`, `--rtmp-max-concurrent-streams`,
   `--rtmp-idle-timeout`, `--rtmp-on-duplicate-key`,
   `--rtmp-require-publish-auth`. Session record store. Spec change:
   `publish_auth` field added to the mode spec. Smoke: RTMP push lands,
   FLV bytes reach `io.Discard`.

2. **`feat(media/encoder): FFmpeg subprocess wrapper + LiveCounter
   (C2)`** — `internal/media/encoder/` package; `BuildArgs` (passthrough);
   `progress.go` parses stderr into atomic fields; `LiveCounter` impl
   on `ffmpeg-progress`. Flags `--ffmpeg-binary`,
   `--ffmpeg-cancel-grace`. New `Livepeer-Error` codes
   `ffmpeg_subprocess_failed` and `rtmp_ingest_idle_timeout` added to
   spec + Go constants.

3. **`feat(media/hls): HLS scratch + HTTP server (C3)`** — `internal/media/hls/`
   package; HTTP handler at `/hls/<sess>/...` on the existing paid
   listener. Flags `--hls-segment-duration`, `--hls-playlist-window`,
   `--hls-scratch-dir`. Mode driver wires RTMP → encoder → HLS scratch
   end-to-end. First end-to-end smoke: push RTMP, GET HLS, see
   `#EXTM3U`.

4. **`feat(modes/rtmpingresshlsegress): lifetime management (C4)`** —
   `expires_at` no-push timer, idle-timeout watchdog, `CloseSession`
   handler at `/v1/cap/{session_id}/end`, optional admin-kill behind
   `--admin-listen-addr`, scratch cleanup.

5. **`test(conformance): end-to-end fixture (C5)`** — fixture file +
   runner image gets `ffmpeg` baked in. `make test-compose` includes
   the new fixture.

6. **`docs: runbook + close 0011-followup (C6)`** — new
   `capability-broker/docs/operator-runbook.md` per §12.2;
   cross-reference in `payment-daemon/docs/operator-runbook.md` per
   §12.1; PLANS.md refreshed; plan moved to `completed/`.

C1+C2+C3 can collapse into one commit if each diff is small (~150-300
lines). The split is for review-tractability.

## 14. Risks and open questions

1. **RTMPS at the broker boundary.** Recommend plaintext-only between
   gateway and broker (gateway terminates TLS for the customer). Adds
   a deployment constraint: broker's `:1935` reachable only from
   gateway. Ship plaintext-only v0.1, add RTMPS in a followup if
   operators ask?

2. **RTMP library choice.** Recommend `yutopp/go-rtmp` (the suite's
   choice). Add as a direct broker dep, hand-roll, or block on
   suite-extraction of its `internal/providers/ingest/rtmp` to a
   public module?

3. **Transcode profile in v0.1.** Recommend passthrough only —
   exercises the entire pipeline without GPU dependencies. Confirm:
   passthrough only, or do we need at least one single-output
   transcode (e.g. 720p reduction) to exercise FFmpeg-bound
   work-units?

4. **Resource isolation.** Recommend cgroups for v0.1, not
   container-per-stream. Cgroups ≈ zero overhead vs ~500ms cold start
   + ~50MB RAM each per-stream container. Confirm: cgroups now,
   container isolation as a future plan?

5. **HLS variant.** Recommend HLS v3 mpegts only (broad
   compatibility). The suite's v2 target is fMP4 byte-range HLS;
   DASH is its own world. Confirm.

6. **Stream-key collision policy.** Recommend `reject` second push
   (safer if URL leaks). Operators opt-in to `replace` via
   `--rtmp-on-duplicate-key=replace` for auto-reconnect-friendly
   behaviour. Confirm default.

7. **Sequencing with plan 0008-followup (gateway-side RTMP adapter).**
   This plan can land independently — broker's RTMP listener is
   directly reachable via the URL it returns, with the gateway
   doing pure passthrough. But the auth model assumes the gateway
   strips `publish_auth` and validates against the open-session
   record. Does plan 0008-followup need to land first, or is direct-
   to-broker acceptable for the v0.1 cut?

8. **Operator kill admin endpoint.** Recommend yes, behind
   `--admin-listen-addr` (default disabled). Plan 0018's roster UX
   is the long-term home; flag-gated raw HTTP is the interim. Ship
   in v0.1, or wait for 0018?

## 15. Out of scope (deferred)

- Gateway-side RTMP adapter (plan 0008-followup, parallel).
- ABR ladder transcoding — single passthrough output for v0.1; ABR
  follows the suite's `Preset` / `Ladder` shape at
  `livepeer-network-suite/video-worker-node/internal/providers/ffmpeg/live.go:60-111`.
- Verifiable-receipt extractor (chain-anchored proof-of-work; future
  plan post-0016).
- Per-stream chain-anchored billing (lands when 0016 closes).
- LL-HLS / WebRTC egress — HLS v3 only for v0.1.
- DRM / token-gated playback — operator concern, not broker
  architecture.
- Recording / VOD sink — live-only for v0.1.
- RTMPS at the broker boundary — plaintext between gateway and
  broker; gateway terminates customer TLS.
- Per-session FFmpeg version selection — single `--ffmpeg-binary`
  per broker.

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
  + `resources` block).

Prior reference impl
(`livepeer-cloud-spe/livepeer-network-suite/video-worker-node/`):

- `internal/providers/ingest/rtmp/rtmp.go:11-258` (RTMP listener
  pattern), `:162-192` (`OnPublish` flow + stream-key extraction),
  `:194-201` (`redactKey`), `:203-222` (RTMP→pipe pump).
- `internal/providers/ffmpeg/ffmpeg.go:142-229` (single-shot
  `SystemRunner` cmd/cancellation reference), `:329-376`
  (`ParseProgressStream`).
- `internal/providers/ffmpeg/live.go:60-111` (`BuildLiveArgs` ABR
  shape — v0.2 target), `:131-134` (`atomic.Int64` for `processed`
  — `LiveCounter` substrate), `:139-227` (`LiveSystemEncoder`),
  `:235-272` (`parseLiveProgress` monotonic-CAS).
- `internal/providers/hls/hls.go:13-58` (master manifest builder;
  v0.2 reference).
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
  UX; long-term home for operator session controls per §7 #5).
