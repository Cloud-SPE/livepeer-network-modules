# payment-daemon operations

Observability assets for the livepeer-payment-daemon. The metric catalog
itself is documented in [`../operator-runbook.md` §8](../operator-runbook.md).

## Enabling metrics

Start the daemon with `--metrics-listen=:9092` (any host:port). Empty —
the default — disables the listener and uses a zero-overhead no-op
recorder; `/metrics` then returns 404 so a mispointed scrape fails loudly.

```
livepeer-payment-daemon --mode=receiver --metrics-listen=:9092 ...
```

## Files

- [`prometheus/alerts.yaml`](prometheus/alerts.yaml) — alert rules in two
  tiers (`page`, `ticket`). Validate with `promtool check rules`.
- [`grafana/livepeer-payment-daemon.json`](grafana/livepeer-payment-daemon.json)
  — importable dashboard (Grafana 10+). On import, pick your Prometheus
  datasource for `DS_PROMETHEUS`.

## Cross-component view

The broker's view of this daemon (RPC latency/error rate) lives in the
broker's own metrics (`livepeer_payment_client_*`) and alert rules
(`../../../capability-broker/docs/operations/prometheus/alerts.yaml`).
Join them for an end-to-end payment-path picture: broker client latency →
daemon gRPC latency → redemption outcomes. See cross-cutting plan
`docs/exec-plans/active/0038-payment-and-registry-metrics.md`.
