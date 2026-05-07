# Operator runbook — gateway-adapters

Operator-facing reference for running a gateway that imports the
`gateway-adapters/` middleware. Audience: gateway operators (the people
who run an `openai-gateway`-style or vtuber-style HTTP service that
points at a livepeer capability-broker upstream).

For payment-daemon operations (escrow economics, redemption queue,
hot-wallet split, etc.), see
[`../../payment-daemon/docs/operator-runbook.md`](../../payment-daemon/docs/operator-runbook.md);
this runbook scopes to gateway-adapter-specific concerns.

---

## 1. Two halves, six modes

`gateway-adapters/` ships two language halves. Which modes you run
determines which half (or both) you import:

| Mode | Half | When to use |
|---|---|---|
| `http-reqresp@v0` | `ts/` | Single request → single response. The OpenAI HTTP API path. |
| `http-stream@v0` | `ts/` | Single request → SSE / chunked response (chat completions, etc.). |
| `http-multipart@v0` | `ts/` | Multipart upload → single response. |
| `ws-realtime@v0` | `ts/` | Long-lived bidirectional WebSocket. The OpenAI Realtime API path. |
| `session-control-plus-media@v0` (control-WS) | `ts/` | Session-open + control-plane WebSocket for vtuber-like workloads. |
| `rtmp-ingress-hls-egress@v0` | `go/` | Customer pushes RTMP, broker hosts HLS. |
| `session-control-plus-media@v0` (WebRTC media plane) | `go/` | WebRTC SFU pass-through alongside the control WS. |

The HTTP-family modes ship as a Node-process library (no extra
container). The Go half ships as an importable Go module — operators
who run Node-only gateways and don't need RTMP / WebRTC don't pay any
cost for the Go half.

---

## 2. Per-mode port exposure

Each mode opens a customer-facing surface. The operator MUST open the
matching port in their firewall / cloud security group.

| Surface | Default | Variable | Mode |
|---|---|---|---|
| HTTP / HTTPS API | gateway-defined (e.g. `:8000`) | gateway-defined | All HTTP family + ws-realtime + control-WS |
| RTMP TCP listener | `:1935` | `LIVEPEER_RTMP_LISTEN_ADDR` | rtmp-ingress-hls-egress |
| WebRTC signalling TCP | `:8443` | `LIVEPEER_WEBRTC_LISTEN_ADDR` | session-control-plus-media (media plane) |
| WebRTC media UDP range | `40000-40099` | `LIVEPEER_WEBRTC_PORT_RANGE` | session-control-plus-media (media plane) |

The payer-daemon unix socket
(`/var/run/livepeer/payer-daemon.sock`) is process-local; nothing to
expose.

### NAT / firewall guidance for WebRTC

The gateway-side WebRTC adapter is a SFU pass-through — it does NOT
terminate media. The customer's browser ultimately sends media to the
broker's media ports, not the gateway's. ICE candidates in the
broker's answer SDP point at the broker; the gateway is on the SDP
relay path only.

That said, customer browsers may reject SDP answers when the gateway's
own signalling endpoint is unreachable (TLS termination needs valid
cert chain). Operator checklist:

1. **TLS:** terminate TLS at the gateway's reverse proxy. Browsers
   require `wss://` for non-localhost WebSocket signalling.
2. **STUN / TURN:** if the broker's media ports sit behind a NAT, the
   broker's operator runs STUN/TURN on the broker side; the
   gateway-side adapter does not need a STUN/TURN server of its own
   (the gateway is not in the media path). If the customer sits
   behind a strict NAT, the broker's TURN allocation handles
   traversal.
3. **UDP range:** `LIVEPEER_WEBRTC_PORT_RANGE` defaults to
   `40000-40099`. Open this range in your firewall as UDP. The pion
   settings engine binds within this range for any session-runner
   media that DOES transit the gateway (rare but possible for
   capabilities that explicitly proxy media; check the capability's
   `media.schema`).

### NAT / firewall guidance for RTMP

RTMP is plain TCP on `:1935`. Open inbound from the public internet
(or from your customers' IP allowlist). Outbound to the broker's
RTMP ingest URL (also TCP/1935 by default) needs to clear your
gateway's egress rules.

---

## 3. Resource sizing per concurrent session

Rough numbers from a 4-vCPU / 8 GiB Linux host. **Placeholder until
empirical measurement on production traffic; bump these once you have
your own.**

| Mode | RAM/session | CPU/session | Notes |
|---|---|---|---|
| http-reqresp / http-stream / http-multipart | < 1 MiB | negligible | Per-request only; no session state. |
| ws-realtime | ~1 MiB | negligible (~0.1% core) | Long-lived; mostly RAM for the `ws` socket buffer. |
| rtmp-ingress-hls-egress | ~8 MiB | ~5% core | RTMP relay forwards bytes; no transcoding. |
| session-control-plus-media (control-WS only) | ~1 MiB | negligible | Same shape as ws-realtime. |
| session-control-plus-media (with WebRTC pass-through) | ~12 MiB | ~8% core | pion SettingsEngine + per-PC accounting; media bytes do not transit. |

Worst-case capacity: 100 concurrent vtuber-style sessions on the host
above ≈ 1.2 GiB RAM, ~80% of one core. The dominant cost is RTMP
relay + WebRTC SFU pass-through; HTTP-family modes are essentially
free. Plan for 2× headroom.

---

## 4. Configuration surface

| Variable | Half | Required | Default | What it does |
|---|---|---|---|---|
| `LIVEPEER_BROKER_URL` | TS + Go | yes | — | Upstream broker URL (e.g. `http://broker:8080`). |
| `LIVEPEER_PAYER_DAEMON_SOCKET` | TS + Go | yes | `/var/run/livepeer/payer-daemon.sock` | Payer-daemon gRPC socket. |
| `LIVEPEER_RTMP_LISTEN_ADDR` | Go | only with rtmp-ingress | `:1935` | TCP bind for RTMP listener. |
| `LIVEPEER_WEBRTC_LISTEN_ADDR` | Go | only with session-control media | `:8443` | TCP bind for WebRTC signalling. |
| `LIVEPEER_WEBRTC_PORT_RANGE` | Go | only with session-control media | `40000-40099` | UDP port range for WebRTC media. |
| `LIVEPEER_WS_IDLE_TIMEOUT_S` | TS | optional | `60` | Customer-leg idle timeout for ws-realtime. |
| `LIVEPEER_SESSION_REQUEST_TIMEOUT_S` | TS + Go | optional | `30` | Session-open POST deadline. |

Customer-facing auth (e.g. an `Authorization: Bearer` from your
tenant's bearer token) is gateway-application-level, not adapter-level.
The adapters do not interpret customer credentials; the gateway
operator must wire its own auth in front of the adapters.

---

## 5. Final-debit reporting on session close

For long-lived modes (`ws-realtime`,
`session-control-plus-media`) the broker accrues interim debits via
`PayeeDaemon.DebitBalance` calls (broker-side). On clean session close
the gateway-adapter consults the payer-daemon's session ledger via
`PayerDaemon.GetSessionDebits` to surface a final `Livepeer-Work-Units`
count to the gateway caller.

The actual debit-pushdown from the broker-side ticker is plan-0015
territory; until then the daemon's embedded
`UnimplementedPayerDaemonServer` returns `UNIMPLEMENTED` and the
adapter falls back to a `0`-valued result so the gateway remains
usable today.

When plan 0015 lands, the gateway gets accurate per-session totals
without any code change on the gateway side.

---

## 6. Session-runner image dependency note

Operators running the `session-control-plus-media` adapter with a
broker-side session-runner (the typical vtuber deployment) should be
aware that the broker-side session-runner is what actually publishes
the media bytes. The gateway-adapter does not pull session-runner
images itself; that's a broker-operator concern. But the
broker-operator's session-runner image pin is what determines which
WebRTC capabilities work end-to-end, so coordinate version bumps
between the two operator roles.

---

## 7. Sidecar nuance

`gateway-adapters/` is a **library**, not a service (per repo core
belief #15 — services ship as Docker images, libraries ship as
packages). The TS half is published as
`@tztcloud/livepeer-gateway-middleware` on npm; the Go half is an
importable Go module under `github.com/Cloud-SPE/...`.

Operators do NOT run gateway-adapters as a sidecar container. The
gateway process imports the adapter directly. The Dockerfile under
each half (`ts/Dockerfile`, `go/Dockerfile`) is the test/build image
only — never deploy these to production.

The payer-daemon, by contrast, IS a sidecar (it ships as
`tztcloud/livepeer-payment-daemon`). The gateway dials the payer-daemon
over a unix socket; the adapter library uses the same socket when the
gateway provides a `debitsClient` for `PayerDaemon.GetSessionDebits`.

---

## 8. Common failure modes

| Symptom | Likely cause | Fix |
|---|---|---|
| `LivepeerBrokerError: payment_invalid` on session open | Payer-daemon escrow ran dry | Top up escrow per the payment-daemon runbook §Economics. |
| `LivepeerBrokerError: capability_not_served` | Broker's host-config doesn't include the capability the gateway requests | Confirm broker host-config matches the gateway's capability/offering. |
| RTMP listener bind fails with "address already in use" | Another process owns `:1935` | Set `LIVEPEER_RTMP_LISTEN_ADDR` to a free port; update customer-facing URLs accordingly. |
| WebRTC signalling 503 | Broker's WebRTC media plane unreachable from the gateway | Confirm `media.webrtc_signal_url` from the broker's session-open response is reachable. |
| `closed.workUnits` always 0 | Payer-daemon `GetSessionDebits` returns UNIMPLEMENTED | Expected today (plan 0015 wires the debit-pushdown). Gateway can ignore until then. |

---

## 9. Observability

The adapters emit structured `log` lines under the `gateway-adapters/`
prefix; consume them via your gateway's existing logging pipeline. The
recommended gateway-level metrics (mirrored from the broker side at
`livepeer-network-protocol/modes/*.md`'s "Observability" sections):

- `livepeer_mode_session_open_total{mode,outcome}` — counter.
- `livepeer_mode_session_duration_seconds{mode}` — histogram.
- `livepeer_mode_session_balance_low_events_total{mode}` — counter.
- `livepeer_mode_session_close_reason_total{mode,reason}` — counter
  (`graceful`, `broker_initiated`, `customer_initiated`,
  `payment_invalid`, `idle_timeout`).

Wire these in your gateway's metric registry; the adapter library
exposes the events but does not own the metric registration (the
gateway operator picks the metrics library — Prometheus, OpenTelemetry,
etc.).
