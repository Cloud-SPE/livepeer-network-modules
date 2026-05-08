# Grafana dashboard

Pre-built dashboard covering everything the daemon emits to Prometheus. Mirrors the metric catalog in [`docs/design-docs/observability.md`](../../design-docs/observability.md) and answers the 10 sample queries from that doc.

## Files

- [`livepeer-service-registry.json`](livepeer-service-registry.json) — dashboard definition (Grafana 10.0+, schema 39).

## Import

### UI (one-shot)

1. Grafana → **Dashboards → Import**.
2. Upload `livepeer-service-registry.json` (or paste the contents).
3. When prompted, pick your Prometheus datasource (the one scraping `:9091`).
4. Click **Import**. The dashboard's `uid` is `livepeer-service-registry` — re-imports update in place.

### API (CI / GitOps)

```sh
curl -s -X POST \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $GRAFANA_TOKEN" \
  -d @<(jq '{dashboard: ., overwrite: true}' docs/operations/grafana/livepeer-service-registry.json) \
  https://grafana.example.com/api/dashboards/db
```

### Provisioning (file-based)

Drop the JSON into your provisioned-dashboards folder, e.g. `/etc/grafana/provisioning/dashboards/livepeer/`, alongside a `dashboards.yaml`:

```yaml
apiVersion: 1
providers:
  - name: livepeer
    orgId: 1
    folder: Livepeer
    type: file
    options:
      path: /etc/grafana/provisioning/dashboards/livepeer
```

## Layout

7 collapsible rows, top-to-bottom in order of operational importance:

| Row | What it shows |
|---|---|
| **Overview** | Build version, uptime, total gRPC qps, gRPC error rate (color-coded thresholds) |
| **Resolver flow** | Resolutions/sec by mode + freshness, cache hit ratio, resolve p50/p95/p99 by mode, legacy fallbacks rate |
| **Manifest pipeline** (security-critical) | Verifications by outcome (`signature_mismatch` painted **red** with thick line so it grabs attention), fetches by outcome, fetch p99 latency + size, signature-verify p99 |
| **Chain provider** | Reads/sec by outcome (color-coded), read p50/p95/p99, time since last successful read (red ≥5min), writes/sec |
| **gRPC traffic** | Per-method qps, per-method p99, top-10 error registry codes (table), in-flight gauge |
| **Cache + audit + overlay** | Cache entries, overlay entries, audit events by kind (with `signature_invalid` highlighted), cache lookup result distribution, overlay drops |
| **Process + Go runtime** (collapsed by default) | Goroutines, heap-in-use, CPU seconds/sec, open FDs |

## Variables

The dashboard exposes three template variables at the top:

| Variable | Source | What it does |
|---|---|---|
| `datasource` | Prometheus picker | Switch dashboards across datasources without editing JSON. |
| `job` | `label_values(livepeer_registry_build_info, job)` | Filter to a specific Prometheus scrape job (default: All). |
| `instance` | `label_values(...{job=~"$job"}, instance)` | Filter to a specific daemon instance (default: All). |

Every panel's PromQL filters by `{job=~"$job"}`. Multi-instance and multi-job environments work without panel-by-panel surgery.

## Customizing

**Adjust thresholds.** The two threshold-driven panels are:
- *gRPC error rate* — green/yellow/red at `0`, `0.01`, `0.05` (1%, 5%). Edit per your SLO.
- *Time since last successful chain read* — green/yellow/red at `0`, `60s`, `300s`. Generous because chain reads are round-anchored (~19 hours apart on Arbitrum One); the seconds-since-last-success only ticks during round transitions or operator-triggered `Refresh()` calls.

**Add panels.** Every metric in [`observability.md`](../../design-docs/observability.md) has stable label values; copy any panel and tweak the PromQL.

**Drop panels.** Resolver-only deployments don't generate publisher metrics; the corresponding panels just show "No data". Either delete them or filter to your specific deployment via the `job` variable.

## Pairing with alerts

The dashboard displays metrics; it does NOT ship alert rules. The `observability.md` doc has a starter `groups:` block for Alertmanager — drop that into your `prometheus.yml` rules section and you'll get paged on:

- Signature-mismatch spikes
- Stale chain RPC (>5 min since success)
- p99 resolve latency >2s
- gRPC error rate >5%

## Compatibility

- Grafana 10.0+ (uses `schemaVersion: 39`, `timeseries` panel type).
- Prometheus 2.x or compatible (Mimir / Cortex / Thanos via `prometheus` datasource plugin).
- Daemon version ≥ v0.8.10 (the version that ships the `livepeer_registry_*` namespace).

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Panels show "No data" | The daemon isn't started with `--metrics-listen`, or Prometheus isn't scraping the host:port. Check `/metrics` directly with curl. |
| `Build version` stat shows nothing | Old daemon version (pre-v0.8.10) without `livepeer_registry_build_info`. |
| `signature_mismatch` rate panel always at 0 | This is the desired state. Configure an alert to page when it goes non-zero. |
| Cache hit ratio stuck at 0 | Daemon was just restarted; cache is empty. Should warm up within a few minutes of normal traffic. |
| `Top error registry codes` empty | Same — until something errors, the table has no rows. Filter labels include `registry_code != "_unset_"`. |
