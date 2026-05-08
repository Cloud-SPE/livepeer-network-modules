# DESIGN — protocol-daemon

Architectural overview. For the deep dive, read the design-docs at [`docs/design-docs/`](docs/design-docs/).

## What it does

Two on-chain orchestrator responsibilities, one binary:

1. **Round initialization** — calls `RoundsManager.initializeRound()` once per round if not already initialized.
2. **Reward calling** — calls `BondingManager.rewardWithHint(prev, next)` once per round on behalf of an active orchestrator, with positional pool hints walked from the transcoder pool linked list.

Modes: `--mode=round-init|reward|both`.

## What it isn't

- Not a payment ticket handler — that's [`payment-daemon`](../payment-daemon).
- Not a discovery daemon — that's [`service-registry-daemon`](../service-registry-daemon).
- Not a workload runtime — transcode, inference, and other workload binaries live outside this monorepo.
- Not a wallet manager — operator funds the wallet; we refuse to start when balance < `--min-balance-wei`.

## Why a separate daemon

`go-livepeer`'s round initializer + reward service share plumbing (TimeWatcher, BlockWatcher, TransactionManager, NonceManager, GasPriceMonitor, AccountManager) that the monorepo has consolidated into `chain-commons`. With the plumbing extracted, the protocol logic on top is small enough to live as its own daemon — letting an orchestrator run protocol responsibilities decoupled from `go-livepeer`'s media stack.

## Layer stack

```
            cmd/livepeer-protocol-daemon
                       │
                       ▼
                runtime/{grpc, metrics, lifecycle}
                       │
                       ▼
                service/{roundinit, reward, preflight}
                       │
            ┌──────────┴──────────┐
            ▼                     ▼
        repo/poolhints     providers/{roundsmanager, bondingmanager, minter}
                                   │
                                   ▼
                        chain-commons.providers.* / services.*
```

Enforced by `lint/layer-check/`. The providers boundary is the only cross-cutting boundary.

## Key dependencies

- `chain-commons.services.txintent` — every on-chain write
- `chain-commons.services.roundclock` — typed Round events with named-subscription dedup
- `chain-commons.providers.controller` — Controller-resolved sub-contract addresses
- `chain-commons.providers.rpc.multi` — multi-URL failover
- `chain-commons.providers.keystore.v3json` — V3 JSON keystore signing
- `chain-commons.providers.receipts.reorg` — reorg-aware confirmation tracking
- `chain-commons.providers.store.bolt` — BoltDB persistence
- `chain-commons.providers.gasoracle.ttl` — TTL-cached gas oracle
- `chain-commons.providers.timesource.poller` — round + L1 block polling

## What ships in this module

- `internal/providers/roundsmanager/` — ABI-bound `currentRoundInitialized`, `initializeRound` calldata
- `internal/providers/bondingmanager/` — ABI-bound `getTranscoder`, `getFirstTranscoderInPool`, `getNextTranscoderInPool`, `rewardWithHint` calldata, reward-event log decoder
- `internal/providers/minter/` — read-only ABI binding (placeholder for future inflation calculations)
- `internal/repo/poolhints/` — BoltDB-backed cache of `(prev, next)` per round
- `internal/service/roundinit/` — the round-init loop
- `internal/service/reward/` — the reward loop + pool walk
- `internal/service/preflight/` — chain-id, contract-code, keystore, balance gates
- `internal/runtime/grpc/` — `ProtocolDaemon` gRPC server
- `internal/runtime/metrics/` — opt-in Prometheus listener
- `internal/runtime/lifecycle/` — boot/shutdown coordination

## Where to read next

- [`docs/design-docs/architecture.md`](docs/design-docs/architecture.md) — full layer stack with rationale
- [`docs/design-docs/roundinit-loop.md`](docs/design-docs/roundinit-loop.md) — sequence diagram, jitter, idempotency
- [`docs/design-docs/core-beliefs.md`](docs/design-docs/core-beliefs.md) — invariants the daemon enforces

Reward-calc, positional-hints, observability, gRPC-surface and operator-runbook docs are tracked as follow-ups; the canonical sources today are the package doc-comments in `internal/service/reward/`, `internal/repo/poolhints/`, `internal/runtime/{grpc,metrics}/`, and `cmd/livepeer-protocol-daemon/`.
