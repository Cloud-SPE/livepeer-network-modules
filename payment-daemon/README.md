# `payment-daemon/`

Long-lived sidecar that owns the Livepeer-Network payment session state.
Runs on both sides of a paid request:

- **`--mode=receiver`** — orchestrator-side. Validates incoming
  `Payment` envelopes, tracks per-sender balances and wholesale accounts,
  and (in chain mode) redeems winning tickets on-chain. The capability-broker
  talks to this daemon over a unix socket via the `PayeeDaemon` gRPC
  service. Operator-only maintenance calls use the co-mounted
  `PayeeAdmin` gRPC service on the same socket.
- **`--mode=sender`** — sender-side. Mints `Payment` envelopes for the
  paying app. Sender clients and the conformance runner talk to this daemon
  over a unix socket via the `PayerDaemon` gRPC service. Callers may
  later report payee-side rejection outcomes back to the daemon so it
  can invalidate stale cached sessions. On a dev clock only, the
  token-gated `PayerAdmin` service is co-mounted for conformance round
  advancement.

Wire format and gRPC contracts at [`../livepeer-network-protocol/proto/livepeer/payments/v1/`](../livepeer-network-protocol/proto/livepeer/payments/v1/).
Operational reading: [`docs/operator-runbook.md`](./docs/operator-runbook.md).
Payout planning: [`docs/payout-modeling-guide.md`](./docs/payout-modeling-guide.md).
Simulation inputs: [`scenarios/`](./scenarios/).

## Status (sender + receiver + restart-stable sessions)

- Both `sender` and `receiver` modes wire up. One binary, mode chosen
  at boot.
- `Payment` wire format is byte-compatible with go-livepeer's
  `net.Payment` per [`wire-compat.md`](../livepeer-network-protocol/docs/wire-compat.md);
  envelopes from this daemon decode against go-livepeer's `pm/`.
- Receiver sessions persist to BoltDB
  (`/var/lib/livepeer/payment-daemon/sessions.db`). Sender mode opens the
  same `--db` path for mint-idempotency records, its durable nonce
  watermark, and its persistent ticket stream identity; give each independently
  running daemon its own durable file. Never clone an active sender database.
- `GetTicketParams` is restart-stable for an open
  `(sender, recipient, capability, offering, wholesale_account_id, ticket_stream_id)` session. Repeated calls
  reuse the same `recipient_rand_hash` until the session is closed or
  reset.
- Receiver-side `ProcessPayment` returns machine-readable per-ticket
  status (`TicketStatus`, `tickets_rejected`, `dominant_rejection`) so
  callers can distinguish invalid-recipient-rand from replay or
  signature failures.
- Sender-side `CreatePayment` returns the minted `work_id`, and
  `ReportPaymentResult` lets a caller report
  `INVALID_RECIPIENT_RAND` back to the daemon. The daemon evicts the
  stale cached session and returns `codes.Aborted` with retry details.
- `CreateSpendAuthorization` signs a chain-, route-, price-, and
  workload-bound grant without minting. Account-aware `CreatePayment` mints
  only target-float shortfall and returns no payment at zero shortfall.
- Receiver account RPCs atomically admit, reserve, advance session runway,
  settle actual units, release unused value, and expose account state.
- `PayeeAdmin.ResetSession` gives operators an explicit session-rotation
  surface instead of relying on daemon restarts.
- **Chain integration is available when `--chain-rpc-urls` is set.** In dev
  mode the daemon still uses fake chain providers and a deterministic
  key; in production mode it validates against real chain state and runs
  the redemption pipeline.
- **Chain mode requires a spend limit.** A sender daemon will not start
  without `--max-payment-wei`, the most actual expected value it may sign in
  one account replenishment. This is a circuit breaker, not a
  workload-size setting. Optional `--max-ticket-face-value-wei` separately
  caps one winning ticket's worst-case payout exposure. Optional
  `--max-authorization-wei` independently caps
  the cumulative debit one job/session may consume. Optional
  `--max-price-per-unit` adds per-work-unit rate
  ceilings, which is how a deployment mixing cheap and expensive
  workloads gets meaningful protection. See
  [operator-runbook §3.5](./docs/operator-runbook.md).

Everything in [`docs/operator-runbook.md`](./docs/operator-runbook.md)
about real funds, real gas, or real redemption applies to chain mode
only. A dev-mode daemon (no `--chain-rpc-urls`) signs with a published
throwaway key and never touches a chain; do not deposit real funds
against it.

## Image

`tztcloud/livepeer-payment-daemon:<tag>`

## Run gestures

```sh
make build      # build dev image locally
make run        # foreground receiver; sock at ./run/payment-daemon.sock
                # (MODE=sender make run for the sender side)
make test       # in-container go test ./...
make publish TAG=0.1.0   # multi-arch push (requires real TAG)
```

Scenario compose:

```sh
docker compose -f compose/docker-compose.scenario.yml --profile sim up payout-sim
docker compose -f compose/docker-compose.scenario.yml --profile sender --profile receiver up -d
```

## Configuration

Flags:

| Flag | Default | Purpose |
|---|---|---|
| `--mode` | — (**required**) | `sender` or `receiver`; the process refuses to boot without it |
| `--socket` | per-mode: `/var/run/livepeer/payer-daemon.sock` (sender), `/var/run/livepeer/payment-daemon.sock` (receiver) | unix socket the gRPC server listens on |
| `--db` | `/var/lib/livepeer/payment-daemon/sessions.db` | BoltDB ledger path: receiver sessions + wholesale accounts, or sender mint-idempotency records |
| `--settlement-domain-id` | empty (generated once and stored) | receiver only: optional bootstrap import of the immutable ledger ID; a mismatch with the stored ID refuses startup |
| `--txintent-db` | `txintents.db` beside `--db` | BoltDB transaction-intent store: every redemption the daemon has signed, resumed on restart (receiver, chain mode) |
| `--payee-admin-token` | empty | bearer token for receiver-only `PayeeAdmin` methods; falls back to `PAYEE_DAEMON_ADMIN_TOKEN` when unset |
| `--payer-admin-token` | empty | bearer token for sender-only `PayerAdmin` methods (dev clock only; empty disables admin access) |

The full flag set (chain, keystore, gas, and redemption tunables) is in
[`docs/operator-runbook.md`](./docs/operator-runbook.md); `--version`
prints the build and exits.

The socket and DB paths are designed to be mounted as docker volumes shared
with the broker container.

## Operating model

```
                 ┌──────────────────────────────┐
                 │  capability-broker container │
                 │  (broker process)            │
                 └─────────────┬────────────────┘
                               │ gRPC
                       unix socket (shared volume)
                               │
                 ┌─────────────▼────────────────┐
                 │  payment-daemon container    │
                 │  (this binary)               │
                 │  ─────────────────────────── │
                 │  PayeeDaemon + PayeeAdmin    │
                 │  BoltDB sessions.db          │
                 └──────────────────────────────┘
```

The unix socket is the trust boundary: only processes with filesystem
access to the socket can call the daemon. The shared volume between the
broker container and the daemon container is the docker-level realization
of that boundary.

Regional collectors use [source-qualified revenue reporting](docs/regional-revenue-reporting.md),
including inclusion-block rounds and explicit completeness evidence.

## Shared-wallet isolation

Every payer application configures an explicit `wholesale_account_id` per environment
and passes it in account funding and spend-authorization requests. Independent payer
daemons may share one wallet only when every serving broker/daemon supports the
[shared-wallet contract](../livepeer-network-protocol/protocols/wholesale-account.md).
Until coordinated cutover, use one wallet per independent payer daemon. A `--db`
flag is optional because a default exists; persistent database storage is mandatory.

Each daemon generates `ticket_stream_id` once in its database. The same account may
have multiple streams; different accounts keep independent balances and versions.
All streams still spend the wallet's shared on-chain deposit. Product/environment
labels are accounting boundaries, not permissions for parties holding the same key.

Funding returns an immutable SHA-256-identified receipt. Replay exact payment bytes
after an uncertain response; do not infer credit from cumulative account deltas or
remint automatically. Isolated ticket generations cannot use legacy `ProcessPayment`;
use `FundWholesaleAccount` or signed authorization admission with inline funding.
Drain old account credit and active authorizations before upgrade; old balances are
retained and never assigned automatically. See the protocol's coordinated cutover.

The manual `livepeer-chain-probe` also requires `--wholesale-account-id`; use a
dedicated probe account and preserve that identity when replaying its recovery
checkpoint. This change does not run the probe or spend on-chain funds.
