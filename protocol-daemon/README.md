# protocol-daemon

A standalone daemon that handles Livepeer’s chain-side orchestrator responsibilities:
round initialization, reward calling, and on-chain `ServiceRegistry` /
`AIServiceRegistry` pointer writes. Built on [`chain-commons`](../chain-commons) — the
durable transaction state machine, multi-RPC failover, Controller-resolved addresses,
and reorg-aware confirmation tracking are all reused from the shared library.

> **Agent-first repository.** Start with [docs/exec-plans/](docs/exec-plans/) → [docs/design-docs/](docs/design-docs/). Root plan [0020](../docs/exec-plans/completed/0020-protocol-daemon-migration.md) delivered the daemon end-to-end.

## What it does

One binary, three modes:

| Mode | Role | What runs |
|---|---|---|
| `--mode=round-init` | Round initializer (any orchestrator can run this — first one to fire wins) | Subscribes to round transitions, calls `RoundsManager.initializeRound()` once per round if not yet initialized |
| `--mode=reward` | Reward caller (orchestrator-specific) | Subscribes to round transitions, checks transcoder eligibility, computes positional pool hints, calls `BondingManager.rewardWithHint(prev, next)` once per round |
| `--mode=both` | Both of the above in one process | The common case for an orchestrator running the protocol daemon as a sidecar |

Every on-chain write goes through `chain-commons.services.txintent` — durable,
idempotent (same round → same intent ID → no duplicate tx), reorg-safe,
restart-resumable.

In the rewrite stack, `SetServiceURI` should point at the public
`orch-coordinator` manifest URL, typically:

- `https://<coordinator-host>/.well-known/livepeer-registry.json`

## Quick start

### Dev mode (no chain, fake providers)

```bash
make build
./bin/livepeer-protocol-daemon --mode=both --dev --socket=/tmp/protocol.sock
```

Dev mode wires `chain-commons.testing.FakeRPC` + `FakeKeystore` + `FakeReceipts` — useful for local development and CI. A loud `DEV MODE` banner prints to stderr at startup; on-chain calls are simulated.

### Production mode (Arbitrum One, real chain)

```bash
export LIVEPEER_KEYSTORE_PASSWORD="$(cat /etc/livepeer/ks-password)"
./bin/livepeer-protocol-daemon \
  --mode=both \
  --socket=/var/run/livepeer-protocol-daemon.sock \
  --store-path=/var/lib/livepeer/protocol-state.db \
  --eth-urls=https://arb1.arbitrum.io/rpc,https://arbitrum.publicnode.com \
  --keystore-path=/etc/livepeer/keystore.json \
  --controller-address=0xD8E8328501E9645d16Cf49539efC04f734606ee4 \
  --orch-address=0x<your-cold-orch> \
  --metrics-listen=:9094
```

Run `bin/livepeer-protocol-daemon --help` for the full flag reference.

### Docker

```bash
cp .env.example .env
docker compose up --build
```

Image is ~20 MB (distroless/static, pure-Go build). See `Dockerfile` and `compose.yaml` at the module root for details.

### See it end-to-end

```sh
go run ./examples/minimal-e2e/...
```

Stands up `chain-commons.testing.FakeRPC` + `FakeReceipts`, wires both `round-init` and `reward` services, fires one Round event, and verifies that both `InitializeRound` and `RewardWithHint` `TxIntent`s reach `confirmed`.

## Architecture at a glance

```
            types ─▶ providers ─▶ repo ─▶ service ─▶ runtime ─▶ cmd
                       │
       ┌───────────────┼─────────────────┐
       ▼               ▼                 ▼
   ABI bindings    chain-commons     local poolhints
   (RoundsMgr,     (rpc, ctrl,       cache (BoltDB)
   BondingMgr,     keystore,
   Minter)         txintent, ...)
```

- `internal/types/` — pure data: `Mode`, `RewardEligibility`, `PoolHints`, error codes
- `internal/config/` — validated daemon config (extends `chain-commons.config.Config`)
- `internal/providers/{bondingmanager,roundsmanager,minter}/` — ABI-bound thin wrappers over `chain-commons.providers.rpc`
- `internal/repo/poolhints/` — BoltDB cache of computed `(prev, next)` hints, keyed by round
- `internal/service/{roundinit,reward,orchstatus,preflight}/` — the protocol logic
- `internal/runtime/{grpc,metrics,lifecycle}/` — gRPC over unix socket, opt-in Prometheus listener, signal handling
- `cmd/livepeer-protocol-daemon/` — flags, dev/prod dispatch, provider construction

The layering is enforced by the per-module `lint/layer-check/`. `internal/service/*` may not import `github.com/ethereum/*` or `bbolt` directly — those go through `internal/providers/` and `chain-commons.providers.*`.

## Highlights

- **Built on `chain-commons`.** Round-init is ~30 lines of business logic; reward is ~50 lines including positional hints. Idempotency, replacement, reorg-recovery, restart-resume all come from `chain-commons.services.txintent`.
- **Three modes via single binary.** `--mode=round-init|reward|both`. Mode-specific RPCs return `Unimplemented` if called on the wrong mode (matches `payment-daemon` and `service-registry-daemon` pattern).
- **Chain registry writes included.** Operator RPCs can set and read on-chain
  `ServiceRegistry` / `AIServiceRegistry` pointers; in this rewrite those pointers should
  reference the coordinator-hosted manifest URL.
- **Pool-hint cache.** Walking the transcoder pool linked list is multiple `eth_call`s. Cached by round in BoltDB; same-round invocations after the first are a fast path.
- **Multi-RPC by default.** `--eth-urls` accepts a comma-separated list; `chain-commons.providers.rpc.multi` does primary/backup failover with circuit breakers.
- **Preflight at startup.** Chain-id verification, Controller resolution + `CodeAt` checks for `RoundsManager` and `BondingManager`, keystore decryption, min-balance gate. A misconfigured daemon fails loudly before the gRPC socket opens.
- **Off-by-default Prometheus.** `--metrics-listen=:9094` (per [`docs/conventions/ports.md`](../docs/conventions/ports.md)) opts in. `livepeer_protocol_*` namespace per [`docs/conventions/metrics.md`](../docs/conventions/metrics.md).
- **75% per-package coverage gate.** Enforced via `lint/coverage-gate/` (matches `payment-daemon`).

## gRPC surface

All RPCs over a unix socket:

```proto
service ProtocolDaemon {
  rpc GetRoundStatus(Empty) returns (RoundStatus);
  rpc GetRewardStatus(Empty) returns (RewardStatus);
  rpc ForceInitializeRound(Empty) returns (ForceOutcome);
  rpc ForceRewardCall(Empty) returns (ForceOutcome);
  rpc SetServiceURI(SetServiceURIRequest) returns (TxIntentRef);
  rpc GetOnChainServiceURI(Empty) returns (OnChainServiceURIStatus);
  rpc IsRegistered(Empty) returns (RegistrationStatus);
  rpc GetWalletBalance(Empty) returns (WalletBalanceStatus);
  rpc GetTxIntent(TxIntentRef) returns (TxIntentSnapshot);
  rpc StreamRoundEvents(Empty) returns (stream RoundEvent);
  rpc Health(Empty) returns (HealthStatus);
}
```

Mode-specific RPCs (`GetRoundStatus`, `ForceInitializeRound`) and
(`GetRewardStatus`, `ForceRewardCall`) return `Unimplemented` when
called on the wrong mode. The proto file at
`proto/livepeer/protocol/v1/protocol.proto` is the canonical source.

## Build & test

```bash
make build          # bin/livepeer-protocol-daemon
make test           # full unit + integration tests
make lint           # go vet + custom lints
make coverage-check # 75% per-package gate
make docker-build   # tztcloud/livepeer-protocol-daemon:dev
```

## License

[MIT](../LICENSE).

## Documentation

- [`AGENTS.md`](./AGENTS.md) — agent-facing component map and attribution
- [`DESIGN.md`](./DESIGN.md) — component overview
- [`docs/design-docs/architecture.md`](./docs/design-docs/architecture.md) — layer and runtime architecture
- [`docs/operator-runbook.md`](./docs/operator-runbook.md) — deployment and operations guide
