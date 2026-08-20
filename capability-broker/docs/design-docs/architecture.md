# Architecture

Package layout, request lifecycle, and dispatch flow for the broker.

> Status: current as of the v1 protocol rewrite (2026-08). The v0
> seven-mode interaction taxonomy — `internal/modes/`, `internal/media/`,
> `internal/controlws/`, and the `POST /v1/cap` dispatch surface — was
> removed and replaced by two protocol engines.

## Package layout

```
capability-broker/
├── cmd/livepeer-capability-broker/
│   └── main.go                           # entry point; flag parsing; wires everything
└── internal/
    ├── config/                           # host-config.yaml loader + validator
    │   ├── config.go                     # types (protocol + job/session axes)
    │   ├── parse.go                      # YAML parse (KnownFields) + Load
    │   └── validate.go                   # cross-field validation; per-protocol rules
    ├── server/                           # HTTP server + routing + middleware
    │   ├── server.go                     # http.Server wiring
    │   ├── routes.go                     # unpaid routes: registry, health, admin, worker
    │   ├── job_routes.go                 # paid-job/v1: POST /v1/job + idempotency
    │   ├── session_routes.go             # paid-session/v1: /v1/session/*
    │   ├── session_ws.go                 # inband-ws session attachment
    │   ├── capability_group.go           # published tuple → candidate backends
    │   ├── backend_capacity.go           # max_in_flight reservation
    │   ├── offering_metadata.go          # periodic backend metadata discovery
    │   ├── runtime_admin.go              # GET/POST /admin/v1/runtime[/reload]
    │   ├── worker_quic.go                # connected-worker QUIC listener
    │   ├── middleware/
    │   │   ├── headers.go                # Livepeer-* header parsing + validation
    │   │   ├── payment.go                # OpenSession / Debit / Reconcile / CloseSession
    │   │   ├── settlement.go             # settlement header emission
    │   │   ├── workid.go                 # work-id derivation
    │   │   ├── requestid.go              # Livepeer-Request-Id propagation
    │   │   └── recover.go                # panic recovery + Livepeer-Error: internal_error
    │   └── registry/
    │       ├── offerings.go              # GET /registry/offerings
    │       ├── health.go                 # GET /registry/health
    │       └── healthz.go                # GET /healthz
    ├── sessionengine/                    # paid-session/v1 authority
    │   ├── engine.go                     # open / topup / end / event / winddown
    │   ├── descriptor.go                 # descriptor minting + sealing
    │   ├── describe.go                   # runner self-description reconciliation
    │   └── runnerclient.go               # runner create/status/terminate calls
    ├── sessionstore/                     # durable bbolt state
    │   ├── sessionstore.go               # session records, debit sequence counters
    │   ├── jobs.go                       # paid-job idempotency records
    │   └── keyfile.go                    # descriptor sealing key
    ├── extractors/                       # work-unit extractor library (paid-job only)
    │   ├── types.go                      # Extractor / LiveCounter interfaces
    │   ├── registry.go                   # extractor-name → factory
    │   └── <one package per extractor>/  # response-jsonpath, openai-usage, ...
    ├── payment/                          # payment-daemon client
    │   ├── client.go                     # PaymentClient interface
    │   ├── grpc.go                       # real gRPC client
    │   ├── metered.go                    # metrics decorator
    │   └── mock.go / mock_persist.go     # dev stub, optionally durable
    ├── backend/                          # outbound forwarding to declared backends
    │   ├── descriptor.go                 # types
    │   ├── http.go                       # HTTP forwarder
    │   ├── headers.go                    # Livepeer-* stripping
    │   └── secret.go                     # backend-auth injection (env://, bearer)
    ├── workerconn/                       # connected-worker sessions (QUIC + WS)
    ├── health/                           # per-backend probes
    ├── selection/                        # eligibility + weighting decisions
    ├── poolsnapshot/                     # pool-controller snapshot cache
    ├── poolreport/ + receipts/           # Pool outcome + work-receipt emission
    └── observability/
        ├── metrics.go                    # Prometheus collector registration
        ├── protocol_metrics.go           # livepeer_protocol_* families
        └── logger.go                     # structured logging with request-id correlation
```

## HTTP surface

Paid listener (`--listen`, default `:8080`):

| Route | Protocol | Notes |
|---|---|---|
| `POST /v1/job` | `paid-job/v1` | Transport negotiated per-request: `unary`, `stream` (SSE), `multipart`. |
| `POST /v1/session` | `paid-session/v1` | Opens a session; mints the runtime descriptor. |
| `GET /v1/session/{id}` | | Status. |
| `POST /v1/session/{id}/topup` | | Refill (subject to the `refill` axis). |
| `POST /v1/session/{id}/end` | | Gateway-initiated winddown. |
| `POST /v1/session/{id}/events` | | Runner-posted usage/heartbeat events. |
| `GET /v1/session/{id}/ws` | | Inband-WS attachment. |
| `POST /v1/payment/ticket-params` | — | Unpaid ticket-params proxy. |
| `GET /registry/offerings` | — | Unpaid capability inventory. |
| `GET /registry/health` | — | Unpaid live availability. |
| `GET /healthz` | — | Process health. |
| `GET|POST /admin/v1/runtime[/reload]` | — | Private; gated by `admin_auth`. |
| `GET /admin/v1/worker-sessions`, `POST .../{backend_id}/kill` | — | Private. |
| `GET /internal/v1/worker/session` | — | Connected-worker WebSocket fallback. |

Metrics listener (`--metrics`, default `:9090`) serves `GET /metrics` and
`GET /healthz`. `/metrics` is deliberately not mounted on the paid listener
so scrapes never traverse the payment middleware chain.

## Required request headers

Every paid request carries `Livepeer-Capability`, `Livepeer-Offering`,
`Livepeer-Payment`, `Livepeer-Protocol` (e.g. `paid-job/v1`), and
`Livepeer-Request-Id`. `Livepeer-Protocol` replaced the pre-v1
`Livepeer-Mode` + `Livepeer-Spec-Version` pair. `Livepeer-Request-Id` is
the idempotency key and is never synthesized server-side — a request
without one is a 400.

## Request lifecycle (`paid-job/v1` unary happy path)

1. **Inbound request** → `POST /v1/job`.
2. **Middleware: recover** — catches panics; produces 500 +
   `Livepeer-Error: internal_error`.
3. **Middleware: requestid** — reads `Livepeer-Request-Id`; attaches to
   context for logging and echoes it on the response.
4. **Middleware: headers** — validates the five required `Livepeer-*`
   request headers per
   [`../../../livepeer-network-protocol/headers/livepeer-headers.md`](../../../livepeer-network-protocol/headers/livepeer-headers.md).
   A malformed protocol tag is 505 + `protocol_unsupported`.
5. **Idempotency layer** (`job_routes.go`) — wraps the payment chain, so a
   retry converges on the recorded outcome without re-executing the backend
   or re-processing the payment envelope. It also refuses a protocol that
   is not `paid-job/v1` (505), an unserved `(capability, offering)` tuple
   (404 `capability_not_served`), and a transport the offering does not
   declare (400 `protocol_transport_unsupported`) — all before any payment
   side effect.
6. **Middleware: payment** — decodes the `Livepeer-Payment` envelope; calls
   `PaymentClient.OpenSession` + `ProcessPayment` + `DebitBalance(estimate)`.
   Rejects with 401 + `Livepeer-Error: payment_invalid` on failure.
7. **Backend selection** — `(capability_id, offering_id)` → capability group
   → `selection.DecisionFor` over probe health and (when configured) the
   Pool snapshot → one eligible backend, with a `max_in_flight` slot
   reserved.
8. **Forward** — `Livepeer-*` headers stripped, declared backend auth
   injected, request forwarded via `backend/http.Forward` (bodies bounded at
   64 MiB).
9. **Extract** — the offering's declared extractor computes `actualUnits`;
   the broker sets `Livepeer-Work-Units` (header for `unary`/`multipart`,
   HTTP trailer for `stream`) and `Livepeer-Work-Unit`.
10. **Middleware: payment (post-serve)** — `Reconcile(actualUnits)` +
    `CloseSession`; the idempotency record is finalized.
11. **Response sent.**

## Request lifecycle (`paid-session/v1`)

`POST /v1/session` opens a session: the engine calls the runner's
`create_path`, mints a runtime descriptor (private parts sealed with the
`session_store` sealing key), and returns control coordinates derived from
`external_base_url` — never from inbound request headers. Usage then flows
the other way: the runner posts cumulative claims to
`POST /v1/session/{id}/events`, which the engine converts into interim
debits. A session winds down through one idempotent path (terminate runner
→ close payment → release capacity → record `close_reason`), whether the
trigger is a gateway `end`, lease expiry, heartbeat loss, insufficient
balance, or runner failure. See `docs/operator-runbook.md` §3.

## Module boundaries

- `config/` knows the YAML grammar; doesn't know HTTP, payment, or protocols.
- `server/middleware/` knows HTTP and the headers spec; doesn't know about
  specific protocols or extractors.
- `server/job_routes.go` and `sessionengine/` know their protocol's wire
  shape; they reach extractors and backends only through interfaces.
- `extractors/` knows recipes; doesn't know about protocols or backends.
  Extractors are a paid-job concept only.
- `payment/` knows the gRPC contract (or its mock); doesn't know protocols,
  extractors, or backends.
- `backend/` knows how to forward over HTTP; doesn't know about Livepeer
  protocol headers (those are stripped before this layer is reached).

This boundary lets us:

- Add a transport to `paid-job/v1` without touching extractors, payment, or
  backend.
- Add a new extractor without touching either protocol engine.
- Swap the mock payment client for the real one without touching anything else.

## Concurrency model

- One HTTP server, one goroutine per request (Go's default).
- Streaming transports and session attachments may spawn additional
  goroutines for reads / writes; cancel on request context cancellation.
- The session engine runs lease/heartbeat enforcement on its own timers,
  independent of any inbound request.
- Payment-daemon client connection-pooled; no per-request connection.

## Configuration reload

Current behavior: the broker exposes private runtime reload/status endpoints:

- `GET /admin/v1/runtime`
- `POST /admin/v1/runtime/reload`

Production flow is:

1. stage a new `host-config.yaml` at the broker's configured path
2. trigger `POST /admin/v1/runtime/reload`
3. confirm broker-reported `loaded_revision`, `last_reload_attempt_id`, and
   reload status

If reload fails, the previous runtime stays active. Removing a paid-session
capability with active sessions leaves those sessions unroutable — they wind
down via heartbeat enforcement. Drain first.

## Tests

- Unit tests per package (`*_test.go`) — Go's default conventions.
- Integration tests under `internal/server/*_test.go` use a
  `httptest.NewServer` mock backend.
- Docker-only smoke matrix: [`../../scripts/smoke.sh`](../../scripts/smoke.sh)
  exercises the live `POST /v1/job` surface end to end.
- End-to-end conformance via the
  [conformance image](../../../livepeer-network-protocol/conformance/) — the
  authoritative grader.
