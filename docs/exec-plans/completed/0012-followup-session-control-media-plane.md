---
plan: 0012-followup
title: session-control-plus-media — control-WS lifecycle + media-plane provisioning (design)
status: design-doc
phase: plan-only
opened: 2026-05-06
owner: harness
related:
  - "completed plan 0012 — session-control-plus-media driver pair (session-open phase)"
  - "active plan 0015 — interim-debit cadence (LiveCounter sibling interface)"
  - "active plan 0016 — chain-integrated payment-daemon (lands the ledger this mode bills against)"
  - "completed plan 0010 — ws-realtime driver (WebSocket transport prior art)"
  - "completed plan 0011-followup (parallel: rtmp media pipeline; sibling shape)"
  - "docs/design-docs/migration-from-suite.md (vtuber-worker-node deprecation row)"
audience: capability-broker maintainers, vtuber-session integrators, orch operators
---

# Plan 0012-followup — session-control-plus-media control-WS + media-plane provisioning (design)

> **Scope of this document.** Pure-paper design of the **control-WS lifecycle**, the
> **media-plane provisioning** machinery, and the **session-runner subprocess
> orchestration** that complete the `session-control-plus-media@v0` mode driver. **No
> Go code, no `go.mod` edit, no proto change ships from this commit.** The output is a
> set of pinned decisions, a component layout, a config grammar, a conformance plan,
> and a `DECIDED:` block per resolved decision (§14) — all ten Qs locked
> 2026-05-06.
>
> The mode wire-spec is already pinned in
> `livepeer-network-protocol/modes/session-control-plus-media.md:36-99` (plan 0002,
> accepted). The session-open phase is already shipped in
> `capability-broker/internal/modes/sessioncontrolplusmedia/driver.go:42-86` (plan
> 0012, completed). Plan 0012-followup lights up the URLs that today's driver returns
> in its 202 body but does not back with running services.

---

## 1. Status and scope

**In scope** — a single coherent followup that makes the mode end-to-end usable:

- Control-plane WebSocket lifecycle: connection establishment, auth, frame relay,
  heartbeat, idle policy, close semantics, error propagation.
- Media-plane provisioning: how the broker stands up a per-session media relay
  (publish leg + egress leg) and what protocol it speaks to the customer.
- Session-runner subprocess model: a workload-specific process per session that
  consumes control commands + incoming media and emits an output media stream;
  lifetime-bound to the session; managed by the broker.
- Broker ↔ session-runner IPC contract.
- LiveCounter wiring against plan 0015's interim-debit ticker so long sessions are
  billed at cadence.
- Configuration surface (broker flags + per-capability YAML additions).
- Conformance fixtures that exercise the full path end-to-end.
- Operator runbook deltas.

**Out of scope** — restated in §15. Headlines: session-open phase (plan 0012,
done); chain integration (plan 0016); interim-debit cadence (plan 0015, this plan
*uses* its `LiveCounter`); gateway-side adapter (plan 0008-followup); persona
authoring + multi-customer broadcaster + verifiable-output + DRM (consumer or v2
concerns).

---

## 2. What plan 0012 left unfinished

Plan 0012 ships the session-open POST → 202 response for `session-control-plus-
media@v0`. Today's driver at
`capability-broker/internal/modes/sessioncontrolplusmedia/driver.go:42-86` returns
`{session_id, control_url, media:{publish_url, publish_auth}, expires_at}`, but
**those URLs are dead**: nothing answers the WebSocket; nothing accepts a media
ingest; no per-session backend stands up (`driver.go:75` is the explicit
`"stub-publish-auth-"+sessID` placeholder). Plan 0012-followup makes them live —
broker accepts the control-WS, provisions a per-session media relay, launches
a workload-specific session-runner subprocess that consumes incoming media +
control commands and emits an output stream the customer plays back, and reports
work units on cadence so the session is billed while it runs.

---

## 3. Reference architecture

### 3.1. Data flow

```
                                            customer client
                                            ↑↓ control-WS (JSON, opaque)
                                            ↑↓ media (WebRTC by default)
gateway ── session-open POST ─→  capability-broker
                                  │  ┌──── media relay (pion/webrtc, in-proc)
                                  │  │
                                  │  │ raw RTP + JSON envelopes
                                  ▼  ▼
                                session-runner subprocess  (one per session;
                                                             operator-pulled image)
                                  ▲  ▲
                                  │  │ output frames + control replies
                                  └──┴──── media relay (egress) → customer player

capability-broker ◀── unix-socket gRPC, ticked by plan 0015 ──▶ payment-daemon
                                                                  (--mode receiver)
```

Three planes intersect at the broker: control (HTTP open + persistent WS), media
(WebRTC PC by default, RTMP+HLS as a per-capability opt-in), payment (broker →
payment-daemon over unix socket, ticked by plan 0015). The session-runner is
workload-specific; the broker is opaque to its semantics.

### 3.2. Lifetime — who terminates whom

**The broker is the authority.** Control-WS, media relay, and session-runner
subprocess are all broker-owned. Customer / gateway / runner can each *request*
graceful close (WS close frame, HTTP DELETE, `session.end` control message,
runner clean exit), but the broker observes the lifecycle event and tears down
the other two planes. Same pattern as ws-realtime at
`capability-broker/internal/modes/wsrealtime/driver.go:117-127`.

### 3.3. Egress URL — returned at session-open

**Recommended: in the 202 body, alongside `media.publish_url`.** The vtuber-session
capability's `media.schema` becomes `{publish_url, publish_auth, playback_url,
playback_auth}` (four fields instead of two). Customer player can start subscribing
before the runner emits the first frame; `media.playback_ready` follows on the
control-WS once frames flow. The wire-spec already says `media` is opaque to the
protocol (`livepeer-network-protocol/modes/session-control-plus-media.md:48-64`)
— this is a capability-schema change, not a mode-spec change.

---

## 4. Control-WS lifecycle

### 4.1. Connection establishment

The gateway (per spec §"Forwarding behavior" lines 142-149) MUST open `control_url`
immediately after the session-open response. The broker:

1. Accepts the WebSocket upgrade on `GET /v1/cap/{session_id}/control` (path is
   minted at session-open and stored in an in-memory session table, keyed by
   `session_id`).
2. Validates that the session-id path component matches a session opened within
   `expires_at`. If no such session, return `401 Unauthorized` (covers both "expired"
   and "never-existed" without leaking which).
3. Validates an auth token. **Decision (Q1 lock, §14): path-id-only — the session-id
   in the URL is the auth.** The id is generated from `crypto/rand` (12 bytes hex;
   see `driver.go:100-106`), so the URL is unguessable. A separate token in a header
   would add complexity for no security gain over the unguessable path. (Compare to
   ws-realtime: same shape, capability-bearer auth applies if configured per
   `wsrealtime/driver.go:67-85`.) Bearer is opt-in per-capability later if operators
   ask; no sibling header bearer in v0.1.

### 4.2. Connection lifetime

Connection lifetime **= session lifetime**. The broker holds the control-WS open
until one of:

- Customer closes cleanly (`session.end` from customer, or WS close frame).
- Broker closes (insufficient balance per plan 0015's `SufficientBalance` check;
  session-runner crash; session reaches `extra.max_session_seconds`).
- Session-runner exits cleanly (broker observes via the IPC channel, then closes the
  WS with `session.ended`).
- Reconnect window expires with no successful reconnect (see below).

#### 4.2.1. Reconnect-within-30s window (Q2 lock, §14)

A dropped control-WS does **not** immediately terminate the session. The broker
holds session-side state for `--session-control-reconnect-window` (default `30s`)
to allow the customer to reconnect across a transient network glitch — the
multi-hour vtuber session is exactly the case where a 5-second WiFi drop
shouldn't cost persona context.

**During the window:**

- The session-runner subprocess keeps running.
- The media-relay PC stays up.
- Plan 0015's payment-daemon ticker keeps billing — the customer is still
  consuming work-units, just not sending control messages.
- Server emits an IPC `ControlEnvelope` of type `runner.control_disconnected` to
  the runner with the WS close `code` (1006 abnormal close, 1011 server error,
  etc.) so the runner can pause emission or otherwise react gracefully. Runners
  are expected to handle this without terminating themselves.

**Reconnect mechanics:**

- A second WebSocket upgrade to the same `/v1/cap/{session_id}/control` path
  within the window is **accepted** (no longer `409 Conflict` for in-window
  reconnects). Same path-id auth check as §4.1.
- The client sends a `Last-Seq` header on the upgrade request (or `?last_seq=`
  query parameter — pick one at implementation time and document; the header is
  preferred to keep the URL clean).
- Server replays buffered server-emitted protocol messages with `seq > Last-Seq`,
  then resumes live delivery. **Customer-emitted messages are NOT replayed** —
  customer-side retry is the customer's concern.
- Once the new WS handshake completes, server emits `session.reconnected` over
  the new WS and an IPC `ControlEnvelope` of type `runner.control_reconnected`
  to the runner so the runner can resume emission.
- Replay buffer is bounded — reuses the §4.4 backpressure limits (default 64
  messages or 1 MiB JSON, configurable via
  `--session-control-reconnect-buffer-messages`). Overflow drops the oldest
  buffered message; client sees a gap, treats it the same as any other transport
  loss.

**Race policy.** If two reconnect attempts arrive simultaneously, **first to
complete the handshake wins**; the loser receives `409 Conflict` and must back
off / not retry within the window.

**After the window expires** with no successful reconnect: full teardown — runner
`Shutdown(graceful=true)` then SIGKILL after the `--session-runner-shutdown-grace`
window, media relay close, `Reconcile` + `CloseSession` per plan 0015 §3.3.
Cause emitted on the runner-crash path is `control_disconnect_window_expired`
(distinct from `runner_crashed`).

### 4.3. Frame protocol

JSON messages, UTF-8, one message per WebSocket text frame. The broker is **opaque**
to the message contents on the workload axis: it does not parse `set_persona` or
`interject_text` or any other capability-defined command. It does parse a small
fixed envelope:

- `type` (string, required): `"session.started"`, `"session.usage.tick"`,
  `"session.balance.low"`, `"session.error"`, `"session.ended"`, `"session.end"` are
  reserved for protocol use; any other `type` is workload-defined and relayed
  verbatim.
- `seq` (uint64, optional): sender-monotonic; broker passes through.
- `body` (object, capability-defined): the payload the session-runner cares about.

Protocol-reserved messages are short-circuited at the broker. Everything else is
shipped over the broker ↔ session-runner IPC channel (§6) verbatim and the runner's
replies come back the same way.

### 4.4. Backpressure

Bounded send-buffer per direction (default: 64 messages or 1 MiB JSON, whichever
hits first). Customer → runner: broker stops reading from the WS (TCP
backpressure propagates); if the buffer stays full for `backpressure_drop_after`
(default 5s), broker drops with a close frame carrying `Livepeer-Error:
backpressure_drop`. Runner → customer: runner sees IPC backpressure on the unix
socket and slows emission. `backpressure_drop` is a new `Livepeer-Error` code —
spec change at `livepeer-network-protocol/headers/livepeer-headers.md` and Go
constants at `capability-broker/internal/livepeerheader/headers.go:35-43`,
coordinated with plan 0015's `insufficient_balance` addition.

### 4.5. Heartbeat / idle policy

Broker sends a WS ping every `--session-control-heartbeat-interval` (default
10s); three missed pongs ⇒ close the WS as dead. The spec at
`session-control-plus-media.md:155-156` allows 60s idle as an option; this plan
pins active heartbeat (cheap, surfaces half-open TCP states the kernel hasn't
noticed).

A heartbeat-fail close does **not** immediately terminate the session — it
triggers the reconnect window per §4.2.1. Only after window expiry does the
session fully tear down.

### 4.6. Session-runner crash → control-WS close

On unexpected runner exit (non-zero, signal, OOM): emit `session.error` with
`code="runner_crashed"`, close WS with code 1011 + reason `runner_crashed`,
tear down media relay, `Reconcile` + `CloseSession` per plan 0015 §3.3. The
gateway MAY re-open a new session if runway remains. v0.1 does not support
runner-side hot-reconnect — crash terminates.

---

## 5. Media-plane provisioning

The mode-spec at
`livepeer-network-protocol/modes/session-control-plus-media.md:86-99` says the
**capability** defines the media plane; the broker stands it up. Plan
0012-followup lands the vtuber-session capability's media plane as the worked
example; broker code stays generic.

### 5.1. Publish leg — WebRTC vs RTMP

**Decision (Q3 lock, §14): WebRTC by default; RTMP as a per-capability opt-in.**
Vtuber is interactive (~100 ms publish→runner latency vs ~2–5 s for RTMP); WebRTC
also gives bidirectional negotiation and browser-native publish out of the box.
RTMP's appeal is reuse of plan 0011-followup's listener and a smaller broker
protocol surface — fine for non-interactive capabilities (long-form transcribe,
batch ingest). v0.1 ships WebRTC only; RTMP is a sibling commit when a
non-interactive capability lands.

### 5.2. Egress leg

WebRTC publish ⇒ **WebRTC subscribe** (same PC, or sibling PC; reuses ICE/DTLS).
RTMP publish ⇒ **HLS pull** at `media.playback_url`, reusing plan 0011-followup's
HLS sink. Non-media events (transcripts, persona-state notifications) ride the
control-WS, **not** a sibling SSE stream — matches the wire-spec separation at
`session-control-plus-media.md:75-83`.

### 5.3. Per-session media relay — in-process

Broker stands up the relay **in-process** (no sidecar), backed by
**`github.com/pion/webrtc/v3`** — accepted as a direct hard dep per Q4 lock
(§14). Go-native, MIT, no cgo; sibling to `gorilla/websocket` already used by
ws-realtime. One `RTCPeerConnection` per session (single PC describing both
directions, or two PCs if simpler). Track demux: incoming customer tracks
forwarded to runner as raw RTP. Track mux: runner-emitted tracks routed back
through egress.

### 5.4. SDP exchange location — control-WS

**Decision (Q5 lock, §14): SDP offer/answer flows over the control-WS**, not the
session-open POST body and not a sibling negotiation socket. Reasons: ICE
candidates trickle over time (single-shot HTTP body can't carry); mid-session
renegotiation needs a persistent channel; the customer hasn't built an offer at
session-open time; one fewer socket and one fewer auth surface. Concrete shape:

1. Session-open 202 returns `media.publish_url = "webrtc:negotiate-on-control-ws"`
   (capability-defined sentinel).
2. Customer connects control-WS, broker sends `{type:"media.negotiate.start"}`.
3. Customer sends `media.sdp.offer`; broker replies `media.sdp.answer`; ICE
   candidates trickle as `media.ice.candidate` in both directions.
4. PC reaches `connected`; broker emits `media.ready`; runner starts emitting.

---

## 6. Session-runner subprocess model

### 6.1. One subprocess per session

A workload-specific container per session, lifetime-bound to the session, broker-
owned. Tracked in the same in-memory session table as the control-WS.

### 6.2. Image distribution — operator-pulled, not vendored

Operator declares per-capability in `host-config.yaml` (full schema in §10.2). The
broker pulls / reuses the image via the host's container runtime; v0.1 supports
Docker only (`--container-runtime=docker`). The image is operator-trusted; pinning,
auditing, reproducibility are operator concerns.

Vendoring is rejected: the vtuber session-runner is a 1.6 GB image with Playwright
+ Chromium + Vite-built three-vrm renderer (anchor:
`livepeer-cloud-spe/livepeer-network-suite/livepeer-vtuber-project/session-runner/README.md:32-61`);
other capabilities under this mode will bring their own. The rewrite is not a
clearinghouse for runner images. A small stub runner ships for conformance only,
homed at `livepeer-network-protocol/conformance/runner/session-runner-stub/` per
Q6 lock (§14).

### 6.3. Broker ↔ session-runner IPC — gRPC over unix socket

**Recommendation: gRPC over a unix socket** at
`${session_runner_socket_dir}/sess_${session_id}.sock`, path passed to the runner
via env var `LIVEPEER_SESSION_RUNNER_SOCK`. Same transport pattern as
`payment-daemon` (one fewer thing for operators to debug). Schema lives at
`livepeer-network-protocol/proto/livepeer/sessionrunner/v1/{control,media}.proto`,
alongside existing payment proto.

Two services:

- `SessionRunnerControl`: bidi-stream `(ControlEnvelope)→(ControlEnvelope)` for
  workload commands; unary `Health()` for startup readiness; unary `Shutdown()`
  for graceful drain.
- `SessionRunnerMedia`: bidi-stream `(MediaFrame)→(MediaFrame)` for raw RTP.

### 6.4. Frame format — raw RTP

**Recommend: raw RTP packets** on the media channel. Pion demuxes incoming RTP
straight to the runner; runner-emitted RTP goes straight back to pion. Broker
stays codec-opaque. The decoded-YUV/PCM alternative would tie the broker to
codec libs and scale CPU cost with concurrent sessions — workloads that prefer
decoded frames can decode in the runner (cf.
`livepeer-vtuber-project/session-runner/src/session_runner/service/mux_pipeline.py`).

### 6.5. Failure modes

| Failure | Detection | Broker response |
|---|---|---|
| Runner panic (non-zero exit) | `Wait()` returns | `session.error` + WS close 1011 + Reconcile/CloseSession |
| OOM-killed | SIGKILL after kernel OOM | same; `cause="runner_oom"` |
| Runner hang (no IPC traffic for `--session-runner-stall-timeout`, default 30s) | watchdog | `Shutdown(graceful=false)` → SIGKILL after grace; same teardown |
| Socket dial fails at startup (within `--session-runner-startup-timeout`, default 30s) | broker observes | `cause="runner_startup_failed"`; teardown |
| Invalid IPC framing | per-frame parse error | error counter; >N in W seconds → kill |
| Control-WS dropped (runner alive) | WS read error / heartbeat-fail / kernel close | broker keeps runner alive for `--session-control-reconnect-window`; emits IPC `runner.control_disconnected` to runner; on successful reconnect, emits IPC `runner.control_reconnected` and replays buffered server-emitted messages with `seq > Last-Seq`; on window expiry, full teardown — runner `Shutdown(graceful=true)` then SIGKILL after grace; `cause="control_disconnect_window_expired"` |

All six surface to the customer as `session.error` on the control-WS; the gateway
(plan 0008-followup) is responsible for translating that to the human-facing
client. The control-WS-dropped row is the only one where the customer may avoid
session loss entirely by reconnecting in time (§4.2.1).

### 6.6. Sandboxing

Runner is operator-trusted; the broker doesn't enforce a sandbox beyond what the
container runtime provides. **Decision (Q7 lock, §14): `--cap-drop=ALL` on Docker
by default; opt back in via `--session-runner-extra-cap`.** Network mode and GPU
access stay operator-configurable per capability.

---

## 7. Worked example: vtuber session

`livepeer:vtuber-session` is the motivating customer of this mode. Today's suite
delivery: `livepeer-cloud-spe/livepeer-network-suite/vtuber-worker-node/`
(deprecated per `docs/design-docs/migration-from-suite.md:99-100`); the per-mode
Go module at
`vtuber-worker-node/internal/service/modules/vtuber_session/{module.go,system.go,events.go,streaming.go}`;
the Python runner at
`livepeer-cloud-spe/livepeer-network-suite/livepeer-vtuber-project/session-runner/`
(Open-LLM-VTuber fork; the business logic this plan orchestrates); the in-runner
WS handler at
`session-runner/src/session_runner/service/{control_ws.py,control_dispatcher.py}`;
the pytrickle egress at `session-runner/src/session_runner/service/trickle_sink.py`;
and the suite-side gateway at
`livepeer-cloud-spe/livepeer-network-suite/livepeer-vtuber-gateway/` (migrates
separately under plan 0008-followup).

### 7.1. What migrates

| Suite artifact | Rewrite home |
|---|---|
| `vtuber_session/module.go` (per-mode Go handler) | Deleted; replaced by generic `internal/modes/sessioncontrolplusmedia/` + operator `host-config.yaml` |
| `vtuber_session/events.go` (control taxonomy) | Standardized to the JSON-over-WS frame protocol in §4.3 |
| `vtuber-worker-node` payment middleware | Replaced by plan 0015's ticker + this plan's LiveCounter wiring |
| `session-runner/` (Python image) | Unchanged; operator pulls per `host-config.yaml.capabilities[].backend.session_runner.image` |
| `session-runner/.../control_ws.py` | Loses its own WS server; consumes broker-relayed envelopes over gRPC unix socket |
| `session-runner/.../trickle_sink.py` | Stays for capabilities preferring trickle; new WebRTC sink lands as sibling |
| `livepeer-vtuber-gateway/` | Migrates under plan 0008-followup |

### 7.2. Lifecycle

1. Customer's gateway issues session-open POST with `Livepeer-Capability:
   livepeer:vtuber-session:default`.
2. Broker validates payment, allocates `session_id`, launches the runner
   subprocess, waits for `Health()` (≤ `--session-runner-startup-timeout`).
3. Broker returns 202 with control + media URLs (publish set to
   `webrtc:negotiate-on-control-ws` sentinel).
4. Customer connects control-WS; broker emits `session.started`.
5. SDP offer/answer + ICE trickle exchanged over control-WS; PC connects;
   broker emits `media.ready`.
6. Customer mic RTP → broker → runner IPC. Runner does LLM + TTS + Live2D /
   three-vrm rendering inside its image (broker opaque). Runner-emitted RTP →
   broker → customer player.
7. Workload commands (`interject_text`, `set_persona`) flow customer → runner
   over control-WS, relayed verbatim by the broker.
8. Plan 0015's ticker reads `LiveCounter.CurrentUnits()` (here:
   `time.Since(session_open)/granularity`) every `--interim-debit-interval`;
   payment-daemon debits at cadence.
9. Customer `session.end` → broker `Shutdown(graceful=true)` to runner →
   teardown of PC + WS → final `Reconcile`/`CloseSession`.

Same lifecycle the deprecated vtuber-worker-node implements today; the win is
landing it in the **generic** mode driver so the next non-vtuber capability
(audio-only chat, podcast persona, voice-cloned NPC) gets it for free.

---

## 8. LiveCounter + interim-debit integration

This mode driver registers a `LiveCounter` on `Params.LiveCounter` per plan 0015
§4.2. Three viable work-unit shapes:

| Work unit | Existing extractor | LiveCounter source |
|---|---|---|
| `seconds-elapsed` (default) | `extractors/secondselapsed/` | `time.Since(start)/granularity`; trivially atomic |
| `bytes-counted` | `extractors/bytescounted/` | `atomic.Uint64` incremented on egress bytes leaving the PC |
| `tokens-generated` (new) | none — does not exist | Runner reports monotonic deltas over sibling gRPC method `SessionRunnerControl.ReportWorkUnits(stream)`; broker accumulates into `atomic.Uint64` |

`tokens-generated` is workload-knowledge; only the runner knows. **Decision (Q8
lock, §14): `runner-reported` is first-class.** It ships as a real extractor at
`internal/extractors/runnerreport/`, reusable by future modes — sets the
precedent for any future workload-reported counter. Plan 0015's ticker doesn't
care how the number is produced; it just calls `CurrentUnits()`.

---

## 9. Component layout

```
capability-broker/
  internal/
    modes/
      sessioncontrolplusmedia/
        driver.go          ← extends today's session-open-only file
        controlws.go       ← new: WS upgrade + frame protocol + heartbeat + reconnect-window state machine (per §4.2.1)
        controlws_reconnect.go ← new (or merged into controlws.go at the implementer's call): reconnect-window state, replay buffer, Last-Seq handling
        sessionmgr.go      ← new: per-session state table; startup/teardown
        livecounter.go     ← new: LiveCounter implementations for this mode
    media/
      sessionrunner/       ← NEW PACKAGE — runner subprocess lifecycle
        runner.go          ← Cmd wrapping; container runtime detection
        ipc.go             ← gRPC unix-socket client (control + media services)
        watchdog.go        ← stall + crash detection
      webrtc/              ← NEW PACKAGE — pion wrapper
        relay.go           ← per-session PC; track demux/mux
        sdp.go             ← offer/answer/ICE plumbing exposed to controlws
    extractors/
      runnerreport/        ← NEW — runner-reported counter (Q8 lock; first-class per §8)
        extractor.go
        livecounter.go

livepeer-network-protocol/
  proto/livepeer/sessionrunner/v1/   ← NEW
    control.proto
    media.proto
  conformance/fixtures/session-control-plus-media/
    happy-path.yaml         ← exists; session-open phase only
    end-to-end.yaml         ← NEW
    backpressure.yaml       ← NEW
    runner-crash.yaml       ← NEW
```

`media/sessionrunner/` and `media/webrtc/` are **shared with future modes** that
need the same shape. (E.g. a hypothetical `webrtc-realtime@v0` mode would be the
PC machinery in `media/webrtc/` plus a stripped-down control plane.) The
`session-control-plus-media` mode driver wires them together.

---

## 10. Configuration surface

### 10.1. Broker flags (new)

| Flag | Type | Default (recommended) | Purpose |
|---|---|---|---|
| `--session-control-max-concurrent-sessions` | uint | `100` | Cap on simultaneous active sessions in this mode. Hard reject above; observability metric counts rejections. |
| `--session-control-heartbeat-interval` | duration | `10s` | Control-WS ping interval (per §4.5). |
| `--session-control-missed-heartbeat-threshold` | uint | `3` | Number of missed pongs before connection is declared dead. |
| `--session-control-reconnect-window` | duration | `30s` | Per-session window during which a dropped control-WS may be reconnected before full teardown (Q2 lock; §4.2.1). |
| `--session-control-reconnect-buffer-messages` | uint | `64` | Max server-emitted messages buffered per session for replay on reconnect (§4.2.1). |
| `--session-runner-startup-timeout` | duration | `30s` | Max time from subprocess launch to `Health()` ready. |
| `--session-runner-stall-timeout` | duration | `30s` | Max IPC silence before watchdog kills the runner. |
| `--session-runner-shutdown-grace` | duration | `5s` | Time the runner gets to drain on `Shutdown(graceful=true)` before SIGKILL. |
| `--session-runner-socket-dir` | path | `/var/run/livepeer/session-runner/` | Directory under which per-session unix sockets are created. |
| `--container-runtime` | string | `docker` | Runner-launch backend; v0.1 supports `docker` only. Future: `containerd`, `podman`, `process` (no-container debug shape). |
| `--webrtc-public-ip` | string | (host's outbound IP, auto-detected) | NAT-traversal-relevant; advertised in ICE candidates. Pinned because UDP NAT discovery is brittle. |
| `--webrtc-udp-port-min` / `--webrtc-udp-port-max` | uint | `40000`–`49999` | UDP range pion binds for media. Operator firewall must open this range. |

These complement plan 0015's `--interim-debit-interval` family; the two flag families
are independent.

### 10.2. Per-capability YAML

Augments the `host-config.yaml` capability schema (anchor:
`docs/design-docs/architecture-overview.md:97-115`) with a `session_runner` block
under `backend`:

```yaml
capabilities:
  - id: "livepeer:vtuber-session:default"
    interaction_mode: "session-control-plus-media@v0"
    work_unit:
      name: "seconds"
      extractor:
        type: "seconds-elapsed"
        granularity: 1
    price:
      amount_wei: 1500000
      per_units: 1
    backend:
      transport: "session-runner"          # NEW transport kind
      session_runner:                       # NEW block
        image: "livepeer-vtuber/session-runner:v0.4"
        command: ["python", "-m", "session_runner"]
        env:
          OPENAI_API_KEY: "${OPENAI_API_KEY}"
        resources:
          memory: "2GiB"
          cpu: "2"
          gpus: 1                          # optional
        startup_timeout: "30s"             # per-capability override of broker default
        media:
          publish:
            transport: "webrtc"            # or "rtmp"
          egress:
            transport: "webrtc"            # or "hls"
```

The `transport: "session-runner"` value is a new fourth transport kind alongside
`http`, `ws`, and `rtmp`. The broker's backend registry grows one entry.

### 10.3. Versioned compatibility

The mode-spec version stays `0.1.0` — no wire-shape change to the session-open POST
or 202 response body schema. The `media.schema` for vtuber-session gains
`playback_url` + `playback_auth` siblings (per §3.3); since the spec already says
`media` is opaque to the protocol, this is a **capability schema change**, not a
**mode wire-spec change**. No spec bump.

---

## 11. Conformance fixtures

Today's
`livepeer-network-protocol/conformance/fixtures/session-control-plus-media/happy-path.yaml`
covers only session-open. This plan adds three siblings:

- **`end-to-end.yaml`** — runner issues session-open POST, connects control-WS,
  sends `echo.request` + asserts echoed reply within 2s, negotiates a minimal
  WebRTC PC (one audio track each direction), pushes 1s of synthetic Opus on
  publish, reads ≥1s of echoed Opus on egress, sends `session.end`, asserts WS
  close + payment debit ≈ session duration + no leaked subprocesses or sockets.
- **`backpressure.yaml`** — runner stops reading control-WS while the broker
  delivers `session.usage.tick` at high rate. Asserts broker-side close,
  `Livepeer-Error: backpressure_drop` on the close-frame reason, ledger
  reflects accumulated units up to drop.
- **`runner-crash.yaml`** — stub runner variant panics 2s after startup.
  Asserts control-WS receives `session.error` with `cause="runner_crashed"`
  within 5s, WS closes with code 1011, final `Reconcile`/`CloseSession`
  arrive at the daemon (indirect inference via `GetBalance` per plan 0015 §9.1).
- **`reconnect-window.yaml`** — runner connects control-WS, exchanges a few
  server-emitted messages (capturing `seq`), then force-closes the WS without
  sending `session.end`. Within 5s the runner reconnects to the same path with
  `Last-Seq` set to the last observed `seq`; asserts buffered server-emitted
  messages with `seq > Last-Seq` replay over the new WS, `session.reconnected`
  fires, the session continues, and the payment ledger reflects continuous
  billing across the gap (the runner kept running, the ticker kept ticking). A
  sub-fixture (or sibling `reconnect-window-expired.yaml`) repeats the
  force-close but does **not** reconnect; asserts that after
  `--session-control-reconnect-window`, the broker tears down with
  `cause="control_disconnect_window_expired"` and final
  `Reconcile`/`CloseSession` land at the daemon.

**Test infrastructure deltas.** Stub image
`tztcloud/livepeer-conformance-session-runner:v0` (echoes control envelopes;
echoes media frames; panic-on-startup variant for the crash fixture).
`webrtc-publisher-fake` compose sidecar to drive synthetic Opus into the
broker's PC. UDP port range `40000-40999/udp` opened in compose. Stub-image
source home is `livepeer-network-protocol/conformance/runner/session-runner-stub/`
per Q6 lock (§14).

---

## 12. Operator runbook updates

A new §"Session-control-plus-media operations" lands in
`payment-daemon/docs/operator-runbook.md` (the cross-cutting operator runbook):

- **Container-runtime prereq.** Docker daemon running; image registry creds.
- **Image management.** Pin to digest in production; rotation = push new image
  + update `host-config.yaml` + SIGHUP broker.
- **WebRTC firewall.** UDP `40000-49999` reachable from customer clients;
  STUN config for NAT traversal; TURN is operator-provisioned (broker doesn't
  bundle one).
- **Resource sizing.** Vtuber-session ≈ 2 GiB RAM + 2 CPU per session; capacity
  formula = `--session-control-max-concurrent-sessions` × per-session sizing.
- **Common failure modes.** Runner crashed (check logs: OOM → raise
  `resources.memory`; missing env; pull failure → registry creds);
  control-WS keeps disconnecting (NAT/firewall; pong RTT vs heartbeat);
  SDP failure (verify `--webrtc-public-ip`; UDP range open; client STUN reachable);
  session starvation (concurrent-sessions cap hit).
- **Observability.** New metrics
  `livepeer_mode_session_runner_subprocess_total{outcome}` (counter; outcome ∈
  {started, exited_clean, crashed, oom_killed, watchdog_killed}),
  `livepeer_mode_session_control_ws_active{capability}` (gauge),
  `livepeer_mode_session_media_pc_state{state}` (counter).

---

## 13. Migration sequence

Estimated 6–8 commits, independently reviewable:

1. **C1 — `feat(broker): control-WS scaffold`.** WS upgrade + frame envelope +
   heartbeat + reconnect-window state machine + replay buffer + `Last-Seq`
   handling in `internal/modes/sessioncontrolplusmedia/controlws.go` (and
   `controlws_reconnect.go` if split for reviewability); in-memory session table;
   new `Livepeer-Error: backpressure_drop` constant. **If C1 risks growing too
   big to review, split as C1a (WS upgrade + frame envelope + heartbeat) and C1b
   (reconnect-window + replay buffer + `Last-Seq`).** Total commit count stays at
   8; editor's call on the split.
2. **C2 — `feat(broker): pion/webrtc relay skeleton`.** New package
   `internal/media/webrtc/`. PC creation + SDP offer/answer + ICE trickle, wired
   to a stub "loopback" backend (publish→egress, no runner). New flags
   `--webrtc-public-ip` + UDP port range.
3. **C3 — `feat(broker): session-runner subprocess lifecycle`.** New package
   `internal/media/sessionrunner/`. Docker launcher, `Health()`, watchdog,
   graceful shutdown. Proto schema in
   `livepeer-network-protocol/proto/livepeer/sessionrunner/v1/`. New flags
   `--container-runtime`, `--session-runner-*`.
4. **C4 — `feat(broker): control-WS ↔ session-runner relay`.** Wires C1 to C3.
   Workload envelopes relayed verbatim; protocol-reserved short-circuited.
5. **C5 — `feat(broker): media relay publish leg`.** C2's PC publish tracks
   to C3's media IPC. Raw RTP pass-through.
6. **C6 — `feat(broker): media relay egress leg`.** C3's media IPC output
   back through C2's egress tracks. SDP ordering pinned (client-offers).
7. **C7 — `feat(modes): LiveCounter + runner-reported extractor`.** Mode
   driver exposes `LiveCounter` per plan 0015 §4.2; new extractor
   `internal/extractors/runnerreport/`. Gated on plan 0015 landing first.
8. **C8 — `test(conformance): end-to-end + backpressure + runner-crash`.**
   Three fixtures + stub image build + runbook deltas; plan moves to
   `completed/`.

C2 and C5+C6 may merge if pion glue is small. C7 can slip if plan 0015 hasn't
landed; C1–C6 + C8 are independent of plan 0015 (interim-debit layers on later).

---

## 14. Resolved decisions

All ten open questions resolved on 2026-05-06. The implementing agent works
against these locks; rationale captured for future readers. One substantive
override (Q2) is called out explicitly.

### Q1. Control-WS auth

**DECIDED: path-id-only — the unguessable 12-byte hex session-id IS the auth
(§4.1).** No sibling header bearer for v0.1. The id is `crypto/rand` 12 bytes
hex (see `driver.go:100-106`); a separate header token would add complexity for
no security gain over the unguessable URL. Bearer is opt-in per-capability later
if operators ask — the `Authorization` header slot stays unallocated, not
forbidden.

### Q2. Reconnectable control-WS (OVERRIDE)

**DECIDED: in-v0.1 — ship a reconnect-within-30s window.** Plan recommended
deferral ("no for v0.1; add if operators ask"); user requested in-v0.1 because
multi-hour vtuber sessions are exactly the case where transient WiFi drops
matter most — losing persona context on a 5-second drop is unacceptable UX.
Mechanics in §4.2.1: server-side state survives for
`--session-control-reconnect-window` (default `30s`), runner stays alive,
payment ticker keeps billing, second WS upgrade within window is accepted (no
longer `409 Conflict` for in-window reconnects), client sends `Last-Seq` and
broker replays buffered server-emitted messages with `seq > Last-Seq`,
customer-emitted messages are not replayed (customer-side retry concern), race
resolved by first-to-complete-handshake, loser gets `409 Conflict`. Window
expiry triggers full teardown with `cause="control_disconnect_window_expired"`.
Two new flags: `--session-control-reconnect-window`,
`--session-control-reconnect-buffer-messages`. Two new IPC envelope types:
`runner.control_disconnected`, `runner.control_reconnected`.

### Q3. Publish leg — WebRTC vs RTMP vs both

**DECIDED: WebRTC default; RTMP as per-capability opt-in (§5.1).** Vtuber is
interactive (~100 ms publish→runner latency vs ~2–5 s for RTMP); WebRTC also
gives bidirectional negotiation and browser-native publish. RTMP reuses plan
0011-followup's listener — fine for non-interactive capabilities. v0.1 ships
WebRTC only; RTMP lands as a sibling commit when a non-interactive capability
under this mode arrives.

### Q4. pion/webrtc dependency

**DECIDED: accept pion as a direct hard dep (§5.3).** Go-native, MIT, no cgo;
sibling to `gorilla/websocket` already used by ws-realtime. No interface wrap
for swap-out — wrap only if a second impl candidate concretely appears. The
sidecar-process alternative trades fewer deps for more ops surface, which is
the wrong trade for v0.1.

### Q5. SDP exchange location

**DECIDED: SDP offer/answer + ICE trickle over the control-WS (§5.4).** No
sibling `wss://.../media-negotiate` socket. One fewer socket, one fewer auth
surface. Concrete shape in §5.4. Revisit if SDP traffic ever measurably
competes with workload traffic on the same socket.

### Q6. Stub session-runner image home

**DECIDED: conformance home —
`livepeer-network-protocol/conformance/runner/session-runner-stub/`.**
First-time operators read the conformance README anyway; the stub belongs
beside the fixtures it serves. Image published as
`tztcloud/livepeer-conformance-session-runner:v0` (cf. §11).

### Q7. Runner sandboxing posture

**DECIDED: drop-all caps default + opt back in via `--session-runner-extra-cap`
(§6.6).** Network mode and GPU access stay per-capability operator-configurable
(GPU especially — vtuber-session needs it; transcribe-batch may not). No
seccomp / no host-network mandate at the broker layer; deployment-level concern.

### Q8. runner-reported extractor — first-class or anonymous

**DECIDED: first-class — new `internal/extractors/runnerreport/` package
(§8).** Reusable across future modes; sets the precedent for any future
workload-reported counter. Plan 0015's ticker doesn't care how the number is
produced; it just calls `CurrentUnits()`.

### Q9. Vtuber-specific subfolder

**DECIDED: defer.** Stay an operator-config-driven capability for v0.1;
promote to its own `capabilities/vtuber-session/` doc subfolder only if a
second non-vtuber capability under this mode forces the abstraction. Until
then the `host-config.yaml` capability entry plus the operator-pulled image is
the entire surface.

### Q10. Sequencing with plan 0008-followup

**DECIDED: this plan ships first; gateway-side adapter (plan 0008-followup)
follows.** The broker side is the long pole; the TS gateway adapter is
mechanical once the broker URLs answer for real.

---

## 15. Out of scope (deferred)

Each item has a forwarding address.

| Item | Forwarding address |
|---|---|
| Gateway-side adapter (TS middleware) | plan 0008-followup |
| Real chain integration | plan 0016 (this plan ships against plan 0014's stub providers) |
| Interim-debit cadence machinery (ticker, `SufficientBalance`, the interface itself) | plan 0015 (this plan *uses* it) |
| Per-mode tick-rate overrides | plan 0015 followup |
| Container runtimes other than Docker | sibling commits when operators ask |
| Persona authoring tooling, scene editors, prompt-graph IDEs | `livepeer-vtuber-project/` (consumer app) |
| Cross-session persona memory persistence | operator-app concern |
| Multi-customer-into-one-session-runner (broadcaster topology) | v2 — needs different payment model + fanout |
| Verifiable persona-output authenticity (chain-anchored proof) | future plan, decoupled |
| DRM / paid-replay / content moderation on egress | operator concern; infra exposes hooks only |
| Renderer-process direct IPC (skipping subprocess, inline renderer) | explicit non-goal — broker stays workload-opaque |
| Reconnect with state-restoration beyond protocol replay (LLM history reconstruction, in-flight tool calls, etc.) | runner's responsibility on `runner.control_reconnected`; broker only replays server-emitted protocol messages with `seq > Last-Seq` per §4.2.1, and customer-emitted messages are NOT replayed by the broker |

---

## Appendix A — file paths cited

**This monorepo.**
`capability-broker/internal/modes/sessioncontrolplusmedia/driver.go:42-86` —
session-open-only handler this plan extends (placeholder
`"stub-publish-auth-"+sessID` at `:75`).
`capability-broker/internal/modes/wsrealtime/driver.go:58-128` (Serve),
`:117-127` (cancel-on-either-close), `:133-147` (`pumpFrames`) — WS prior art.
`capability-broker/internal/modes/types.go:34-41` (`Params`),
`capability-broker/internal/extractors/types.go:16-25` (extractor + LiveCounter
home per plan 0015 §4.1–4.2).
`capability-broker/internal/livepeerheader/headers.go:35-43` (this plan adds
`backpressure_drop`).
`livepeer-network-protocol/modes/session-control-plus-media.md:36-99` (wire
spec; not changed by this plan).
`livepeer-network-protocol/headers/livepeer-headers.md` (canonical error list;
grows by `backpressure_drop`).
`livepeer-network-protocol/conformance/fixtures/session-control-plus-media/happy-path.yaml`
(siblings land in C8).
`payment-daemon/docs/operator-runbook.md` (runbook deltas land here).
`docs/exec-plans/completed/0012-session-control-plus-media-driver.md` (parent
plan).
`docs/exec-plans/active/0015-interim-debit-cadence-design.md:154-217`
(LiveCounter interface this plan consumes).
`docs/exec-plans/active/0016-chain-integrated-payment-design.md` (chain
integration; orthogonal, same provider interfaces).
`docs/design-docs/architecture-overview.md:74-92` (Layer 2 mode taxonomy).
`docs/design-docs/migration-from-suite.md:99-100` (vtuber-worker-node row
points at this plan).

**Sibling tree `livepeer-cloud-spe/livepeer-network-suite/`.**
`vtuber-worker-node/internal/service/modules/vtuber_session/{module.go,
system.go,events.go,streaming.go,deps.go}` (deprecated per-mode Go module;
replaced by the generic mode driver in this plan).
`vtuber-worker-node/internal/{runtime/http/,providers/payeedaemon/}` (HTTP
shell + payment client; both collapsed into the broker).
`livepeer-vtuber-project/session-runner/README.md:32-61` (1.6 GB image
shape; operator-pulled).
`livepeer-vtuber-project/session-runner/src/session_runner/service/{control_ws.py,
control_dispatcher.py,trickle_sink.py,mux_pipeline.py,egress_segment_sink.py}`
(current WS handler + trickle/MPEG-TS egress; WS server moves to broker;
sibling WebRTC sink lands here).
`livepeer-vtuber-project/session-runner/src/session_runner/{runtime/,ui/}`
(FastAPI + HTTP routes; broker takes over the surface).
`livepeer-vtuber-gateway/` (suite-side gateway; plan 0008-followup,
separate).

**Sibling tree `livepeer-cloud-spe/livepeer-modules-project/`.**
`payment-daemon/internal/repo/{sessions,sessiondebits}/...` (prior session
ledger; rewrite's plan 0014 already wire-compat).
`worker-runtime/` (deprecated per `migration-from-suite.md:78-81`;
capability-as-Go-Module pattern is what the broker + this plan kill).
