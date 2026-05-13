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
all and unifies their offerings into a single candidate manifest.

Three listeners:

- `--listen=:8080` — operator UX. Web UI (roster + diff + signed-manifest
  upload) plus JSON API. Bound to the LAN; the operator hits this from a
  browser on the same LAN.
- `--public-listen=:8081` — resolver-facing. Serves only
  `GET /.well-known/livepeer-registry.json`; everything else is 404.
- `--metrics-listen=:9091` — Prometheus.

The coordinator never holds a signing key. Cold key on `secure-orch` is the
only signer.

When `ORCH_COORDINATOR_ADMIN_TOKENS` is set, the admin listener requires
operator login with admin token + actor identity and records the actor on
signed-manifest upload audit events. The admin UI allows one active
session at a time, with a 12-hour absolute timeout and a 30-minute
idle timeout.

## Status

**v0.1 scaffold** (plan 0018, commit 1). The flag set, config parser, broker
HTTP client, and scrape loop are wired; candidate output, diff surface,
signed-manifest receive, resolver endpoint, metrics, and web UI land in
later commits.

## Build

```bash
make build               # build tztcloud/livepeer-orch-coordinator:dev
make test                # go test -race ./...
make help                # show all targets
```

## Configuration

A YAML config file (mounted to `/etc/livepeer/orch-coordinator.yaml` by
default) plus flags. See
[`examples/coordinator-config.yaml`](./examples/coordinator-config.yaml)
and the [`AGENTS.md`](./AGENTS.md) flag table.

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
└── compose.yaml                     dev compose (coordinator + a fake broker)
```

`internal/` packages are added as code lands across plan 0018's seven
commits.
