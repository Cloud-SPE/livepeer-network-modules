# Plan 0003 — Capability broker reference implementation (Go, http-reqresp first)

**Status:** active
**Opened:** 2026-05-06
**Owner:** TBD

## Goal

Stand up `capability-broker/` — the Go reference implementation of a
workload-agnostic capability broker per the spec at
`livepeer-network-protocol/`.

First milestone: end-to-end working `http-reqresp` mode against a mocked
payment-daemon and the `response-jsonpath` extractor, dispatching real HTTP
requests to arbitrary backends declared in `host-config.yaml`.

This is plan 0002's gating done condition and the prerequisite for the
OpenAI-compat gateway migration.

## Why

Plan 0002 has shipped the wire spec. Without a reference implementation that
proves the spec is implementable end-to-end, the spec is just docs. The
reference broker:

- Validates the spec's wire shape under real HTTP traffic.
- Provides a known-good target the conformance runner is tested against.
- Becomes the production binary the OpenAI-compat gateway routes traffic to
  (per plan 0009 — gateway adoption).
- Establishes the Go package patterns the other modes follow as they're
  built.

## Scope

### In scope

- Scaffold: `Dockerfile`, `Makefile`, `go.mod`, `cmd/livepeer-capability-broker/main.go`.
- Component-local docs (`AGENTS.md`, `README.md`, `DESIGN.md`,
  `examples/host-config.example.yaml`, `docs/design-docs/architecture.md`).
- HTTP server with the `Livepeer-*` header pipeline (per
  `livepeer-network-protocol/headers/livepeer-headers.md`).
- `host-config.yaml` parser + validator.
- One mode driver: `http-reqresp@v0`.
- One extractor: `response-jsonpath` (most generic; runs against any JSON
  response).
- Mock `PaymentClient` (stubs validation/debit/reconcile/close; real
  payment-daemon integration is plan 0005).
- `GET /registry/offerings` returning the configured capability list.
- `GET /registry/health` returning live availability.
- `GET /healthz`, `GET /metrics` (Prometheus scrape).
- Backend forwarding: strip `Livepeer-*`, inject auth from `host-config.yaml`.
- Local Make-driven build + run + smoke test.

### Out of scope

- Real payment-daemon integration → plan 0005.
- Modes other than `http-reqresp` → plan 0006.
- Extractors other than `response-jsonpath` → plan 0007.
- TLS termination / reverse-proxy concerns (operator's job; broker binds
  `:8080`).
- `/registry/offerings` signing — orch-coordinator's job; broker only
  publishes the bare offerings list which gets signed downstream.

## Outcomes

- [x] `capability-broker/` scaffold landed.
- [x] `internal/config/` loads + validates `host-config.yaml` (KnownFields
  strict; eth-address regex; interaction-mode regex; auth scalar/mapping
  union via custom UnmarshalYAML).
- [x] `internal/server/` exposes the four registry endpoints + the paid
  `/v1/cap` route (POST and GET, the latter for ws-realtime upgrade).
- [x] `internal/server/middleware/` chain: Recover → RequestID → Headers
  → Payment, in that order. Headers validates the five required `Livepeer-*`
  request headers; major-version mismatch on Spec-Version → 505 +
  `spec_version_unsupported`; malformed Mode → 505 + `mode_unsupported`.
- [x] `internal/server/registry/` — offerings (manifest payload sans
  signature), health (currently-available capabilities, JSON header +
  body), healthz (process liveness).
- [ ] `internal/modes/httpreqresp/` driver dispatches per the mode spec.
- [ ] `internal/extractors/responsejsonpath/` computes work units.
- [ ] `internal/payment/mock.go` validates any `Livepeer-Payment` header,
  records debits/reconciles/closes locally for inspection, returns success.
- [ ] `internal/backend/` HTTP forwarder + auth injection.
- [ ] `internal/observability/` metrics (Prometheus) + structured logging.
- [ ] End-to-end smoke test: `docker run` the broker, send a curl to
  `/v1/cap`, get a forwarded response with `Livepeer-Work-Units` set.
- [ ] First conformance fixture exists and passes against the running broker
  (closes plan 0002).

## Done condition

A developer can:

1. `make build` to produce `tztcloud/livepeer-capability-broker:dev`.
2. `make run` to start the broker locally with the example
   `host-config.yaml`.
3. POST `/v1/cap` with the five required `Livepeer-*` headers and a JSON body.
4. Receive a response forwarded from the configured backend with
   `Livepeer-Work-Units` set per the offering's extractor.

The spec's first conformance fixture passes against this binary, closing
plan 0002.

## Follow-on plans (queued)

- Plan 0004 — conformance runner mode drivers (closes plan 0002 alongside 0003).
- Plan 0005 — real payment-daemon integration.
- Plan 0006 — additional mode drivers (`http-stream`, `http-multipart`,
  `ws-realtime`, `rtmp-ingress-hls-egress`, `session-control-plus-media`).
- Plan 0007 — additional extractors (`openai-usage`, `request-formula`,
  `bytes-counted`, `seconds-elapsed`, `ffmpeg-progress`).
- Plan 0008 — `gateway-adapters/` TS reference middleware.
- Plan 0009 — OpenAI-compat gateway migration brief execution.
