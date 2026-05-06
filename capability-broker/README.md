# capability-broker

The Go reference implementation of the workload-agnostic capability broker
defined in [`../livepeer-network-protocol/`](../livepeer-network-protocol/).

> **For agents:** start at [`AGENTS.md`](./AGENTS.md).

## What it is

One process per orch host. Reads a single declarative `host-config.yaml`,
exposes:

- `POST /v1/cap` — paid request entry point (HTTP modes).
- `GET /v1/cap` — paid WebSocket upgrade entry point (`ws-realtime` mode).
- `GET /registry/offerings` — capability inventory for orch-coordinator scrape.
- `GET /registry/health` — live capability availability for gateway resolvers.
- `GET /healthz` — process health.
- `GET /metrics` — Prometheus scrape.

Dispatches inbound requests to backends declared in `host-config.yaml`. Reports
work units via the offering's declared extractor. Validates payment via a
co-located `payment-daemon` (over unix socket; v0.1 uses a stub client).

**This binary contains zero capability-specific code.** All workload knowledge
lives in mode adapters and extractor implementations, both standardized in the
spec.

## Status

**v0.1 scaffold** — flag parsing, Docker build, Makefile gestures all work. The
HTTP server, `host-config.yaml` parser, mode drivers, extractors, and payment
client are TODO. The binary exits with code 2 ("not implemented") to make the
state unambiguous.

Tracked in [`../docs/exec-plans/active/0003-capability-broker.md`](../docs/exec-plans/active/0003-capability-broker.md).

## Build

Per repo-root core belief #15, every gesture is Docker-first.

```bash
make build               # build tztcloud/livepeer-capability-broker:dev
make run                 # run with examples/host-config.example.yaml
make help                # show all targets
```

No host Go install required.

## Configuration

A single declarative YAML file: [`examples/host-config.example.yaml`](./examples/host-config.example.yaml).

When the broker runs in production, mount your real `host-config.yaml` over
`/etc/livepeer/host-config.yaml` (the default `--config` location).

## Layout (planned)

See [`docs/design-docs/architecture.md`](./docs/design-docs/architecture.md)
for the full planned package tree and dispatch flow.

```
capability-broker/
├── cmd/livepeer-capability-broker/main.go
├── internal/
│   ├── config/         # host-config.yaml loader + validator
│   ├── server/         # HTTP server, middleware, registry endpoints
│   ├── modes/          # one driver per mode
│   ├── extractors/     # work-unit extractor library
│   ├── payment/        # payment-daemon client (mock for v0.1)
│   └── observability/  # metrics, logging, request-id
├── examples/
│   └── host-config.example.yaml
├── docs/
└── Dockerfile / Makefile / go.mod
```

`internal/` packages are added as code lands; the v0.1 scaffold creates only
the entry point.
