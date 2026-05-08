# chain-commons

Shared chain-glue Go library for the [livepeer-modules-project](../README.md) monorepo.

Provides the Ethereum/Arbitrum interaction primitives that `payment-daemon`, `service-registry-daemon`, and `protocol-daemon` consume:

- **Multi-RPC failover** — primary/backup endpoint routing with circuit breaker
- **Durable transaction state** — `TxIntent` state machine with idempotency, replacement, reorg-aware confirmation, restart resume
- **Controller-resolved addresses** — sub-contract address discovery from on-chain Controller, no bake-ins
- **Gas oracle** — TTL-cached `eth_gasPrice` + `maxPriorityFeePerGas`
- **Log subscriptions with durable offsets** — restart-safe `eth_getLogs` poller
- **Reorg-aware confirmation tracking** — wait N confirmations before terminal
- **Keystore signing** — V3 JSON in v1; HSM/KMS shaped for v2
- **BoltDB persistence** — single-writer key-value, embedded
- **Structured logging** — stdlib `log/slog` wrapper
- **Prometheus-recordable metrics** — via a `Recorder` interface (no Prometheus dependency)

`chain-commons` is a library, never a daemon. It has no `cmd/`, no `main`, no Docker image. External workload binaries (transcode, inference, etc.) talk to the chain-aware daemons over local gRPC; they don't import `chain-commons` directly.

## Status

This is the first scaffolding milestone (plan 0001 §D–§K). Currently shipping:

- ✅ All 11 provider interfaces (rpc, controller, keystore, gasoracle, logs, receipts, timesource, store, metrics, logger, clock)
- ✅ `services/txintent` — full durable state machine + persistence + idempotency, comprehensive test suite
- ✅ `services/{roundclock, eventlog}` — interfaces only; impls land later
- ✅ In-memory `Store` (production BoltDB impl lands later)
- ✅ `slog`-backed `Logger` (production impl)
- ✅ No-op `Recorder` (production decorators live in daemons)
- ✅ System `Clock`
- ⏳ Provider implementations for `rpc`, `controller`, `gasoracle`, `keystore`, `logs`, `receipts`, `timesource` — land in subsequent commits
- ⏳ `services/txintent` `Processor` (signing/broadcasting/receipt-tracking goroutine) — lands in subsequent commit

The interfaces are stable enough to be consumed; consumer daemons can dial against fakes from `testing/` (when it lands) and switch to real impls without API churn.

## Layout

```
chain-commons/
├── chain/              typed domain values
├── errors/             classified error types + Classify()
├── config/             validated Config struct
├── providers/          interfaces + per-provider impls
│   ├── rpc/            multi-URL go-ethereum wrapper (impl pending)
│   ├── controller/     sub-contract address resolver (impl pending)
│   ├── keystore/       V3 JSON keystore + HSM-shaped Sign() (impl pending)
│   ├── gasoracle/      eth_gasPrice + maxPriorityFeePerGas TTL cache (impl pending)
│   ├── logs/           eth_getLogs poller with durable offsets (impl pending)
│   ├── receipts/       reorg-aware confirmation tracking (impl pending)
│   ├── timesource/     current round + L1 block + Round events (impl pending)
│   ├── store/          BoltDB-backed KV (memory impl shipped; bolt impl pending)
│   ├── metrics/        Recorder interface + no-op
│   ├── logger/         slog wrapper
│   └── clock/          time.Now + tickers
├── services/
│   ├── txintent/       durable transaction state machine ✅ shipped
│   ├── roundclock/     typed Round events (impl pending)
│   └── eventlog/       durable log subscriptions (impl pending)
├── testing/            fakes for every provider (impl pending)
├── lint/               coverage-gate, layer-check, no-secrets-in-logs
├── go.mod
├── Makefile
└── .golangci.yml
```

## Usage

```sh
make build           # go build ./...
make test            # go test ./...
make test-race       # go test -race ./...
make lint            # go vet + golangci-lint (if installed)
make coverage-check  # per-package coverage report
```

## Design

Full design at the monorepo root:

- [`docs/design-docs/chain-commons-api.md`](../docs/design-docs/chain-commons-api.md) — provider + service catalog with rationale
- [`docs/design-docs/tx-intent-state-machine.md`](../docs/design-docs/tx-intent-state-machine.md) — the durable transaction state machine
- [`docs/design-docs/multi-rpc-failover.md`](../docs/design-docs/multi-rpc-failover.md) — circuit-breaker primary/backup routing
- [`docs/design-docs/controller-resolver.md`](../docs/design-docs/controller-resolver.md) — sub-contract address discovery
- [`docs/design-docs/event-log-offsets.md`](../docs/design-docs/event-log-offsets.md) — durable per-subscriber log offsets

The build-out plan: [`docs/exec-plans/active/0001-establish-monorepo-and-chain-commons.md`](../docs/exec-plans/active/0001-establish-monorepo-and-chain-commons.md).

## License

[MIT](../LICENSE).
