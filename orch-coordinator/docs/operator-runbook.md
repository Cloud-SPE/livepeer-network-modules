# orch-coordinator operator runbook

> **Status:** v0.1 scaffold draft. The runbook fills in as later commits land.

## Boot

```
livepeer-orch-coordinator \
  --config=/etc/livepeer/orch-coordinator.yaml \
  --data-dir=/var/lib/livepeer/orch-coordinator \
  --listen=:8080 \
  --public-listen=:8081 \
  --metrics-listen=:9091
```

The three listeners are intentionally separate:

- `--listen` — operator UX (web UI + JSON API + signed-manifest upload).
  Bind to a LAN-private interface; this is reachable to operators on the
  same LAN.
- `--public-listen` — resolver-facing
  `/.well-known/livepeer-registry.json`. Bind to the public-facing
  interface; only that one path is routed.
- `--metrics-listen` — Prometheus.

## Dev mode

`--dev` boots the coordinator with synthetic in-memory broker fixtures
and a loud `=== DEV MODE ===` banner. Use it to smoke-test the binary
without standing up real brokers. Production deployments must NOT pass
`--dev`.

## Configuration

`coordinator-config.yaml`:

```yaml
identity:
  orch_eth_address: "0x..."
brokers:
  - name: broker-a
    base_url: http://10.0.0.5:8080
publish:
  manifest_ttl: 24h
```

The orch eth address is the on-chain `ServiceRegistry` (or
`AIServiceRegistry`) entry the cold key on secure-orch will sign for.
The broker list is static for v0.1; service discovery is a follow-up.

## Failure modes

The remainder of this runbook fills in as later commits land:

- Candidate-build duplicate-key conflict (commit 2).
- Drift detection + roster reading (commit 3).
- Signed-manifest upload + verification rejection codes (commit 4).
- Resolver-endpoint health + Prometheus surface (commit 5).
- Web UI navigation + signed-manifest upload form (commit 6).
