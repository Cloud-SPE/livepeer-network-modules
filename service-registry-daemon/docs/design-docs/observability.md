---
title: Observability — Prometheus metrics catalog
status: verified
last-reviewed: 2026-04-28
---

# Observability — Prometheus metrics catalog

The daemon emits a comprehensive set of Prometheus metrics so every layer of the system is legible to a scrape-driven dashboard. This document is the durable specification: the source of truth for what's emitted, what each label can be, and which questions each metric is meant to answer.

## Quick start

```sh
livepeer-service-registry-daemon --mode=resolver \
  --metrics-listen=:9091 \
  --metrics-path=/metrics
```

Then add to your `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: livepeer-service-registry
    scrape_interval: 15s
    static_configs:
      - targets: ['registry-host:9091']
```

When `--metrics-listen` is unset, the daemon installs the [Noop recorder](../../internal/providers/metrics/noop.go) and never opens a TCP socket — zero runtime cost, no exposition surface.

## Cardinality philosophy

Every metric below carries label values from a closed enum. **No metric ever labels by `eth_address`, model name, URL, or any user-controlled string** — those would explode Prometheus memory at network scale (10k+ orchestrators × everything else).

Per-orchestrator drill-down lives in the **audit log**, queryable via the `Resolver.GetAuditLog(eth_address, since, limit)` gRPC. Two-tool workflow:

- **Metrics** → "what's the *shape* of failure?" (signature_mismatch rate jumped 5×)
- **Audit log** → "what's the *history* of failure for this address?" (47 events over the last hour)

The daemon also enforces a hard cardinality cap (`--metrics-max-series-per-metric`, default 10000). A future code change that introduces a high-cardinality label will hit the cap, log a warning, and silently drop the runaway label combinations rather than blowing up Prometheus.

## Build / health metrics

| Metric | Type | Labels | What it answers |
|---|---|---|---|
| `livepeer_registry_build_info` | Gauge (=1) | `version`, `mode`, `go_version` | "What version is running?" Stamp dashboards with this to detect mixed-version fleets. |
| `livepeer_registry_uptime_seconds` | Gauge | — | Crash-loop detection. |
| `go_*`, `process_*` | (built-in) | various | Standard Go runtime + process collectors — goroutine count, GC pause, FD usage, RSS. |

## Resolver flow

| Metric | Type | Labels | What it answers |
|---|---|---|---|
| `livepeer_registry_resolutions_total` | Counter | `mode`, `freshness` | Resolve volume + how often we're returning fresh data. |
| `livepeer_registry_resolve_duration_seconds` | Histogram (default buckets) | `mode`, `freshness` | End-to-end resolve latency per `(mode, freshness)`. |
| `livepeer_registry_legacy_fallbacks_total` | Counter | `reason` | "How many orchestrators are still legacy?" trend. |
| `livepeer_registry_overlay_dropped_nodes_total` | Counter | `reason` | Visibility into nodes the operator overlay rejected. |

### Label values

- `mode`: `well_known` `csv` `legacy` `static_overlay` `unknown`
- `freshness`: `fresh` `stale_recoverable` `stale_failing`
- `legacy_fallbacks_total reason`: `manifest_unavailable` `manifest_too_large` `parse_error` `signature_mismatch` `other`
- `overlay_dropped_nodes_total reason`: `signature_policy` `disabled` `tier_filter`

## Manifest pipeline

| Metric | Type | Labels | What it answers |
|---|---|---|---|
| `livepeer_registry_manifest_fetches_total` | Counter | `outcome` | Off-chain hosts going dark surface here first. |
| `livepeer_registry_manifest_fetch_duration_seconds` | Histogram | `outcome` | Slow-but-not-failing operator hosts. |
| `livepeer_registry_manifest_fetch_bytes` | Histogram | — | Capacity planning; spot operators bumping into the size cap. |
| `livepeer_registry_manifest_verifications_total` | Counter | `outcome` | **Critical**: `signature_mismatch ≠ 0` means MITM or operator misconfiguration. |
| `livepeer_registry_signature_verify_duration_seconds` | Histogram | — | secp256k1 recover is CPU-bound; sanity check. |
| `livepeer_registry_manifest_fetcher_last_success_timestamp_seconds` | Gauge | — | Alert when stale > N minutes. |

### Label values

- `manifest_fetches_total outcome`: `ok` `too_large` `http_error` `timeout`
- `manifest_verifications_total outcome`: `verified` `signature_mismatch` `parse_error` `expired` `eth_address_mismatch`

## Cache + audit

| Metric | Type | Labels | What it answers |
|---|---|---|---|
| `livepeer_registry_cache_lookups_total` | Counter | `result` | Cache hit ratio. |
| `livepeer_registry_cache_writes_total` | Counter | — | Refresh activity. |
| `livepeer_registry_cache_evictions_total` | Counter | `reason` | Distinguish "operator moved" from "we gave up". |
| `livepeer_registry_cache_entries` | Gauge | — | Cardinality watch — should track the active orchestrator set. |
| `livepeer_registry_audit_events_total` | Counter | `kind` | Long-tail security signal — `signature_invalid` rate is the big one. |

### Label values

- `cache_lookups_total result`: `hit_fresh` `hit_stale` `miss`
- `cache_evictions_total reason`: `chain_uri_changed` `forced` `max_stale`
- `audit_events_total kind`: see [`internal/types/audit.go`](../../internal/types/audit.go) — `manifest_fetched` `manifest_unchanged` `manifest_changed` `signature_invalid` `chain_uri_changed` `mode_changed` `fallback_used` `evicted` `publish_written` `publish_onchain`

## Chain provider

| Metric | Type | Labels | What it answers |
|---|---|---|---|
| `livepeer_registry_chain_reads_total` | Counter | `outcome` | RPC error rate against your chain endpoint. |
| `livepeer_registry_chain_writes_total` | Counter | `outcome` | Publisher-side; should be near zero in steady state. |
| `livepeer_registry_chain_read_duration_seconds` | Histogram | — | RPC tail latency — early warning for endpoint degradation. |
| `livepeer_registry_chain_last_success_timestamp_seconds` | Gauge | — | Alert when stale > N minutes. |

### Label values

- `chain_reads_total outcome`: `ok` `not_found` `unavailable`
- `chain_writes_total outcome`: `ok` `reverted` `not_implemented` `disallowed`

## Static overlay

| Metric | Type | Labels | What it answers |
|---|---|---|---|
| `livepeer_registry_overlay_reloads_total` | Counter | `outcome` | SIGHUP / fsnotify reload health. |
| `livepeer_registry_overlay_entries` | Gauge | — | Operator's allowlist size at-a-glance. |

### Label values

- `overlay_reloads_total outcome`: `ok` `parse_error` `io_error`

## Publisher

| Metric | Type | Labels | What it answers |
|---|---|---|---|
| `livepeer_registry_publisher_builds_total` | Counter | — | `BuildManifest` invocations. |
| `livepeer_registry_publisher_signs_total` | Counter | `outcome` | Sign rate; keystore-lock alarm. |
| `livepeer_registry_publisher_probe_workers_total` | Counter | `outcome` | Worker discovery health. |

### Label values

- `publisher_signs_total outcome`: `ok` `keystore_locked` `parse_error`
- `publisher_probe_workers_total outcome`: `ok` `http_error` `timeout`

## gRPC

| Metric | Type | Labels | What it answers |
|---|---|---|---|
| `livepeer_registry_grpc_requests_total` | Counter | `service`, `method`, `code`, `registry_code` | Full RPC accounting, including the stable `registry_error_code`. |
| `livepeer_registry_grpc_request_duration_seconds` | Histogram (default buckets) | `service`, `method` | Per-method latency. |
| `livepeer_registry_grpc_request_duration_seconds_fast` | Histogram (sub-ms buckets) | `service`, `method` | Sub-millisecond detail for the unix-socket fast path. |
| `livepeer_registry_grpc_in_flight_requests` | Gauge | `service`, `method` | Saturation indicator. |

The two histograms cover the full latency range. Default buckets work for resolves that hit chain (10ms–1s); fast buckets resolve the cache-hit fast path (50µs–10ms).

### Label values

- `service`: `Resolver` `Publisher`
- `method`: any RPC method name (e.g. `ResolveByAddress`, `BuildManifest`)
- `code`: any gRPC status code (`OK` `NotFound` `InvalidArgument` `Unavailable` …)
- `registry_code`: stable code from [`docs/product-specs/grpc-surface.md`](../product-specs/grpc-surface.md), or `_unset_` on success

## Sample PromQL queries

Every operator dashboard should be able to answer these in <30 seconds:

| Question | Query |
|---|---|
| Resolve QPS by mode | `sum by (mode) (rate(livepeer_registry_resolutions_total[1m]))` |
| Cache hit ratio | `sum(rate(livepeer_registry_cache_lookups_total{result=~"hit_.*"}[5m])) / sum(rate(livepeer_registry_cache_lookups_total[5m]))` |
| Resolve p99 latency by mode | `histogram_quantile(0.99, sum by (le, mode) (rate(livepeer_registry_resolve_duration_seconds_bucket[5m])))` |
| Manifest signature mismatches per minute | `sum(rate(livepeer_registry_manifest_verifications_total{outcome="signature_mismatch"}[1m]))` |
| Chain RPC error rate | `sum(rate(livepeer_registry_chain_reads_total{outcome!="ok"}[5m])) / sum(rate(livepeer_registry_chain_reads_total[5m]))` |
| Chain RPC stale (no success in last 5m) | `(time() - livepeer_registry_chain_last_success_timestamp_seconds) > 300` |
| Manifest fetch p99 size | `histogram_quantile(0.99, sum by (le) (rate(livepeer_registry_manifest_fetch_bytes_bucket[15m])))` |
| Top RPC error registry codes (last hour) | `topk(10, sum by (registry_code) (rate(livepeer_registry_grpc_requests_total{registry_code!="_unset_"}[1h])))` |
| Saturated gRPC method | `max by (service, method) (livepeer_registry_grpc_in_flight_requests) > 50` |
| Daemons running stale versions | `count by (version) (livepeer_registry_build_info)` |

## Pre-built Grafana dashboard

A complete dashboard covering every section above ships at [`docs/operations/grafana/livepeer-service-registry.json`](../operations/grafana/). 30 panels across 7 collapsible rows: overview, resolver flow, manifest pipeline, chain provider, gRPC traffic, cache + audit + overlay, and (collapsed by default) Go runtime + process. Threshold-driven panels color-code the operational red flags: `signature_mismatch` rate is painted red, chain-RPC staleness goes red after 5 minutes, gRPC error rate hits red at 5%.

UID is `livepeer-service-registry`, so re-imports update in place. Compatible with Grafana 10.0+ via `schemaVersion: 39`.

See [`docs/operations/grafana/README.md`](../operations/grafana/README.md) for import / API / GitOps instructions.

## Alert rules

A production-ready, drop-in alert-rules file ships at [`docs/operations/prometheus/alerts.yaml`](../operations/prometheus/alerts.yaml). Three severity tiers (`page` / `ticket` / `info`), twelve alerts covering security (signature mismatch), availability (chain stale, daemon down, error-rate spike), latency (chain p99, resolve p99), and quality-of-life informational signals (mixed versions, recent restart, overlay reload failures, cardinality cap proximity). See [`docs/operations/prometheus/README.md`](../operations/prometheus/README.md) for install / Prometheus Operator / tuning instructions.

The terser starter set below is retained for inline context — prefer the shipping `alerts.yaml`:

```yaml
groups:
  - name: livepeer-service-registry
    rules:
      - alert: SignatureMismatchSpike
        expr: rate(livepeer_registry_manifest_verifications_total{outcome="signature_mismatch"}[5m]) > 0.1
        for: 5m
        annotations:
          summary: "Manifest signatures are failing verification"

      - alert: ChainRPCStale
        expr: (time() - livepeer_registry_chain_last_success_timestamp_seconds) > 300
        for: 1m
        annotations:
          summary: "No successful chain reads in 5 minutes"

      - alert: ResolveLatencyHigh
        expr: histogram_quantile(0.99, sum by (le, mode) (rate(livepeer_registry_resolve_duration_seconds_bucket[5m]))) > 2
        for: 10m
        annotations:
          summary: "p99 resolve latency > 2s"

      - alert: GRPCErrorRateHigh
        expr: sum(rate(livepeer_registry_grpc_requests_total{code!="OK"}[5m])) / sum(rate(livepeer_registry_grpc_requests_total[5m])) > 0.05
        for: 5m
        annotations:
          summary: "gRPC error rate > 5%"
```

## Provider boundary

Per [`architecture.md`](architecture.md), no service or repo package may import `prometheus/client_golang` directly. All emissions go through [`internal/providers/metrics/recorder.go`](../../internal/providers/metrics/recorder.go). The Recorder interface is the swap point if we ever migrate to OpenTelemetry — service code never needs to change.

The TCP HTTP listener lives in [`internal/runtime/metrics/`](../../internal/runtime/metrics/), distinct from the gRPC unix-socket listener. Two listeners, two trust boundaries.

## What this does NOT cover

- Distributed tracing (OpenTelemetry / Jaeger). The Recorder interface could grow a `TraceSpan` method, but none in v1.
- Per-orchestrator drill-down — see audit log.
- Application-level alerts (Alertmanager rules) — operators ship their own.
- Long-term storage (Cortex / Thanos / Mimir) — this daemon emits metrics; storage is the operator's choice.
