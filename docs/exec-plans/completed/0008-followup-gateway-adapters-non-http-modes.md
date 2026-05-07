---
status: completed
opened: 2026-05-06
closed: 2026-05-07
owner: harness
related: plan 0008 (completed), plan 0009 (completed), plan 0010 (completed), plan 0011 (completed) + 0011-followup, plan 0012 (completed) + 0012-followup, plan 0014 (completed), plan 0015 (design)
audience: gateway-adapters maintainers, gateway operators (`openai-gateway/`, suite gateways), broker-side mode-driver maintainers
---

> **Closed 2026-05-07.** All §13 locks honoured. C8 (reference
> `openai-gateway/` adopts `ws-realtime`) deferred per the plan's own
> "may slip to a future commit" carve-out — not gating the plan close.
> See the closing commit message for the full deviation log.

# Plan 0008-followup — gateway-adapters for non-HTTP modes (design)

**This is a paper-only design doc.** No TypeScript code, no Go code, no
`package.json` or `go.mod` edits ship from this commit. The goal is to
settle the language-strategy fork and pin the per-mode wire shape on
the gateway side so the implementing commits are mechanical.

## 1. Status and scope

Scope: **gateway-side adapter middleware for the three non-HTTP modes**
in `livepeer-network-protocol/`:

- `ws-realtime@v0` — `livepeer-network-protocol/modes/ws-realtime.md`.
- `rtmp-ingress-hls-egress@v0` — `livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md`.
- `session-control-plus-media@v0` — `livepeer-network-protocol/modes/session-control-plus-media.md`.

This plan papers the gateway-side counterpart to the three broker-side
drivers that already shipped session-open phase only:

- `capability-broker/internal/modes/wsrealtime/driver.go:11-29` — full
  bidirectional relay (plan 0010 closed).
- `capability-broker/internal/modes/rtmpingresshlsegress/driver.go:1-50` —
  session-open POST only; RTMP listener / FFmpeg / HLS sink deferred to
  plan 0011-followup.
- `capability-broker/internal/modes/sessioncontrolplusmedia/driver.go:1-47` —
  session-open POST only; control WebSocket lifecycle and media-plane
  provisioning deferred to plan 0012-followup.

Out of scope (covered elsewhere or deferred):

- Broker-side media planes — plan 0011-followup (RTMP listener + FFmpeg +
  HLS sink) and plan 0012-followup (control-WS lifecycle + media-plane
  provisioning). This plan describes the symmetric **payer-side** work.
- Chain integration — plan 0016.
- Existing HTTP-family adapters (`http-reqresp@v0`, `http-stream@v0`,
  `http-multipart@v0`) shipped under plan 0008 and are unchanged.
- Interim-debit cadence — broker-side ticker is plan 0015; gateway-side
  has no ticker work (the bill is computed on the broker side and the
  gateway just reads it via `Livepeer-Work-Units` on close or via
  control-plane events).
- Customer-facing gateway concerns (auth, billing, customer model,
  rate-limit, multi-tenancy) — per-deployment, not in this monorepo.
- DRM / token-gated playback URLs.
- Federated / multi-instance gateway clusters.

## 2. What plan 0008 left unfinished

Plan 0008 (closed 2026-05-06,
`docs/exec-plans/completed/0008-gateway-adapters-typescript-middleware.md:1-72`)
shipped TypeScript middleware for the HTTP family
(`gateway-adapters/src/modes/http-reqresp.ts`, `http-stream.ts`,
`http-multipart.ts`) under `@tztcloud/livepeer-gateway-middleware`
v0.1.0 (`gateway-adapters/package.json:1-39`). The non-HTTP modes
have broker-side drivers but no symmetric gateway-side adapter —
gateway authors who want `ws-realtime`, `rtmp-ingress-hls-egress`, or
`session-control-plus-media` today hand-roll upgrade handshakes,
payment-header injection, frame relay, RTMP listeners, and WebRTC
plumbing themselves. Plan 0008-followup completes the gateway-adapter
set. Tracked at `PLANS.md:127`.

## 3. Architectural fork — language strategy

The HTTP-family adapters in `gateway-adapters/` are TypeScript-only with
zero runtime dependencies (`gateway-adapters/AGENTS.md:13-25`). The non-
HTTP modes do not all fit that mould.

### 3.1. DECIDED: Path B (TS + Go split).

**DECIDED 2026-05-06: Path B — TS + Go split inside `gateway-adapters/`.**
Two language subdirs: `gateway-adapters/ts/` (current TS package,
renamed home) and `gateway-adapters/go/` (new Go module with native
adapters for RTMP and WebRTC media plane). Each adopter writes in
their own language; no IPC tax. Path A (TS-only library + standalone
Go sidecar binary) and Path C (Go sidecar dialled by a TS-flavoured
IPC even from Go gateways) were both rejected — A adds an extra
container plus an IPC channel we'd have to spec; C makes Go gateways
pay an IPC tax for no benefit. Full rationale in §3.2 below; the
resolved-decision block lives at §13 Q1.

### 3.2. DECIDED rationale (kept for future readers).

1. **Reference `openai-gateway/` is TS** and uses
   `gateway-adapters` today
   (`openai-gateway/src/livepeer/payment.ts:73-101`). For
   `ws-realtime` (OpenAI Realtime API) and the control half of
   `session-control-plus-media`, TS-with-`ws` is fine; the reference
   gateway gets clean per-mode TS imports, no sidecar.
2. **Suite reality.** Plan 0011-followup's broker-side RTMP listener
   is Go (FFmpeg shells out, broker is Go). A Go gateway-side adapter
   reuses ~80% of the implementor's mental model. Suite analogue:
   `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/vtuber/relay.ts:1-40`
   is a TS WS relay (351 lines); the live-video equivalent at
   `livepeer-network-suite/livepeer-video-gateway/apps/api/src/service/live/liveSessionService.ts:1-100`
   delegates to a non-Node ingest path
   (`apps/playback-origin/README.md:1-7` is nginx, not Node) —
   confirming nobody ships production RTMP through Node.
3. **Image size.** TS package stays alpine-thin. Go module is
   imported only by RTMP/WebRTC consumers; the common
   (TS-gateway, no-RTMP) case pays nothing.
4. **Dev experience.** TS adopters get TS imports; Go adopters get
   Go imports. No forced IPC.
5. **Wire-compat testing.** Conformance runner already supports
   `--target=gateway` as a flag (rejected today at
   `livepeer-network-protocol/conformance/runner/cmd/livepeer-conformance/main.go:71`);
   one fixture set verifies both language halves against the broker.

Rest of this plan is written against the Path B lock. The Path A / C
rejection rationale is preserved at §13 Q1.

## 4. `ws-realtime@v0` adapter design

Lives in TS half: `gateway-adapters/ts/src/modes/ws-realtime.ts`.

### 4.1. Public surface

Mirrors the existing HTTP-family shape
(`gateway-adapters/src/modes/http-reqresp.ts:36-78`):

- `connect(endpoint, req)`. Inputs: `BrokerEndpoint`
  (`gateway-adapters/src/types.ts`), the five required `Livepeer-*`
  headers, and a pre-minted base64 `paymentBlob` (gateway calls
  `PayerDaemon.CreatePayment` itself; reference at
  `openai-gateway/src/livepeer/payment.ts:115-132`).
- Output: a `WebSocket`-shaped object exposing `send`, `onMessage`,
  `onClose`, `close`, plus bytes-in/out helpers for billing.

### 4.2. Wire behaviour

Per `livepeer-network-protocol/modes/ws-realtime.md:36-70`:

- HTTP `GET /v1/cap` upgrade with all five `Livepeer-*` headers
  (constants already at `gateway-adapters/src/headers.ts`).
- Broker validates payment **before** completing the upgrade; failure
  surfaces as a `LivepeerBrokerError`
  (`gateway-adapters/src/errors.ts`) — same shape as HTTP modes.
- After 101: bidirectional frame pump on the broker leg. The adapter
  does **not** own the customer leg; the gateway operator wires that
  to its app.

### 4.3. Payment lifecycle

- `PayerDaemon.CreatePayment` runs once at session-open *before* the
  adapter is invoked. Library-not-service pattern per
  `gateway-adapters/AGENTS.md:21-26`.
- During the session the adapter has nothing to do payment-wise. The
  broker-side ticker (plan 0015) computes interim debits server-side;
  the gateway-side ledger accrues from `Debit` events on the
  payer-daemon's session log (per
  `payment-daemon/docs/operator-runbook.md:36-52`).
- On close: adapter reads final `Livepeer-Work-Units` (mechanism in
  §13.6 open question) and surfaces it to the caller.

### 4.4. Heartbeat + failure

- RFC 6455 ping/pong handled by `ws`; broker auto-replies
  (`ws-realtime.md:84-88`). Adapter doesn't synthesize keepalives.
- Idle timeout (60 s default) is broker-side
  (`ws-realtime.md:144-149`); adapter mirrors it on the customer-leg
  knob via `LIVEPEER_WS_IDLE_TIMEOUT_S`.
- Broker-initiated close → adapter emits `onClose` with the close
  code; gateway closes the customer leg. `payment_invalid`
  surfaces as `LivepeerBrokerError`.

## 5. `rtmp-ingress-hls-egress@v0` adapter design

Lives in Go half: `gateway-adapters/go/modes/rtmpingresshlsegress/`.

### 5.1. Three-leg shape

The mode has three customer-facing surfaces, only one of which the
adapter owns end-to-end:

| Leg | Wire | Owner |
|---|---|---|
| Session-open | HTTP `POST /v1/cap` to broker | Adapter (HTTP-reqresp shape) |
| RTMP push | TCP/RTMP from customer's encoder | Adapter (RTMP listener) |
| HLS playback | HTTPS from customer's player | Broker directly (per spec); adapter just returns the URL |

Per `livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md:88-98`,
the broker hosts the HLS playlist + segments. The gateway-side adapter
**does not proxy HLS bytes** — it just returns the broker-issued
`hls_playback_url` to the customer.

### 5.2. Session-open

- Adapter calls broker `POST /v1/cap` with the five required
  `Livepeer-*` headers and a capability-defined JSON body, exactly
  like the HTTP-reqresp middleware does today
  (`gateway-adapters/src/modes/http-reqresp.ts:36-78`, but in Go).
- Broker returns 202 with `session_id`, `rtmp_ingest_url`,
  `hls_playback_url`, `control_url`, `expires_at`
  (`livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md:60-72`;
  the broker-side response is built at
  `capability-broker/internal/modes/rtmpingresshlsegress/driver.go:84-99`).
- Adapter returns those four URLs + the session token to the calling
  gateway. The customer obtains them out-of-band via the gateway's
  customer-facing API (the customer-facing API is the gateway's
  business, not in scope).

### 5.3. RTMP listener

- Adapter exposes an RTMP listener on `LIVEPEER_RTMP_LISTEN_ADDR`
  (default `:1935`). The customer pushes RTMP here using the
  `session_id` (or a derived token; see §13.5 open question) as the
  stream key.
- On accept: adapter parses the stream key, looks up the associated
  session in an in-memory session map, opens an outbound RTMP
  connection to the broker's `rtmp_ingest_url`, relays TCP frames
  bidirectionally.
- **No transcoding at the gateway.** The broker's pipeline (plan
  0011-followup) does the FFmpeg work
  (`livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md:144-156`).
- Library: `github.com/yutopp/go-rtmp` — pinned to align with plan
  0011-followup's broker-side choice. Pure-Go, MIT, suite-validated.
  See §13 Q8 for the alignment rationale.

### 5.4. Customer disconnect / failure

- Customer disconnects RTMP → adapter closes broker RTMP leg →
  broker drains HLS, finalizes manifest with `EXT-X-ENDLIST`, calls
  `PayeeDaemon.Reconcile` and `CloseSession`
  (`livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md:114-117`).
- Broker disconnects (e.g. balance-zero) → adapter forwards the close
  to the customer's RTMP encoder, marks the session done.
- `expires_at` reached without a push → broker auto-closes; adapter
  cleans up the listener slot.

### 5.5. HLS playback URL

- Adapter returns the broker-issued `hls_playback_url` to the gateway,
  which returns it to the customer. **The adapter does not proxy
  HLS bytes**; the customer's player connects directly to the
  broker's HLS sink (which may be an S3-compatible CDN — see the
  spec note at
  `livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md:92-96`).

## 6. `session-control-plus-media@v0` adapter design

Lives in both halves: control-WS in TS, WebRTC media plane in Go.
This is the most complex of the three.

### 6.1. Multi-leg lifecycle

The mode has up to four customer-facing surfaces:

| Leg | Wire | Owner |
|---|---|---|
| Session-open | HTTP `POST /v1/cap` | TS adapter (HTTP-reqresp shape) |
| Control plane | WebSocket at `control_url` | TS adapter |
| Media publish | Capability-defined (WebRTC, RTMP, trickle) | Go adapter (or broker-direct) |
| Media playback | Capability-defined | Broker-direct (per spec) |

The session-open response carries a capability-shaped `media`
descriptor (`livepeer-network-protocol/modes/session-control-plus-media.md:60-66`)
which the adapter passes through opaquely; **the protocol does not
interpret it**.

### 6.2. Session-open

- TS adapter (`gateway-adapters/ts/src/modes/session-control-plus-media.ts`)
  posts session-open identical to `http-reqresp` middleware shape.
- Receives 202 with `session_id`, `control_url`, `media`, `expires_at`
  (broker side at
  `capability-broker/internal/modes/sessioncontrolplusmedia/driver.go:70-85`).
- Returns the JSON body to the caller. The caller (gateway) routes
  `control_url` and `media` to the customer per the gateway's
  customer-facing API.

### 6.3. Control-WS

- The gateway MUST open `control_url` immediately after session-open;
  if not opened within `expires_at`, broker auto-closes
  (`session-control-plus-media.md:67-75`).
- TS adapter opens the control WS and exposes an event-emitter API:
  - Inbound (broker → gateway): `session.started`,
    `session.balance.low`, `session.balance.refilled`,
    `session.usage.tick`, `session.error`, `session.ended`.
  - Outbound (gateway → broker): `session.end`, plus capability-
    defined messages the adapter forwards opaquely.
- Frame format is capability-defined JSON (recommended);
  the adapter does not interpret payloads beyond the protocol-level
  event names.

### 6.4. Media plane (WebRTC)

When the offering's `media.schema` describes a WebRTC publish endpoint
(the canonical case for vtuber sessions), the Go half of the adapter
mediates SDP exchange:

- Customer's browser connects to the gateway's WebRTC endpoint
  (separate port, default UDP range from `LIVEPEER_WEBRTC_PORT_RANGE`).
- Adapter relays SDP offer/answer between customer and broker.
- Adapter is a SFU pass-through — no transcoding, no media-byte
  inspection.
- Library: `pion/webrtc` (Go); the only production-quality
  option, used today by `vtuber-worker-node` per the architecture
  note at
  `docs/design-docs/architecture-overview.md:198-202`.

When `media.schema` describes a non-WebRTC media plane (e.g.,
pytrickle URL + bearer auth), the adapter does **not** intermediate —
the customer connects directly to the broker-issued URL. The adapter
just hands the URL through.

### 6.5. Lifecycle

Any leg disconnect terminates the session:

- Control WS close → adapter closes media-plane SFU leg.
- Customer media disconnect → adapter sends `session.end` on the
  control WS.
- Broker emits `session.error` → adapter closes all legs, surfaces
  the error to the caller.

### 6.6. Heartbeat

- Control WS uses RFC 6455 ping/pong (TS `ws` library default).
- Idle timeout 60 s default, configurable via offering's
  `extra.idle_timeout_seconds`.
- WebRTC media leg uses ICE keepalive per pion defaults.

## 7. Component layout (Path B — locked)

The directory tree below is the canonical shape under the Path B lock
(§3.1, §13 Q1). `gateway-adapters/ts/` + `gateway-adapters/go/` are
the two halves; the Go module path is
`github.com/Cloud-SPE/livepeer-network-rewrite/gateway-adapters/go`
(§13 Q3 lock — sub-module of the monorepo, matching the
`payment-daemon/`, `capability-broker/`, `orch-coordinator/`,
`secure-orch-console/` precedents). Reorganize `gateway-adapters/` to
host both halves:

```
gateway-adapters/
├── AGENTS.md             # repo-root agent map (cross-language)
├── CLAUDE.md
├── README.md             # describes the two halves + when to use each
├── DESIGN.md             # cross-language design notes
├── ts/
│   ├── package.json      # @tztcloud/livepeer-gateway-middleware (existing)
│   ├── tsconfig.json
│   ├── Dockerfile
│   ├── Makefile
│   ├── src/
│   │   ├── headers.ts          # unchanged from today
│   │   ├── errors.ts
│   │   ├── types.ts
│   │   ├── modes/
│   │   │   ├── http-reqresp.ts        # existing
│   │   │   ├── http-stream.ts         # existing
│   │   │   ├── http-multipart.ts      # existing
│   │   │   ├── ws-realtime.ts         # NEW (this plan)
│   │   │   ├── session-control-plus-media.ts  # NEW (control-WS only)
│   │   │   └── index.ts
│   │   └── index.ts
│   └── test/                          # node:test, no jest/vitest
├── go/
│   ├── go.mod                         # NEW: github.com/Cloud-SPE/.../gateway-adapters/go
│   ├── Dockerfile
│   ├── Makefile
│   ├── headers/                       # canonical Livepeer-* constants (Go)
│   ├── errors/                        # LivepeerBrokerError equivalent
│   ├── modes/
│   │   ├── rtmpingresshlsegress/      # NEW: RTMP listener + relay
│   │   ├── sessioncontrolplusmedia/   # NEW: WebRTC SFU pass-through
│   │   └── wsrealtime/                # OPTIONAL: Go consumers also get a WS adapter
│   └── internal/                      # session map, listener wiring
└── docs/                              # design notes (cross-cutting)
```

Each half stays component-local-Docker-first per repo core belief #15
(reflected today at `gateway-adapters/AGENTS.md:39-41`). `make test`
in each half runs in its own image.

The TS half's `package.json:8-26` already exposes per-mode subpath
imports (e.g. `./modes/http-reqresp`); the new modes follow the same
pattern (`./modes/ws-realtime`, `./modes/session-control-plus-media`).

## 8. Configuration

Flag / env-var landscape, layered on the existing reference gateway's
patterns (`openai-gateway/src/config.ts`):

| Var | Half | Purpose | Default |
|---|---|---|---|
| `LIVEPEER_BROKER_URL` | TS + Go | Broker base URL (existing in OpenAI gateway) | required |
| `LIVEPEER_PAYER_DAEMON_SOCKET` | TS + Go | Unix socket of payer-daemon (existing) | `/var/run/livepeer/payer-daemon.sock` |
| `LIVEPEER_RTMP_LISTEN_ADDR` | Go | TCP addr for the RTMP listener | `:1935` |
| `LIVEPEER_WEBRTC_LISTEN_ADDR` | Go | TCP addr for WebRTC signalling | `:8443` |
| `LIVEPEER_WEBRTC_PORT_RANGE` | Go | UDP port range for WebRTC media | `40000-40099` |
| `LIVEPEER_WS_IDLE_TIMEOUT_S` | TS | Customer-leg idle timeout | `60` |
| `LIVEPEER_SESSION_REQUEST_TIMEOUT_S` | TS + Go | Session-open POST deadline | `30` |

Customer-facing auth (`Authorization` from the customer's bearer) is
not in this plan; it lives at the gateway operator's level per the
pattern pinned in plan 0009's reference impl.

## 9. Conformance fixtures

Gateway-targeted, not broker-targeted. The fixture runner already
distinguishes targets at
`livepeer-network-protocol/conformance/runner/cmd/livepeer-conformance/main.go:47-72`,
but rejects `--target=gateway` with "not yet implemented" today; this
plan wires it up.

Fixtures land at:

- `livepeer-network-protocol/conformance/fixtures/ws-realtime/gateway-*.yaml`
- `livepeer-network-protocol/conformance/fixtures/rtmp-ingress-hls-egress/gateway-*.yaml`
- `livepeer-network-protocol/conformance/fixtures/session-control-plus-media/gateway-*.yaml`

Per-mode happy-path fixture set (matches the broker-side fixtures at
`livepeer-network-protocol/modes/*.md`'s "Conformance" sections):

- **ws-realtime gateway:** customer connects to gateway WS endpoint;
  gateway upgrades to broker; echo bytes round-trip; clean close from
  either side.
- **rtmp gateway:** session-open returns valid URLs; RTMP push to
  gateway listener appears as bytes-in to mock broker within N
  seconds; HLS URL is the mock broker's URL (passthrough verified).
- **session-control gateway:** session-open returns control_url +
  media descriptor; control WS echo round-trip; SDP offer/answer
  round-trip on the WebRTC media leg; clean close on `session.end`.

The runner stubs the broker behind a `mock-broker` container so each
gateway-target fixture exercises the gateway adapter in isolation.
The mock-broker re-uses the in-process mock from
`livepeer-network-protocol/conformance/runner/internal/` (broker-side
fixtures use it today; gateway-target re-uses it as the upstream).

## 10. Test-harness changes

Concrete changes to
`livepeer-network-protocol/conformance/runner/`:

1. Remove the early-exit at `cmd/livepeer-conformance/main.go:70-72`
   ("not yet implemented"); replace with target-dispatch in
   `runner.Run`.
2. New per-mode driver path in `internal/runner/` for each non-HTTP
   mode that flips its role: instead of "I'm the gateway, I send
   requests to the broker URL," it becomes "I'm the customer, I send
   requests to the gateway URL, and a mock-broker is what the
   gateway-under-test points at."
3. The `Target` field on `runner.Config` (already exists; see
   `cmd/livepeer-conformance/main.go:93-98`) gates the dispatch.
4. The `--mock-addr` flag (existing) becomes the bind address the
   mock-broker container listens on.

No new flags; the existing gateway-target flag is wired, not added.

## 11. Operator runbook updates

`payment-daemon/docs/operator-runbook.md` is gateway-operator-facing
in its "sender mode" sections (lines 14-20, 41-52). This plan adds
gateway-adapters-specific operator concerns. **Lands at
`gateway-adapters/docs/operator-runbook.md` (component-local; matches
the per-component runbook pattern across the monorepo).** Concrete
content to add:

- **Per-mode port exposure:** RTMP TCP `:1935`, WebRTC signalling
  `:8443`, WebRTC media UDP `40000-40099`. Operator must open these
  in their firewall / cloud security group; the existing payer-daemon
  unix socket is process-local and unchanged.
- **Resource sizing:** rough numbers per concurrent session per mode
  (ws-realtime ≈ 1 MiB RAM + negligible CPU; rtmp ≈ 8 MiB RAM + 5%
  core for relay; session-control with WebRTC ≈ 12 MiB RAM + 8% core
  for SFU). Numbers are placeholder until empirical measurement
  lands.
- **Session-runner image management:** if the operator runs the
  session-control-plus-media adapter, the broker-side session-runner
  is the WebRTC publisher (per plan 0012-followup); the adapter does
  not pull session-runner images itself but the operator must
  understand the dependency.
## 12. Migration sequence

Estimated 6–9 commits under the Path B lock, depending on whether
the WebRTC half ships in the same plan or splits to a follow-up:

1. **Reorg `gateway-adapters/` to two halves** (`ts/` + `go/`).
   Mechanical move; no behaviour change. Lands the new Go module at
   `gateway-adapters/go/`. ~1 commit.
2. **`ws-realtime` TS adapter** (`ts/`). Most analogous to existing
   HTTP family; lowest risk; lights up the OpenAI Realtime API path
   on the reference gateway. ~1 commit.
3. **Session-control TS adapter** (`ts/`, control-WS only, no media
   plane). The control-WS half mirrors `ws-realtime` plus
   session-open. ~1 commit.
4. **Conformance runner gateway-target wiring.** Removes the
   "not yet implemented" guard; per-mode driver inversion for the
   three non-HTTP modes. ~1 commit.
5. **`rtmp-ingress-hls-egress` Go adapter** (`go/`). RTMP listener
   on `yutopp/go-rtmp`, broker relay. ~2 commits (listener + relay
   separate).
6. **WebRTC media plane Go adapter** (`go/`). SFU pass-through using
   `pion/webrtc`. ~1 commit.
7. **Operator runbook addendum** at `gateway-adapters/docs/operator-runbook.md`.
   Per-mode ports, resource sizing. ~1 commit.
8. **Reference `openai-gateway/` adopts `ws-realtime` adapter.**
   Demonstrates end-to-end. Lands as a separate commit (or even a
   separate plan) once plan 0008-followup's TS half is published.
   ~1 commit.

Plan 0011-followup (broker-side RTMP listener + FFmpeg + HLS sink)
and plan 0012-followup (broker-side control-WS + media-plane
provisioning) ship in parallel; this plan does not block on them
because the conformance fixtures use a mock-broker.

## 13. Resolved decisions

All eight open questions resolved on 2026-05-06. The implementing
agent works against these locks; rationale captured for future
readers.

### Q1. Language strategy

**DECIDED: Path B — TS + Go split inside `gateway-adapters/`.**
TS half (`gateway-adapters/ts/`) hosts the existing TS package plus
new `ws-realtime.ts` and `session-control-plus-media.ts` (control-WS)
modules. Go half (`gateway-adapters/go/`) hosts the new RTMP listener
and the WebRTC SFU pass-through. Each adopter writes in their own
language; no IPC tax. **Rejected:** Path A (TS-only library + Go
sidecar binary) — adds an extra container plus an IPC channel we'd
have to spec; **Path C** (TS-flavoured IPC even for Go gateways) —
pure tax for Go adopters and reuses none of the broker-side code.
Production libs available natively on each side: `ws` for TS;
`gorilla/websocket` (already used at
`capability-broker/internal/modes/wsrealtime/driver.go:21`) and
`pion/webrtc` for Go (§3.1, §3.2).

### Q2. Package naming

**DECIDED: single TS package `@tztcloud/livepeer-gateway-middleware`
+ subpath imports** for all six modes
(`./modes/http-reqresp`, `./modes/http-stream`,
`./modes/http-multipart`, `./modes/ws-realtime`,
`./modes/session-control-plus-media`, etc.). Matches the existing
shape at `gateway-adapters/package.json:8-26`. Independent
versioning per mode is gated on extraction (per `PLANS.md` §"Repo
shape"). Three-package and hybrid shapes both rejected — premature
split for the small number of in-tree consumers (§7).

### Q3. Go module naming

**DECIDED: `github.com/Cloud-SPE/livepeer-network-rewrite/gateway-adapters/go`**
— sub-module of the monorepo. Matches the existing
`payment-daemon/`, `capability-broker/`, `orch-coordinator/`,
`secure-orch-console/` Go-module precedents. Future extraction is a
mechanical move; sub-module first reduces deployment-time decisions
(§7).

### Q4. Operator-facing docs location

**DECIDED: component-local at `gateway-adapters/docs/operator-runbook.md`**
(new file). Matches the per-component runbook pattern across the
monorepo (capability-broker, payment-daemon, orch-coordinator,
secure-orch-console each have their own). Folding into
`payment-daemon/docs/operator-runbook.md` would couple deployment
surfaces unnecessarily (§11).

### Q5. Sequencing with plan 0011-followup

**DECIDED: parallel.** Conformance fixtures use the mock-broker
already in the runner (`livepeer-network-protocol/conformance/runner/internal/`);
0008-followup does not gate on 0011-followup landing. Wire-compat is
enforced by the fixture set, not by code-sharing (§9, §12).

### Q6. Final-debit reporting on `ws-realtime` close

**DECIDED: payer-daemon ledger read.** Adapter calls
`PayerDaemon.GetSessionDebits` (or equivalent gRPC method) after the
close event to surface the final `Livepeer-Work-Units` to the
gateway caller. Matches plan 0014's ledger ownership; no close-frame
extension; no control-plane event added. The
`Livepeer-Work-Units-Final` close-frame extension and the pre-close
control-plane event were both rejected — neither is needed once the
ledger is the source of truth (§4.3).

### Q7. WebRTC library

**DECIDED: pin `github.com/pion/webrtc`** — the only
production-grade option, and the same choice plan 0012-followup
locked at its Q4 plus the broker-side framing pinned in plan
0011-followup. `werift` (TS) and `node-webrtc` (TS, abandoned) both
rejected — not production-quality for an SFU (§6.4).

### Q8. RTMP library — REFRAMED

**DECIDED: `github.com/yutopp/go-rtmp`.** Plan recommended
`notedit/rtmp` prototype with `livego/livego` as fallback; locked
instead to `yutopp/go-rtmp` to align with plan 0011-followup's
broker-side library choice (its Q2 lock). Same library on both
sides of the wire ensures identical handshake handling, identical
edge-case behaviour, and code reuse / mental-model overlap with the
broker-side RTMP listener at `capability-broker/internal/media/rtmp/`.
`yutopp/go-rtmp` is pure-Go (no cgo), MIT-licensed, suite-validated.
The notedit/livego prototype phase is dropped entirely (§5.3).

## 14. Out of scope (defer list)

- Broker-side media planes — plan 0011-followup
  (RTMP listener + FFmpeg + HLS sink) and plan 0012-followup
  (control-WS lifecycle + media-plane provisioning).
- Gateway-side application logic (auth, billing, customer model,
  rate-limit, multi-tenancy) — per-deployment, not in this monorepo.
- DRM / token-gated playback URL signing — gateway-deployment
  concern.
- Federated / multi-instance gateway clusters (load balancing across
  multiple gateway instances pointing at the same broker pool) —
  ops/infra concern, not adapter code.
- Persona authoring tools, content moderation, Persona library — all
  vtuber-app concerns
  (`livepeer-network-suite/livepeer-vtuber-gateway/` territory).
- Chain integration on the payer-daemon — plan 0016.
- Warm-key handling on the payer-daemon — plan 0017.
- Interim-debit cadence on the broker — plan 0015.
- Coordinator UX (orch-coordinator) — plan 0018.
- Secure-orch / cold-key trust spine — plan 0019.
