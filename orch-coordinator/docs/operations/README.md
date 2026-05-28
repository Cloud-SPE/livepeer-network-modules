# orch-coordinator operations

Observability assets for the livepeer-orch-coordinator (plan 0018 §10).

## Enabling metrics

The coordinator serves Prometheus on a dedicated listener, **on by
default** at `:9091`. Override with `--metrics-listen`:

```
livepeer-orch-coordinator --metrics-listen=:9091 ...
```

All series use the `orch_coordinator_*` namespace. Labels are bounded:
`broker` comes from the static coordinator-config, and `outcome` / `kind`
are pinned enums.

## Files

- [`grafana/livepeer-orch-coordinator.json`](grafana/livepeer-orch-coordinator.json)
  — importable dashboard (Grafana 10+): publish health (known/healthy
  brokers, manifest age, capability tuples, drift), pipeline throughput
  (scrape → build → sign → publish), and stage latencies. Pick your
  Prometheus datasource for `DS_PROMETHEUS` on import.

## Key signals

- `orch_coordinator_published_manifest_age_seconds` — if this climbs, the
  publish loop is stalled or every scrape/build/sign is failing. `-1`
  means nothing has been published yet.
- `orch_coordinator_brokers_healthy` vs `orch_coordinator_known_brokers` —
  a persistent gap means one or more brokers are unreachable.
- `orch_coordinator_candidate_drift_count{kind}` — pending divergence
  between the freshest scraped candidate and the currently-published
  manifest; non-zero for long stretches means publishes aren't keeping up.

Alert rules are not yet checked in for this component; the manifest-age,
broker-health, and publish-outcome series above are the natural starting
points.
