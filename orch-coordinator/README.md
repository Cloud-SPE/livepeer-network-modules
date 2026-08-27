# orch-coordinator

The operator's LAN-side process that scrapes capability-broker
`/registry/offerings`, builds a candidate manifest the operator hand-carries
to `secure-orch-console` for cold-key signing, receives the signed manifest
back via HTTP POST, and atomic-swap publishes the live manifest at
`/.well-known/livepeer-registry.json` for resolvers to consume.

> **For agents:** start at [`AGENTS.md`](./AGENTS.md).

## What it is

One process per orch operator (not per host). A single operator with multiple
broker hosts on the LAN runs one coordinator; the coordinator scrapes them
all and unifies their offerings into a single candidate manifest. In addition
to `/registry/offerings`, it also consumes broker `/registry/health` so the
roster can surface per-tuple liveness and broker metadata-discovery state.

Three listeners:

- `--listen=:8080` — operator UX. Web UI (roster + diff + signed-manifest
  upload) plus JSON API. Bound to the LAN; the operator hits this from a
  browser on the same LAN.
- `--public-listen=:8081` — resolver-facing. Serves only
  `GET /.well-known/livepeer-registry.json`; everything else is 404.
- `--metrics-listen=:9091` — Prometheus.

The coordinator never holds a signing key. Cold key on `secure-orch` is the
only signer.

`candidate.tar.gz` now carries richer operator-only provenance in
`metadata.json`, including broker metadata-warning thresholds, per-broker
metadata summaries, and per-tuple metadata warnings when broker discovery is
degraded, stale, or has never succeeded.

When `ORCH_COORDINATOR_ADMIN_TOKENS` is set, the admin listener requires
operator login with admin token + actor identity and records the actor on
signed-manifest upload audit events. The admin UI allows one active
session at a time, with a 4-hour absolute timeout and a 30-minute
idle timeout.

## Status

Feature-complete against plan 0018 plus the plan 0042 sign-cycle agent:
config parser, broker HTTP client, scrape loop, candidate build + tarball,
diff surface, roster UI, signed-manifest receive + atomic publish, the
resolver endpoint, the agent bearer credential, and Prometheus metrics all
ship.

## Build

```bash
make build               # build tztcloud/livepeer-orch-coordinator:dev
make test                # go test -race ./...
make help                # show all targets
```

## Hot-zone console

Four pages manage runners and offers over each broker's admin API
(plan 0043 §3.6). They are the *hot* zone: they change what a runner may
serve. They cannot change what is sold — prices come from offers, and
the manifest is still only ever changed by the cold key on secure-orch.

| Page | Answers |
|---|---|
| **Runners** | Which hosts are attached, what they declared, and why a capability is or is not eligible for an offer — with the disagreeing field and both sides named. |
| **Offers** | What each broker sells, the frozen runner-declared shape, and candidate shapes with their diff. `Accept this shape` is the explicit supersession gesture; the candidate still has to be signed. |
| **Enroll host** | Mint an attach credential (shown once) with the agent environment to paste into the bundle; list, and revoke, enrollments. |
| **Certification** | What each runner proved before it was allowed to serve, step by step with evidence; re-run on demand. |

Each broker needs `admin_token_ref` in `coordinator-config.yaml`:

```yaml
brokers:
  - name: broker-a
    base_url: http://broker-a:8080
    admin_token_ref: env://BROKER_A_ADMIN_TOKEN   # or file:///run/secrets/broker-a-admin
```

Reference form only, never the secret inline. A broker without one is
listed but not administrable, and the pages say so. The pages are not
registered at all when no broker admin surface is configured.

## Configuration

A YAML config file (mounted to `/etc/livepeer/orch-coordinator.yaml` by
default) plus flags. See
[`examples/coordinator-config.yaml`](./examples/coordinator-config.yaml)
and the flag documentation in
[`docs/operator-runbook.md`](./docs/operator-runbook.md); `--help` on the
binary is the authoritative list.

## Layout

```
orch-coordinator/
├── cmd/livepeer-orch-coordinator/   main binary
├── internal/
│   ├── config/                      coordinator-config.yaml grammar
│   ├── types/                       offerings, candidate, signed-manifest types
│   ├── providers/brokerclient/      HTTP GET /registry/offerings
│   ├── repo/                        candidates / audit / published manifest
│   ├── service/                     scrape, candidate, diff, roster, receive
│   └── server/                      adminapi / publicapi / metrics
├── examples/coordinator-config.yaml
├── docs/                            design + operator runbook
├── Dockerfile                       distroless static
├── Makefile                         docker-first gestures
├── compose/                         run-only compose (+ agent overlay)
└── compose.yaml                     dev compose (coordinator in --dev mode)
```
