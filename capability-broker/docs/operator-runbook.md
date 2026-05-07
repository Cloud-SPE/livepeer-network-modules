# capability-broker — operator runbook

Cross-cutting operational guidance for running `livepeer-capability-broker`
on an orchestrator host. Pairs with `payment-daemon/docs/operator-runbook.md`
(payment-side concerns) and the spec subfolder
(`livepeer-network-protocol/`) for wire-shape questions.

## 1. Listener topology

The broker exposes three listeners; default ports + roles:

| Flag | Default | Purpose | Reachability |
|---|---|---|---|
| `--listen` | `:8080` | Paid `/v1/cap` dispatch + `/registry/*` + `/_hls/...` LL-HLS playback. | Gateway-reachable (LAN or public, operator's call). |
| `--metrics` | `:9090` | Prometheus scrape endpoint. | Operator's metrics network only. |
| `--rtmp-listen-addr` | empty (disabled) | RTMP ingest for `rtmp-ingress-hls-egress@v0`. Set to e.g. `:1935`. | Gateway-reachable; **never** directly to the public internet. |

Configure each listener separately at the network layer. The RTMP listener
in particular is plaintext (no TLS); the gateway terminates customer TLS
upstream per the locked plan-0011-followup §13 Q1 decision.

## 2. RTMP pipeline (mode `rtmp-ingress-hls-egress@v0`)

Live RTMP → FFmpeg → LL-HLS pipeline lit up by plan 0011-followup.

### 2.1. Port exposure

Default port `:1935` (IANA-reserved). Reachable from the gateway only.
Cloud security-group rules:

- **Allow:** ingress on `1935/tcp` from the gateway's source CIDR.
- **Deny:** ingress on `1935/tcp` from `0.0.0.0/0`.

The gateway adapter (plan 0008-followup) is the customer-facing TLS +
auth wrapper. v0.1 deployments without the gateway adapter expose `:1935`
directly to customers (smoke-grade); production deployments add the
gateway in front.

The URL the broker returns at session-open is path-shaped:
`rtmp://<broker>:1935/<session_id>/<stream_key>`. The `stream_key`
field on the session-open response is a 32-byte URL-safe random bearer
the listener constant-time-compares against the open-session record.
Mismatched stream keys get an RTMP `_error` immediately.

### 2.2. GPU encoder hardware

Per the locked plan-0011-followup §13 Q3 decision, the production
default is a 5-rung H.264 ABR ladder with NVIDIA NVENC primary. NVIDIA
Pascal+ (GTX 1060 / 1070 / 1080 minimum; Turing / Ampere / Ada
recommended) is the Livepeer transcoder fleet norm.

Operators select the encoder via `--encoder=auto|nvenc|qsv|vaapi|libx264`:

- **`auto` (default)** — runtime probes for available encoders and
  prefers in order: NVENC → QSV → VAAPI → libx264. Probe walks
  `ffmpeg -hide_banner -encoders` plus `-init_hw_device` for each
  vendor; first available wins.
- **`nvenc`** — NVIDIA NVENC. Requires NVIDIA driver + cuda-toolkit
  installed on the host. Validate with `ffmpeg -hide_banner -encoders
  | grep h264_nvenc`.
- **`qsv`** — Intel QuickSync. Requires `intel-media-driver` (or
  `intel-media-va-driver-non-free` on older releases) + Skylake+
  iGPU or discrete Arc GPU.
- **`vaapi`** — generic VAAPI. Covers AMD VCN-capable GPUs (RX 5700+)
  + Intel iGPU via the mesa VAAPI driver (`mesa-va-drivers`).
- **`libx264`** — software CPU x264. **Not the default for production
  deployments.** Available as an explicit operator opt-in for
  hardware-less environments.

If `--encoder=auto` finds no GPU encoder AND `--encoder-allow-cpu=false`
(default), the broker refuses to start with a clear error:

```
error: no GPU encoder detected; install NVIDIA driver + cuda-toolkit,
OR set --encoder-allow-cpu=true to use libx264 (production deployments
should use a GPU)
```

Production deployments should keep `--encoder-allow-cpu=false`. CI /
dev environments flip the flag.

### 2.3. Profile selection

`backend.profile` per capability in `host-config.yaml`:

| Profile | Use | GPU required? |
|---|---|---|
| `passthrough` | `-c:v copy -c:a copy`. CI smoke / dev / no-transcode pass-through. | No |
| `h264-live-1080p-nvenc` | 5-rung H.264 ABR ladder (240p / 360p / 480p / 720p / 1080p) + AAC, NVENC. **Production default.** | NVIDIA Pascal+ |
| `h264-live-1080p-qsv` | Same ladder, Intel QuickSync. | Intel Skylake+ |
| `h264-live-1080p-vaapi` | Same ladder, VAAPI (AMD / Intel iGPU). | AMD RX 5700+ or Intel iGPU |
| `h264-live-1080p-libx264` | Same ladder, software x264. Operator opt-in only. | No |

Bitrate ladder mirrors Apple's HLS Authoring Spec + Mux's published
encoder recommendations: 240p baseline 400 kbps, 360p baseline 800 kbps,
480p main 1400 kbps, 720p main 2800 kbps, 1080p high 5000 kbps. AAC
stereo audio shared across rungs. 4s GOP.

### 2.4. FFmpeg licensing

Broker ships LGPL FFmpeg by default (no `--enable-gpl`). Operators
who want GPL libs (x264 / x265 default encoders) supply their own via
`--ffmpeg-binary=/usr/local/bin/my-ffmpeg`. The relicensing
implication is the operator's responsibility.

LGPL is sufficient for: NVENC, QSV, VAAPI, AAC, fmp4 muxing, LL-HLS.
GPL is required for: software libx264 / libx265 (when using GPL
builds). The default `libx264` profile we ship uses the LGPL build of
libx264; operators bringing GPL builds must `--ffmpeg-binary` swap.

### 2.5. LL-HLS playback

LL-HLS is the v0.1 default per plan-0011-followup §13 Q5 lock.
Glass-to-glass latency ~1-3 seconds with 2s segment duration + 333ms
parts + 4-segment rolling window.

Player compatibility:

- **Compatible** — hls.js 1.0+, native Safari (macOS / iOS).
- **Compatible with caveats** — Android browsers' built-in players
  may need fallback to legacy HLS v3 for older Android versions.
- **Incompatible** — niche players that only speak HLS v1-v3.

For player compatibility issues, flip `--hls-legacy=true` to fall
back to mpegts segments + standard HLS v3 (4-6s segments, ~12-24s
glass-to-glass). The flag is operator-side, not per-capability.

### 2.6. Resource sizing per concurrent stream

Approximate per-stream load (pinned 1080p source @ 30fps):

| Profile | CPU | RAM | tmpfs scratch |
|---|---|---|---|
| `passthrough` | ~0.1-0.3 cores | ~50 MB | ~25 MB |
| `h264-live-1080p-nvenc` | ~1.0 cores (host) + GPU NVENC slot | ~200 MB | ~80 MB (1080p rung dominates) |
| `h264-live-1080p-libx264` | ~3-4 cores (5 rungs encoded in software) | ~500 MB | ~80 MB |

For 100 concurrent passthrough streams: ~10-30 cores, ~5 GB RAM,
~2.5 GB tmpfs. For 10 concurrent NVENC ABR ladders: ~10 cores,
~2 GB RAM, ~800 MB tmpfs, plus GPU NVENC sessions (Pascal allows
3-5 concurrent, Turing+ allows ~8-15).

Operators cap at the listener level via `--rtmp-max-concurrent-streams`
(default 100). Above the cap, the listener accepts the TCP connection
then rejects in `OnPublish`.

### 2.7. Common failure modes

| Symptom | Likely cause | What to check |
|---|---|---|
| Customer encoder gets RTMP `_error` immediately | Stream-key auth failed (constant-time mismatch) or session has expired. Broker logs `rtmp.publish_rejected` with redacted key prefix. | Confirm the gateway minted the right URL and the customer pushed within the session's `expires_at`. |
| `Livepeer-Error: ffmpeg_subprocess_failed` on a control-WS or in broker logs | FFmpeg subprocess exited non-zero before the RTMP push completed. 128KB stderr ring buffer captured. | Inspect `livepeer_ffmpeg_subprocess_failures_total{capability,reason}` for the failure class. Common: codec not found, unsupported source format, GPU unavailable. |
| `Livepeer-Error: rtmp_ingest_idle_timeout` | RTMP push was idle for `--rtmp-idle-timeout` (default 10s). Customer encoder may have stalled or the network is dropping packets. | Check the gateway's logs for the per-customer connection state; raise `--rtmp-idle-timeout` if the operator's customers run very low-bitrate / sparse-packet streams. |
| `Livepeer-Error: insufficient_balance` (trailer or in logs) | Payment-daemon's `SufficientBalance` returned false on a tick. Plan 0015. | Have the gateway raise the initial `face_value`. See `payment-daemon/docs/operator-runbook.md` §6.5.3 for the termination flow. |
| Disk-full on segment write | tmpfs scratch sized too small for concurrent-stream count. | Resize the `--hls-scratch-dir` mount; see §2.6 for sizing guidance. |
| `lt; 1s` interim-debit tick warning | Operator set `--interim-debit-interval` below 1s. The warning is intentional. | Raise the value to `1s` minimum for production. |

### 2.8. Observability metrics

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `livepeer_rtmp_active_sessions` | gauge | (none) | In-flight publish-state RTMP connections. |
| `livepeer_rtmp_bytes_in_total` | counter | `capability,offering` | RTMP bytes received from customer encoders, per-capability. |
| `livepeer_hls_segments_written_total` | counter | `capability,offering` | LL-HLS / HLS segments flushed to scratch. |
| `livepeer_ffmpeg_subprocess_failures_total` | counter | `capability,reason` | Non-zero FFmpeg exits classified by stderr-pattern (`codec_not_found`, `gpu_unavailable`, `network_drop`, `unknown`). |
| `livepeer_rtmp_idle_timeouts_total` | counter | (none) | Sessions reaped by the watchdog idle-timeout trigger (plan 0011-followup §7.2). |
| `livepeer_mode_hls_cleanup_failed_total` | counter | (none) | scratch-dir `RemoveAll` failures (soft fail; logged). |

Plus the cross-cutting metrics from the payment middleware
(`livepeer_payment_*`); see `payment-daemon/docs/operator-runbook.md` §8.

## 3. Other modes

This runbook will grow per-mode sections as `0012-followup`
(`session-control-plus-media`) and `0008-followup` (gateway-side
adapters) land. v0.1 ships only the RTMP-pipeline section above; the
HTTP-family modes (`http-reqresp`, `http-stream`, `http-multipart`)
need no operator-side guidance beyond the listener topology in §1.
