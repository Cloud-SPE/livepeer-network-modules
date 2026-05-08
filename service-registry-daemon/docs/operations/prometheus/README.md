# Prometheus alert rules

Production-ready alert rules pairing the [metrics catalog](../../design-docs/observability.md) with the [Grafana dashboard](../grafana/). Three severity tiers, twelve alerts.

## Files

- [`alerts.yaml`](alerts.yaml) — three rule groups (`critical`, `warning`, `info`), Alertmanager-compatible.

## Severity model

| Severity | Routing convention | When to use |
|---|---|---|
| `page` | wake-someone-up route in Alertmanager | Security incident, data loss risk, availability impact |
| `ticket` | low-priority queue (Slack channel, email) | Investigate during business hours; not user-impacting yet |
| `info` | dashboard banner only | Operationally interesting, not actionable |

The `severity` label rides on every alert; route on it in your Alertmanager `route:` block.

## What each tier covers

### Critical (page)
- **`RegistrySignatureMismatchSpike`** — manifests failing signature verification at >0.05/s for 5m. MITM, DNS hijack, or operator key-rotation gone wrong.
- **`RegistryChainRPCStale`** — no successful chain reads in 5+ minutes.
- **`RegistryDaemonDown`** — Prometheus can't scrape the daemon.
- **`RegistryGRPCErrorRateHigh`** — >5% non-OK gRPC responses for 10m.

### Warning (ticket)
- **`RegistryChainRPCSlow`** — chain read p99 >1.5s for 15m.
- **`RegistryResolveLatencyHigh`** — resolve p99 >2s for 15m.
- **`RegistryManifestFetcherStale`** — fetcher trying but every fetch failing for 10m.
- **`RegistryLegacyFallbacksHigh`** — legacy synthesis rate sustained >0.5/s for 30m.
- **`RegistryCacheHitRatioLow`** — hit ratio <50% for 30m (with non-trivial traffic).
- **`RegistryGRPCInFlightHigh`** — >100 in-flight on a single method for 5m.

### Info (dashboard banner)
- **`RegistryCardinalityCapHit`** — gRPC metric approaching its `--metrics-max-series-per-metric` cap.
- **`RegistryDaemonRecentlyRestarted`** — uptime <5min.
- **`RegistryOverlayReloadFailed`** — static-overlay reload failed; previous valid overlay still active.
- **`RegistryMixedDaemonVersions`** — multiple `version` labels in `build_info` for 30+ minutes (rolling deploy stuck).

## Install

### File-based (single host)

1. Copy the file:
   ```sh
   sudo install -m 0644 docs/operations/prometheus/alerts.yaml \
     /etc/prometheus/rules.d/livepeer-service-registry.yaml
   ```
2. In `prometheus.yml`:
   ```yaml
   rule_files:
     - rules.d/livepeer-service-registry.yaml
   ```
3. Reload:
   ```sh
   curl -X POST http://prometheus:9090/-/reload
   # or: sudo systemctl reload prometheus
   ```

### Validate before deploy

```sh
promtool check rules docs/operations/prometheus/alerts.yaml
```

The CI for this repo runs the same check (queued under `tech-debt-tracker.md` `prometheus-rules-ci-check`).

### Kubernetes (kube-prometheus / Prometheus Operator)

Wrap the file in a `PrometheusRule` CR:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: livepeer-service-registry
  namespace: monitoring
  labels:
    prometheus: kube-prometheus
spec:
  # paste the contents of alerts.yaml under `spec` here, or use a
  # ConfigMap and reference it from your Prometheus CR.
```

A more sustainable option: keep `alerts.yaml` as the source of truth and template it into `PrometheusRule` via `kubectl create configmap --from-file=…` + `helm template …` in your deployment pipeline.

## Tuning

These thresholds are **opinionated defaults**. Calibrate before going live:

| Threshold | What to consider |
|---|---|
| `RegistrySignatureMismatchSpike` 0.05/s | If your network has many orchestrators with intermittently-broken manifests, lower to 0.01/s and bump the `for:` to 30m. |
| `RegistryChainRPCStale` 5m | Should be ≥ `--round-poll-interval × 2` (default 1m × 2 = 2min). Dashboards page on 5min — generous because round-anchored refreshes only fire on round transitions, ~19 hours apart on Arbitrum One. |
| `RegistryGRPCErrorRateHigh` 5% / 10m | If you have a small number of orchestrators, 5% is one orchestrator failing — consider relaxing. |
| `RegistryResolveLatencyHigh` p99 2s / 15m | Cache misses with chain RPC + manifest fetch can legitimately take >1s. 2s is a soft SLO. |
| `RegistryCacheHitRatioLow` 50% / 30m | Traffic-pattern dependent. Sparse traffic → lower threshold. |

The runbook for each alert lives in the alert's `description:` field — Alertmanager renders it inline. If you maintain a wiki, add a `runbook_url:` annotation pointing at it.

## Pairing with notifications

The alerts ship with `severity` labels but no `team` or `service_owner` labels. Add those at receive-time in your Alertmanager `route:` tree:

```yaml
route:
  receiver: default
  group_by: [alertname, severity]
  routes:
    - matchers: [service="livepeer-service-registry", severity="page"]
      receiver: pagerduty
      group_wait: 30s
    - matchers: [service="livepeer-service-registry", severity="ticket"]
      receiver: slack-livepeer
    - matchers: [service="livepeer-service-registry", severity="info"]
      # no receiver → silenced
```

## What's NOT alerted on

- **Per-orchestrator failure** — that's audit-log territory (`Resolver.GetAuditLog`). Metrics aggregate; alerts page on aggregate trends.
- **ServiceRegistry write failures** — those now belong to `protocol-daemon` alerting and operator workflows, not this daemon.
- **Boltdb file size** — the cache is small enough that disk-fill alerts belong to your generic `node_exporter` rules, not here.

## Compatibility

- Prometheus 2.x (LTS or latest).
- Alertmanager 0.27+ for the `matchers` syntax in the README's routing example. Older Alertmanager uses `match:` / `match_re:`.
- All PromQL is standard; no recording rules required.
