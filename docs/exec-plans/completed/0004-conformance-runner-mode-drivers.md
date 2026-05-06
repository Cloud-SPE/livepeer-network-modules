# Plan 0004 — Conformance runner mode drivers

**Status:** completed (2026-05-06)
**Opened:** 2026-05-06
**Closed:** 2026-05-06
**Owner:** initial author + assistant

## Goal

Make `tztcloud/livepeer-conformance` actually execute fixtures.

v0.1 scope: the http-reqresp@v0 mode driver in the runner, fixture loading
from `fixtures/<mode>/*.yaml`, an in-process mock backend, and a compose-
driven test target that exercises the capability-broker reference impl
end-to-end.

This closes plan 0002's last open outcome.

## Why

Plan 0003 shipped a working broker that satisfies the spec's wire shape;
plan 0002 shipped the spec docs + a fixture documenting the canonical
happy-path. Without a runner that actually loads fixtures and drives a
target, "conformance" is just a docstring. Closing 0004 turns the
fixture file into an executed test the broker passes.

## Scope

In scope:

- `internal/fixtures/` — YAML loader; struct definitions matching the
  format documented at `livepeer-network-protocol/conformance/fixtures/README.md`.
- `internal/mockbackend/` — small in-process HTTP server the broker
  forwards to during a test; programmable per-fixture (status / headers /
  body) and records inbound calls for backend-assertions.
- `internal/modes/httpreqresp/` — driver that runs an http-reqresp@v0
  fixture against a target broker. Asserts response shape + backend
  assertions.
- `internal/runner/` — main test loop. Loads fixtures, filters by `--modes`,
  runs each through its mode driver, accumulates pass/fail.
- `internal/report/` — pass/fail formatting; exit code derived from the
  total fail count.
- Updated `cmd/livepeer-conformance/main.go` wires everything.
- Updated `conformance/compose.yaml` runs the runner against the
  capability-broker reference image with a test-suite host-config.yaml that
  declares the test capabilities pointing at the runner's mock backend.
- New `conformance/test-broker-config.yaml` — the broker config used by the
  compose stack.
- `make test-compose` from `conformance/` runs the full stack and exits
  non-zero on any failure.

Out of scope:

- ws-realtime, http-stream, http-multipart, rtmp-ingress-hls-egress,
  session-control-plus-media drivers (plan 0006).
- Cross-implementation conformance — runner only runs against the Go
  reference broker for now.
- Stress / soak / benchmark surfaces.
- Pretty HTML reports / JUnit XML output.

## Outcomes

- [x] `internal/fixtures.LoadAll(dir)` walks `<dir>` recursively, returns
  parsed `Fixture` values.
- [x] `internal/mockbackend.Server` exposes `Set(Response)`,
  `Reset()`, `LastCall()` for per-fixture programming + inspection.
- [x] `internal/modes/httpreqresp.Driver` runs the http-reqresp@v0 driver
  against a target URL, asserting response status / headers /
  Livepeer-Work-Units / body-passthrough + backend assertions.
- [x] `internal/report.Report` accumulates pass/fail with Print()
  formatter; exit code = 1 if any failure.
- [x] `cmd/livepeer-conformance/main.go` wires the above; replaced the
  v0.1 stub.
- [x] `conformance/test-broker-config.yaml` declares the test capabilities
  pointing at the in-Docker mock backend URL.
- [x] `conformance/compose.yaml` runs runner + broker on a private
  network, runner sends test traffic, exit code propagated.
- [x] `make test-compose` from `conformance/` runs the full stack and
  passes the http-reqresp/happy-path fixture against the live broker.

## Done condition (met 2026-05-06)

```
$ make test-compose
...
runner-1  |   PASS: happy-path [http-reqresp@v0]
runner-1  |
runner-1  | result: 1 passed, 0 failed
runner-1 exited with code 0
```

Plan 0002 closes on the same gesture.

## Follow-on plans

- Plan 0006 — additional mode drivers in the runner (one per spec mode).
- Plan 0007 — additional extractors (runner doesn't directly need these,
  but new fixtures in plan 0006 will exercise them).
- Plan 0009 — gateway adoption (when the OpenAI-compat gateway runs against
  the same fixtures with `--target=gateway`).
