# Architecture

Planned package layout, request lifecycle, and dispatch flow for the broker.

> Status: skeleton (v0.1). Code lands incrementally per
> [`../../../docs/exec-plans/completed/0003-capability-broker.md`](../../../docs/exec-plans/completed/0003-capability-broker.md).

## Planned package layout

```
capability-broker/
├── cmd/livepeer-capability-broker/
│   └── main.go                           # entry point; flag parsing; wires everything
└── internal/
    ├── config/                           # host-config.yaml loader + validator
    │   ├── config.go                     # types
    │   ├── parse.go                      # YAML parse + structural validate
    │   └── validate.go                   # cross-field validation (mode references valid extractor, etc.)
    ├── server/                           # HTTP server + routing + middleware
    │   ├── server.go                     # http.Server wiring
    │   ├── routes.go                     # route table
    │   ├── middleware/
    │   │   ├── headers.go                # Livepeer-* header parsing + validation
    │   │   ├── payment.go                # OpenSession / Debit / Reconcile / CloseSession lifecycle
    │   │   ├── requestid.go              # Livepeer-Request-Id propagation
    │   │   └── recover.go                # panic recovery + Livepeer-Error: internal_error
    │   └── registry/
    │       ├── offerings.go              # GET /registry/offerings
    │       ├── health.go                 # GET /registry/health
    │       └── healthz.go                # GET /healthz
    ├── modes/                            # one driver per interaction mode
    │   ├── types.go                      # Driver interface
    │   ├── registry.go                   # mode-name → Driver lookup
    │   └── httpreqresp/
    │       └── driver.go                 # POST /v1/cap; forward; report Livepeer-Work-Units
    ├── extractors/                       # work-unit extractor library
    │   ├── types.go                      # Extractor interface; extractor-name → impl
    │   ├── registry.go
    │   └── responsejsonpath/
    │       └── extractor.go
    ├── payment/                          # payment-daemon client
    │   ├── client.go                     # PaymentClient interface
    │   ├── grpc.go                       # real gRPC client (plan 0005)
    │   └── mock.go                       # v0.1 stub: validates any header, records ops
    ├── backend/                          # outbound forwarding to declared backends
    │   ├── descriptor.go                 # types
    │   ├── http.go                       # HTTP forwarder (used by httpreqresp)
    │   └── auth.go                       # backend-auth injection (vault://, env://, bearer)
    └── observability/
        ├── metrics.go                    # Prometheus collector registration
        └── logger.go                     # structured logging with request-id correlation
```

## Request lifecycle (`http-reqresp` happy path)

1. **Inbound request** → `POST /v1/cap`.
2. **Middleware: recover** — catches panics; produces 500 +
   `Livepeer-Error: internal_error`.
3. **Middleware: requestid** — extracts/generates `Livepeer-Request-Id`;
   attaches to context for logging and response echo.
4. **Middleware: headers** — validates the five required `Livepeer-*` request
   headers per
   [`../../../livepeer-network-protocol/headers/livepeer-headers.md`](../../../livepeer-network-protocol/headers/livepeer-headers.md).
   Rejects with the appropriate `Livepeer-Error` code on any mismatch.
5. **Middleware: payment** — decodes `Livepeer-Payment` envelope; calls
   `PaymentClient.OpenSession` + `ProcessPayment` + `DebitBalance(estimate)`.
   Rejects with 401 + `Livepeer-Error: payment_invalid` on failure.
6. **Route lookup** — `(capability_id, offering_id)` → `internal/config`
   capability entry → backend descriptor + extractor descriptor.
7. **Mode driver** — `httpreqresp.Driver.Serve(ctx, req, capability)`:
   - Strips `Livepeer-*` headers.
   - Calls `backend/auth.Apply(req, capability.backend.auth)` to inject
     declared backend auth.
   - Issues outbound request via `backend/http.Forward`.
   - Awaits response.
   - Calls extractor on the response → `actualUnits`.
   - Sets `Livepeer-Work-Units: <actualUnits>` on the outbound response to
     the gateway.
   - Returns the backend body unchanged.
8. **Middleware: payment (post-Serve)** — calls
   `PaymentClient.Reconcile(actualUnits)` + `CloseSession`.
9. **Response sent.**

## Module boundaries

- `config/` knows the YAML grammar; doesn't know HTTP, payment, or modes.
- `server/middleware/` knows HTTP and the headers spec; doesn't know about
  specific modes or extractors.
- `modes/` knows mode-specific wire shapes; doesn't know about specific
  extractors (calls them via the `extractors.Extractor` interface).
- `extractors/` knows recipes; doesn't know about modes or backends.
- `payment/` knows the gRPC contract (or its mock); doesn't know modes,
  extractors, or backends.
- `backend/` knows how to forward over HTTP (and later over WebSocket / RTMP);
  doesn't know about Livepeer protocol headers (those are stripped before
  this layer is reached).

This boundary lets us:

- Add a new mode without touching extractors, payment, or backend.
- Add a new extractor without touching modes or backends.
- Swap the mock payment client for the real one without touching anything else.

## Concurrency model

- One HTTP server, one goroutine per request (Go's default).
- Mode drivers may spawn additional goroutines for streaming reads / writes;
  cancel on request context cancellation.
- Payment-daemon client connection-pooled; no per-request connection.

## Configuration reload

v0.1: no hot reload. Operator edits `host-config.yaml`, restarts the
container.

A `SIGHUP` reload handler can be added in a follow-up; not in scope for v0.1.

## Tests

- Unit tests per package (`*_test.go`) — Go's default conventions.
- Integration tests under `internal/server/server_test.go` use a
  `httptest.NewServer` mock backend.
- End-to-end conformance via the
  [conformance image](../../../livepeer-network-protocol/conformance/) — the
  authoritative grader.
