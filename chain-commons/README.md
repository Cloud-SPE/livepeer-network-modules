# chain-commons

Shared chain-glue Go library for the [livepeer-network-modules](../README.md) monorepo.

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

Consumed in production by `payment-daemon`, `service-registry-daemon`, and
`protocol-daemon`. Currently shipping:

- ✅ All 13 provider interfaces (rpc, controller, keystore, gasoracle, logs, receipts, timesource, store, metrics, logger, clock, bondingmanager, roundsmanager) — each with a real implementation in a subpackage
- ✅ `services/txintent` — full durable state machine + persistence + idempotency + processor
- ✅ `services/{roundclock, eventlog}`
- ✅ Both `Store` impls: in-memory (`store/memstore.go`) and BoltDB (`store/bolt`)
- ✅ `slog`-backed `Logger`; no-op `Recorder` (production decorators live in daemons); system `Clock`
- ✅ `testing/` fakes for rpc, controller, keystore, gasoracle, receipts, store, metrics, logger, clock
- ✅ `testing/simchain` — go-ethereum's in-process simulated chain exposed as `rpc.RPC`, with deploy/mine helpers and a call-failing wrapper, for tests that need real signed transactions, nonces and receipts without a network
- ⏳ `lint/{coverage-gate, layer-check, no-secrets-in-logs}` — policy READMEs only; the Go tools are unimplemented (`.golangci.yml` `depguard` covers most layer rules today)

The interfaces are stable; consumer daemons dial against fakes from `testing/` and switch to real impls without API churn.

## Layout

```
chain-commons/
├── chain/              typed domain values
├── errors/             classified error types + Classify()
├── config/             validated Config struct
├── providers/          interfaces in the package root, impls in subpackages
│   ├── rpc/            multi-URL go-ethereum wrapper (rpc/multi)
│   ├── controller/     sub-contract address resolver (controller/eth)
│   ├── keystore/       V3 JSON keystore + HSM-shaped Sign() (keystore/v3json)
│   ├── gasoracle/      eth_gasPrice + maxPriorityFeePerGas TTL cache (gasoracle/ttl)
│   ├── logs/           eth_getLogs poller with durable offsets (logs/poller)
│   ├── receipts/       reorg-aware confirmation tracking (receipts/reorg)
│   ├── timesource/     current round + L1 block + Round events (timesource/poller)
│   ├── bondingmanager/ BondingManager reads + writes
│   ├── roundsmanager/  RoundsManager round state
│   ├── store/          KV: memstore.go (memory) + store/bolt (BoltDB)
│   ├── metrics/        Recorder interface + no-op
│   ├── logger/         slog wrapper
│   └── clock/          time.Now + tickers
├── services/
│   ├── txintent/       durable transaction state machine + processor
│   ├── roundclock/     typed Round events
│   └── eventlog/       durable log subscriptions
├── testing/            fakes for the providers daemons dial against
│   └── simchain/       simulated chain as rpc.RPC (real txs, no network)
├── lint/               coverage-gate, layer-check, no-secrets-in-logs
│                       (policy READMEs; tools not implemented yet)
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

The per-provider and per-service design rationale lives in the package
doc comments — start at [`doc.go`](./doc.go), then the interface files in
`providers/*/` and `services/*/`. The layer rules the module is built to
are pinned in [`lint/layer-check/README.md`](./lint/layer-check/README.md)
and enforced by `depguard` in `.golangci.yml`.

Repo-wide architectural context: [`../docs/design-docs/architecture-overview.md`](../docs/design-docs/architecture-overview.md)
and [`../AGENTS.md`](../AGENTS.md).

## License

MIT.
