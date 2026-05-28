# capability-broker operations

Observability assets for the livepeer-capability-broker. The full metric
catalog is documented in [`../operator-runbook.md` §2.8](../operator-runbook.md).

## Enabling metrics

The broker serves Prometheus on a dedicated listener (separate from paid
traffic), on by default. Configure via the `metrics:` host-config key or
the `--metrics` flag (default `:9090`):

```
livepeer-capability-broker --metrics=:9090 ...
```

`/metrics` is intentionally NOT mounted on the paid listener — scrapes do
not traverse the payment middleware chain.

## Files

- [`grafana/livepeer-capability-broker.json`](grafana/livepeer-capability-broker.json)
  — importable dashboard (Grafana 10+): paid requests, payment-daemon
  client RPCs, backend selection, registry serving, pool snapshot, and
  metadata discovery. Pick your Prometheus datasource for `DS_PROMETHEUS`
  on import.
- [`prometheus/alerts.yaml`](prometheus/alerts.yaml) — alert rules for the
  payment-daemon client and registry-serving surfaces (plan 0038
  workstream A). Validate with `promtool check rules`.

## Cross-component view

The broker's `livepeer_payment_client_*` metrics pair with the
payment-daemon's own `livepeer_payment_*` catalog for an end-to-end
payment-path view (broker client latency → daemon gRPC latency →
redemption outcomes). See cross-cutting plan
`docs/exec-plans/active/0038-payment-and-registry-metrics.md`.
