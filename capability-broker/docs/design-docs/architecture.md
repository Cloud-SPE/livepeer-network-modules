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
    │   ├── config.go                     # top-level types (identity, stores, sinks)
    │   ├── offers.go                     # offers[] — the operator grammar
    │   ├── auth.go                       # scalar-or-mapping auth refs
    │   ├── parse.go                      # YAML parse (KnownFields) + Load
    │   └── validate.go                   # cross-field validation; extra{} grammar
    ├── server/                           # HTTP server + routing + middleware
    │   ├── server.go                     # http.Server wiring
    │   ├── routes.go                     # unpaid routes: registry, health, admin, attach
    │   ├── job_routes.go                 # paid-job/v1: POST /v1/job + idempotency
    │   ├── session_routes.go             # paid-session/v1: /v1/session/*
    │   ├── session_ws.go                 # paid-session §8 control socket (usage ticks, top-ups); no media relay
    │   ├── attach.go                     # runner attach over WS and QUIC (§2)
    │   ├── attach_quic.go                # the QUIC attach listener
    │   ├── offer_dispatch.go             # offer → eligible attached runner
    │   ├── offer_session.go              # pinned session backend refs
    │   ├── offer_health.go               # health from offers + attach tunnels
    │   ├── offers_admin.go               # /admin/v1/offers*
    │   ├── credential_admin.go           # /admin/v1/enroll, /admin/v1/credentials*
    │   ├── certification_admin.go        # /admin/v1/certification*
    │   ├── certification_usage.go        # session usage callback (§3.3)
    │   ├── capability_group.go           # published tuple → candidate runners
    │   ├── backend_capacity.go           # max_in_flight reservation
    │   ├── debitretry.go                 # durable retry for a debit that did not land
    │   ├── nonadmission.go               # signed evidence of a refused exchange
    │   ├── exchange_lookup.go            # GET /v1/exchange/{request_id}
    │   ├── runtime_admin.go              # GET/POST /admin/v1/runtime[/reload]
    │   ├── middleware/
    │   │   ├── headers.go                # Livepeer-* header parsing + validation
    │   │   ├── payment.go                # OpenSession / Debit / Reconcile / CloseSession
    │   │   ├── settlement.go             # settlement header emission
    │   │   ├── pendingdebit.go           # the outstanding-debit context slot
    │   │   ├── workid.go                 # work-id derivation
    │   │   ├── requestid.go              # Livepeer-Request-Id propagation
    │   │   └── recover.go                # panic recovery + Livepeer-Error: internal_error
    │   └── registry/
    │       ├── offerings.go              # GET /registry/offerings
    │       ├── offer_tuples.go           # frozen shape → advertised manifest tuple
    │       ├── health.go                 # GET /registry/health
    │       └── healthz.go                # GET /healthz
    ├── offers/                           # the offer state machine
    │   └── engine.go                     # match → certify → freeze → advertise
    ├── runnerattach/                     # attach document evaluation
    │   └── runnerattach.go               # §4 pipeline: parse, allowlist, per-capability verdict
    ├── runners/                          # attached hosts and their connections
    │   └── registry.go                   # attach/detach, dispatch lookup, snapshots
    ├── certification/                    # proving a matched runner can serve
    │   ├── certification.go              # run lifecycle, retention, outcome reporting
    │   ├── steps.go                      # readiness / request / usage / latency steps
    │   └── usagetap.go                   # per-run usage callback for session runners
    ├── credentialstore/                  # sealed bearer credentials for attach
    │   └── credentialstore.go            # enroll, rotate, revoke, controller sync
    ├── sessionengine/                    # paid-session/v1 authority
    │   ├── engine.go                     # open / topup / end / event / winddown
    │   ├── descriptor.go                 # descriptor minting + sealing
    │   ├── settlement.go                 # session settlement records
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
    ├── workerconn/                       # the attach tunnel wire (QUIC + WS frames)
    ├── settlement/                       # canonical payload + delegated signing
    ├── health/                           # health vocabulary + aggregation (no prober)
    ├── selection/                        # eligibility + weighting decisions
    ├── poolsnapshot/                     # pool-controller snapshot cache
    ├── livepeerheader/                   # the Livepeer-* header vocabulary and errors
    ├── poolreport/                       # backend-outcome emission to a pool controller
    ├── receipts/                         # work-receipt emission
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
| `GET /v1/exchange/{request_id}` | — | What happened to an exchange, keyed on the consumer's id. |
| `GET /v1/settlement/{id}` | — | The signed settlement for a job or session. |
| `POST /v1/non-admission/{request_id}` | — | Signed evidence that nothing was admitted. |
| `GET|POST /admin/v1/runtime[/reload]` | — | Private; gated by `admin_auth`. |
| `GET|PUT /admin/v1/offers`, `/admin/v1/runners`, `/admin/v1/credentials`, `/admin/v1/certification` | — | Private; the broker-admin contract. |
| `GET /internal/v1/worker/session` | — | Runner attach over WebSocket. The path keeps its old spelling because every minted bundle and running agent uses it. |
| `POST /internal/v1/certification/usage/{tap_id}` | — | Usage callback for a session under certification. |

The QUIC attach listener (`listen.attach_quic`, optional) carries the same
attach document and the same tunnel as the WebSocket path.

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
7. **Runner selection** — `(capability_id, offering_id)` → the offer's
   eligible attached runners → `selection.DecisionFor` over that eligibility
   and (when configured) the Pool snapshot → one runner, with a
   `capacity.max_in_flight` slot reserved.
8. **Forward** — `Livepeer-*` headers stripped, request forwarded down that
   runner's attach tunnel to the path the runner declared (bodies bounded at
   64 MiB).
9. **Extract** — the extractor frozen from the runner's declaration computes
   `actualUnits`;
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
