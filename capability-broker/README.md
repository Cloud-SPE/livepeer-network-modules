# capability-broker

The Go reference implementation of the workload-agnostic capability broker
defined in [`../livepeer-network-protocol/`](../livepeer-network-protocol/).

> **For agents:** start at [`AGENTS.md`](./AGENTS.md).

## What it is

One process per orch host. Reads a single declarative `host-config.yaml`,
exposes:

- `POST /v1/cap` — paid request entry point (HTTP modes).
- `GET /v1/cap` — paid WebSocket upgrade entry point (`ws-realtime` mode).
- `POST /v1/payment/ticket-params` — unpaid quote-free ticket-params proxy for sender-mode payment daemons.
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

**Shipped.** 6 mode drivers registered, 7 extractors, RTMP-ingress + LL-HLS
egress pipeline, session-control + WebRTC SFU pass-through, and broker-side
interim-debit ticker are all in. See PLANS.md "Code shipping today" §`capability-broker/`
for the canonical summary, and the design brief at
[`../docs/exec-plans/completed/0003-capability-broker.md`](../docs/exec-plans/completed/0003-capability-broker.md).

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
The example starts with minimal `http-reqresp@v0` entries for smoke bring-up
and keeps more involved shipped shapes commented out until you wire the
necessary backend infrastructure.

For OpenAI-compatible offerings, use the base capability family in `id`
(`openai:chat-completions`, `openai:embeddings`, etc.) and put model identity
in `extra.openai.model`. Deprecated suffixed forms such as
`openai:chat-completions:<model>` are rejected by config validation.

Current broker validation for `openai:*` offerings requires:

- `extra.openai.model`
- `extra.provider`

Optional stable enrichment fields are:

- `served_model_name`
- `backend_model`
- `features.*` (booleans only)

For `provider: "vllm"` and `provider: "ollama"` on HTTP OpenAI-compatible
backends, the broker attempts a startup metadata probe against `GET /v1/models`.
When the configured `extra.openai.model` is present upstream, the broker fills
missing `served_model_name`, `backend_model`, and capability-appropriate
`features.*` fields in `/registry/offerings`. Operator-declared values still
win; discovery fills gaps only.

The broker refreshes this metadata periodically while running. Per-offering
refresh status and the last discovery result are exposed on
`GET /registry/health` under each capability's `metadata` object.

When the broker runs in production, mount your real `host-config.yaml` over
`/etc/livepeer/host-config.yaml` (the default `--config` location).

## Layout

See [`docs/design-docs/architecture.md`](./docs/design-docs/architecture.md)
for the full package tree and dispatch flow.

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

`internal/` packages reflect what's shipped.
