# AGENTS.md

This is `orch-coordinator/` — the operator's LAN-side process that scrapes
capability-broker `/registry/offerings`, builds candidate manifests, hosts the
candidate for the operator to hand-carry to `secure-orch-console`, receives the
cold-key-signed manifest back, atomic-swap publishes at
`/.well-known/livepeer-registry.json`, and exposes the capability-as-roster UX.

Component-local agent map. The repo-root [`../AGENTS.md`](../AGENTS.md) is the
cross-cutting map; this file scopes to coordinator-specific guidance. The
design lives in [`../docs/exec-plans/completed/0018-orch-coordinator-design.md`](../docs/exec-plans/completed/0018-orch-coordinator-design.md).

## Hard rule

> **The coordinator never holds a signing key.**

Cold key on `secure-orch` is the only signer. The coordinator receives signed
manifests via HTTP POST and verifies them; if the signature recovers anything
other than the configured `eth_address`, the upload is rejected. There is no
warm-key path here. Cite: plan 0018 §1 + core belief #4.

## Two listeners, two postures

- `--listen=:8080` — operator UX (web UI + JSON API + signed-manifest upload).
  LAN-bound by intent; the coordinator runs on the operator's LAN and the
  operator hits this from a browser on the same LAN. No `ssh -L` (unlike
  secure-orch).
- `--public-listen=:8081` — resolver-facing. Serves **only**
  `GET /.well-known/livepeer-registry.json`. Every other path is 404. This
  is defense-in-depth: a routing bug elsewhere in the codebase cannot
  accidentally expose admin or operator-UX routes via the public listener.
- `--metrics-listen=:9091` — Prometheus.

## Operating principles

Inherited from the repo root, plus:

- **Idempotent candidate builds.** Same broker offerings + same scrape window
  → byte-identical manifest bytes. `issued_at` is the scrape window end, not
  wall-clock. Capability tuples sorted by `(capability_id, offering_id,
  worker_url)` before serialization.
- **Uniqueness key for tuple identity.** `(capability_id, offering_id, extra,
  constraints)` quadruple. `worker_url` is the endpoint, not identity.
  Identical key + different prices → hard-fail loud. Identical key + identical
  price + different `worker_url` → emit one tuple, lex-min URL wins, second URL
  in metadata sidecar. Different `extra` / `constraints` → distinct tuples.
- **Last-good fallback on soft scrape failure.** Broker unreachable / 5xx /
  timeout → keep its last-good entries flagged `freshness=stale_failing`.
  Hard fail (malformed JSON, schema-invalid) → drop entries immediately.
- **JCS-canonical signed bytes.** The coordinator builds the same bytes the
  cold key will sign. The canonicalizer is shared with
  [`../secure-orch-console/internal/canonical/`](../secure-orch-console/internal/canonical/)
  via a re-implementation here (zero-dep, stdlib only).

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Architectural overview | [`DESIGN.md`](./DESIGN.md) |
| Operator runbook | [`docs/operator-runbook.md`](./docs/operator-runbook.md) |
| Build / run / test gestures | [`Makefile`](./Makefile) |
| Example coordinator config | [`examples/coordinator-config.yaml`](./examples/coordinator-config.yaml) |
| The wire spec this implements | [`../livepeer-network-protocol/`](../livepeer-network-protocol/) |
| The verifier shared with resolver/gateway | [`../livepeer-network-protocol/verify/`](../livepeer-network-protocol/verify/) |

## Package layout

```
cmd/livepeer-orch-coordinator/  — main binary entrypoint
internal/
  config/                       — coordinator-config.yaml grammar + validation
  types/                        — decoded broker offerings, candidate, signed manifest
  providers/
    brokerclient/               — HTTP GET /registry/offerings (real + dev fake)
  repo/
    candidates/                 — filesystem snapshots (history, pruned by count)
    audit/                      — BoltDB publish + upload events
    published/                  — single live-manifest file
  service/
    scrape/                     — poll loop, freshness, dedup, last-good fallback
    candidate/                  — JCS-canonical bytes from scrape cache
    diff/                       — candidate-vs-published structural diff
    roster/                     — roster row materialization
    receive/                    — verify + atomic-swap publish
  server/
    adminapi/                   — operator-facing HTTP+JSON + web UI handlers
      web/                      — embedded HTML/CSS/JS via embed.FS
    publicapi/                  — resolver-facing /.well-known/... only
    metrics/                    — Prometheus
```

## Code-of-conduct

- Stdlib `net/http`, `html/template`, `embed.FS`. No router framework.
- BoltDB (`go.etcd.io/bbolt`) for the audit log.
- `github.com/ethereum/go-ethereum/crypto` for signature recovery — reused via
  `../livepeer-network-protocol/verify/`.
- `github.com/prometheus/client_golang` for metrics.
- No new dependencies beyond those.
- The `--public-listen` listener serves **only**
  `GET /.well-known/livepeer-registry.json`. Add a unit test to cover the
  404-everywhere-else invariant when the public listener changes.
- The coordinator never opens an inbound connection from the public internet
  for scrape purposes. Brokers live on the LAN.
