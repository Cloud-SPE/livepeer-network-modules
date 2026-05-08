---
id: 0003
slug: prometheus-metrics
title: Prometheus metrics — verbose, cardinality-capped, opt-in TCP listener
status: completed
owner: agent
opened: 2026-04-25
closed: 2026-04-25
---

## Goal

Make every layer of the daemon legible to a Prometheus dashboard. An operator should be able to answer "is the chain RPC degrading?", "is anyone publishing manifests with bad signatures?", "is my cache earning its keep?" in <30 seconds without grepping logs. Aggregate metrics live in Prometheus; per-orchestrator drill-down stays in the existing audit log + `Resolver.GetAuditLog` RPC.

## Non-goals

- No OpenTelemetry pull/push. Prometheus only for v1; the Recorder interface makes OTLP a swap if anyone asks.
- No per-eth-address labels. Cardinality discipline is a hard rule, not a guideline.
- No alerting rules. We ship metrics; consumers ship rules.

## Decisions (fixed by user prompt 2026-04-25)

1. **Listener type**: TCP, opt-in via `--metrics-listen`. Defaults off (no listener, Recorder is Noop).
2. **Default port**: `:9091`. Override via flag.
3. **Histogram buckets**: Prometheus default for everything *except* `grpc_request_duration_seconds`, which gets a finer-grained variant for sub-millisecond unix-socket latency.
4. **Cardinality cap**: 10,000 series per metric, configurable via `--metrics-max-series-per-metric`. Wrapper logs a warning and drops new label combinations beyond the cap (existing combinations keep updating).
5. **Backend**: Prometheus pull.

## Approach

- [x] go.mod: add `github.com/prometheus/client_golang`. Pin a stable version.
- [x] `internal/providers/metrics/` — Recorder interface (counter / histogram / gauge ops + `Handler() http.Handler`), Prometheus impl with cardinality-cap wrapper, Noop impl, tests asserting on registry contents.
- [x] `internal/runtime/metrics/` — TCP HTTP listener serving `/metrics` (delegates to `recorder.Handler()`) and `/healthz`. Graceful shutdown wired to ctx like gRPC listener.
- [x] `internal/runtime/grpc/listener.go` — extend logging interceptor to also call `recorder.IncGRPCRequest` + `ObserveGRPC` + maintain `grpc_in_flight` gauge. Defaults to a Noop recorder when none injected.
- [x] Inject Recorder into:
  - `internal/service/resolver` (resolutions, verifications, fallbacks, cache lookups, overlay drops)
  - `internal/service/publisher` (builds, signs, probes)
  - `internal/repo/manifestcache` (cache writes + entries gauge)
  - `internal/repo/audit` (events by kind)
  - `internal/providers/chain` (reads, writes, last-success)
  - `internal/providers/manifestfetcher` (fetch outcome + bytes + last-success)
  - `internal/providers/verifier` (verify outcome + duration)
- [x] CLI flags: `--metrics-listen`, `--metrics-path`, `--metrics-max-series-per-metric` (default 10000). Wire into `cmd/.../providers.go` + `run.go`.
- [x] Lifecycle: `runtime/lifecycle.Run` accepts an optional metrics listener, runs it alongside the gRPC listener, GracefulStop on ctx cancel.
- [x] `docs/design-docs/observability.md` — metric catalog (durable spec), label value enums, cardinality rules, sample PromQL queries, scrape config example.
- [x] Update `docs/operations/running-the-daemon.md` with the metrics flags + a sample `prometheus.yml` snippet.
- [x] Update `README.md` Highlights, `AGENTS.md` "Where to look for X", `docs/design-docs/index.md`.
- [x] Tests: per-package ≥75% coverage maintained; new packages exceed 80%.
- [x] Smoke test: boot the binary with `--metrics-listen=:19091`, `curl /metrics`, confirm catalog renders.
- [x] Commit (1) implementation, (2) move plan to completed/ + close.

## Decisions log

### 2026-04-25 — Recorder is the single cross-cutting metrics interface
All metric emissions go through `internal/providers/metrics/recorder.Recorder`. No package outside `internal/providers/metrics/` may import `prometheus/client_golang`. depguard rule added.

### 2026-04-25 — Cardinality cap is enforced in-process, not via Prometheus federation
The cap wraps each `*MetricVec` and tracks distinct label tuples with a `sync.Map`. New label combinations beyond the cap are logged once-per-violation-block and dropped. Existing combinations continue to update. This is a guardrail against accidental high-cardinality labels in future PRs, not a quota system.

### 2026-04-25 — gRPC histogram has a separate finer-grained variant
The default Prometheus buckets (5ms, 10ms, ..., 10s) cluster too tightly at the low end for unix-socket gRPC where cache hits return in <1ms. We add a parallel `grpc_request_duration_seconds_fast` histogram with sub-ms buckets specifically for that one metric. Cost: ~5 extra series.

### 2026-04-25 — No labels on the cache_entries gauge
The `cache_entries` gauge is a single number. We deliberately don't break it down by mode or freshness — that's Resolver.ListKnown territory.

## Open questions

None — all five decisions are fixed.

## Artifacts produced

- `internal/providers/metrics/` — Recorder interface, Prometheus impl, Noop impl, Counter test helper, full tests
- `internal/runtime/metrics/` — TCP HTTP listener with `/metrics` + `/healthz`, graceful shutdown, tests
- `internal/runtime/grpc/listener.go` — `metricsInterceptor` (recovers + deadline + metrics + logging chain), full-method splitting helper
- Recorder threaded through: `service/{resolver,publisher}`, `repo/{manifestcache,audit}`, `providers/{chain,manifestfetcher}` via `WithMetrics(...)` decorators
- New CLI flags: `--metrics-listen`, `--metrics-path`, `--metrics-max-series-per-metric` (default 10000)
- `docs/design-docs/observability.md` — durable metric catalog with label enums, cardinality philosophy, 10 sample PromQL queries, sample alert rules
- `docs/operations/running-the-daemon.md` — metrics flag table + sample `prometheus.yml` scrape config
- `compose.yaml` + `.env.example` — exposes :9091 by default with `METRICS_PORT` override
- Per-package coverage range: 77.1%–100% (lowest: service/publisher; highest: providers/clock + service/legacy)
- End-to-end smoke verified: `curl /metrics` against a running `--mode=resolver --dev` daemon shows the full namespace including `livepeer_registry_grpc_requests_total{service="Resolver",method="ResolveByAddress",code="NotFound",registry_code="not_found"}`
