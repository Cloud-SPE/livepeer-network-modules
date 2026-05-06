# Plan 0006 — Additional mode drivers (http-stream + http-multipart)

**Status:** completed (2026-05-06)
**Opened:** 2026-05-06
**Closed:** 2026-05-06
**Owner:** initial author + assistant

## Goal

Implement two additional interaction-mode drivers in both the
capability-broker and the conformance runner:

- `http-stream@v0` — request → SSE / chunked-response stream;
  `Livepeer-Work-Units` reported as an HTTP trailer.
- `http-multipart@v0` — multipart/form-data request body → regular HTTP
  response.

These two are minor deltas from `http-reqresp@v0`. Together they unblock
7 of the 8 OpenAI surfaces the first adopter needs (everything except
the WebSocket realtime mode).

## Why

The broker currently serves only `http-reqresp@v0`. The OpenAI-compat
gateway (first adopter, per plan 0009) needs streaming chat completions
(`http-stream`) and audio transcriptions / image edits (`http-multipart`).

Each is small enough that landing them together makes one cohesive
"the HTTP family is complete" milestone.

## Scope

In scope:

- Broker driver `capability-broker/internal/modes/httpstream/`.
- Broker driver `capability-broker/internal/modes/httpmultipart/`.
- Both registered in `capability-broker/internal/server/setup.go`.
- Payment + Metrics middleware updated so they read `Livepeer-Work-Units`
  from response trailers (set after body) when the regular header slot is
  empty.
- Runner driver `livepeer-network-protocol/conformance/runner/internal/modes/httpstream/`.
- Runner driver `livepeer-network-protocol/conformance/runner/internal/modes/httpmultipart/`.
- Both registered in the runner's `main.go`.
- One fixture per mode under
  `livepeer-network-protocol/conformance/fixtures/<mode>/happy-path.yaml`.
- `conformance/test-broker-config.yaml` extended with two test
  capabilities.
- `make test-compose` from `conformance/` exits 0 with all three fixtures
  passing.

Out of scope (future plans):

- `ws-realtime@v0` → plan 0010 (WebSocket lifecycle + interim-debit
  payment cadence; substantial).
- `rtmp-ingress-hls-egress@v0` → plan 0011 (RTMP listener + FFmpeg + HLS
  sink; substantial).
- `session-control-plus-media@v0` → plan 0012 (session-open + control
  WS + capability-defined media plane; substantial).

## Outcomes

- [x] Broker `httpstream` driver: forwards, sets `Trailer:
  Livepeer-Work-Units` before headers, writes body, emits trailer via the
  http.ResponseWriter.Header() map post-body.
- [x] Broker `httpmultipart` driver: forwards multipart body unchanged
  (Content-Type passthrough), regular response shape.
- [x] Payment middleware reads work-units trailer when regular header is
  absent (post-handler header-map fallback).
- [x] Metrics middleware reads work-units trailer when regular header is
  absent.
- [x] Runner `httpstream` driver: reads response body to EOF, asserts
  `Livepeer-Work-Units` value via `resp.Trailer` (Go's HTTP client deletes
  the `Trailer:` announcement from `resp.Header` after parsing — checking
  the value directly is the correct test).
- [x] Runner `httpmultipart` driver: sends multipart request, asserts
  response shape (same as http-reqresp).
- [x] `fixtures/http-stream/happy-path.yaml` passes.
- [x] `fixtures/http-multipart/happy-path.yaml` passes.
- [x] `make test-compose` from `conformance/` exits 0 with 3 passing
  fixtures.

## Done condition (met 2026-05-06)

```
$ make -C livepeer-network-protocol/conformance test-compose
...
runner-1  |   PASS: happy-path [http-multipart@v0]
runner-1  |   PASS: happy-path [http-reqresp@v0]
runner-1  |   PASS: happy-path [http-stream@v0]
runner-1  |
runner-1  | result: 3 passed, 0 failed
runner exited with code 0
```
