# AGENTS.md

This is `payment-daemon/` — the payment sidecar for both receiver-side and
sender-side flows. Brokers, gateways, and test harnesses talk to it over a
unix-socket gRPC connection per the spec at
[`../livepeer-network-protocol/proto/livepeer/payments/v1/`](../livepeer-network-protocol/proto/livepeer/payments/v1/).

Component-local agent map. The repo-root [`../AGENTS.md`](../AGENTS.md) is
the cross-cutting map; this file scopes to daemon-specific guidance.

## Operating principles

Inherited from the repo root (agent-first harness pattern). Plus:

- **The proto is the contract.** When code disagrees with
  `../livepeer-network-protocol/proto/`, the proto wins. File a plan if
  the proto needs to change.
- **The daemon is stubbed.** v0.2 accepts any non-empty `ticket` and
  records it. Don't add ticket-format validation here without a plan;
  that is the chain-integration workstream's domain.
- **State is durable.** BoltDB is the session ledger. Tests should not
  reach in and mutate the file directly — use the gRPC surface.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Operator-grade runbook (economics, gas, escrow, redemption, hot/cold wallet, dev mode) | [`docs/operator-runbook.md`](./docs/operator-runbook.md) |
| How to reason about payout rate, payout cadence, and simulations | [`docs/payout-modeling-guide.md`](./docs/payout-modeling-guide.md) |
| Wire-format spec the daemon implements | [`../livepeer-network-protocol/proto/livepeer/payments/v1/`](../livepeer-network-protocol/proto/livepeer/payments/v1/) |
| Wire-compat byte-for-byte contract with go-livepeer | [`../livepeer-network-protocol/docs/wire-compat.md`](../livepeer-network-protocol/docs/wire-compat.md) |
| Architectural overview | [`DESIGN.md`](./DESIGN.md) |
| Build / run / test gestures | [`Makefile`](./Makefile) |
| Tech debt | [`docs/exec-plans/tech-debt-tracker.md`](./docs/exec-plans/tech-debt-tracker.md) |

## Package layout

```
cmd/livepeer-payment-daemon/   — entrypoint (flag parsing + boot)
cmd/payout-sim/                — offline payout/pricing simulator
internal/
  server/                      — grpc.Server lifecycle + listener
  service/                     — PayerDaemon / PayeeDaemon / PayeeAdmin RPC
                                 implementation, plus escrow + settlement
  store/                       — BoltDB session ledger
  providers/                   — chain, keystore, devbroker, metrics adapters
  types/                       — shared payment domain values
  payoutsim/                   — simulator engine behind cmd/payout-sim
  compat/                      — go-livepeer wire-compat round-trip fixture
lint/no-secrets-in-logs/       — custom analyzer (see lint/README.md)
```

The generated gRPC bindings are **not** vendored here — they live in
[`../livepeer-network-protocol/proto-go/livepeer/payments/v1/`](../livepeer-network-protocol/proto-go/livepeer/payments/v1/)
and are pulled in via a `replace` directive in `go.mod`. `make proto`
regenerates them into that directory.

## Code-of-conduct

- Generated `.pb.go` files are committed; treat them as read-only and
  regenerate via `make proto` after editing a `.proto`.
- The store package is the single owner of bbolt buckets — no other
  package may touch BoltDB directly.
- gRPC errors map to `codes.NotFound` / `codes.InvalidArgument` /
  `codes.FailedPrecondition`; do not invent ad-hoc error strings.
