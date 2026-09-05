# `payment-daemon/`

Long-lived sidecar that owns the Livepeer-Network payment session state.
Runs on both sides of a paid request:

- **`--mode=receiver`** — orchestrator-side. Validates incoming
  `Payment` envelopes, tracks per-sender balances, and (post chain
  integration) redeems winning tickets on-chain. The capability-broker
  talks to this daemon over a unix socket via the `PayeeDaemon` gRPC
  service. Operator-only maintenance calls use the co-mounted
  `PayeeAdmin` gRPC service on the same socket.
- **`--mode=sender`** — sender-side. Mints `Payment` envelopes for the
  paying app. Sender clients and the conformance runner talk to this daemon
  over a unix socket via the `PayerDaemon` gRPC service. Callers may
  later report payee-side rejection outcomes back to the daemon so it
  can invalidate stale cached sessions.

Wire format and gRPC contracts at [`../livepeer-network-protocol/proto/livepeer/payments/v1/`](../livepeer-network-protocol/proto/livepeer/payments/v1/).
Operational reading: [`docs/operator-runbook.md`](./docs/operator-runbook.md).
Payout planning: [`docs/payout-modeling-guide.md`](./docs/payout-modeling-guide.md).
Simulation inputs: [`scenarios/`](./scenarios/).

## Status (pre-1.0 — sender + receiver + restart-stable sessions)

- Both `sender` and `receiver` modes wire up. One binary, mode chosen
  at boot.
- `Payment` wire format is byte-compatible with go-livepeer's
  `net.Payment` per [`wire-compat.md`](../livepeer-network-protocol/docs/wire-compat.md);
  envelopes from this daemon decode against go-livepeer's `pm/`.
- Receiver sessions persist to BoltDB
  (`/var/lib/livepeer/payment-daemon/sessions.db`).
- `GetTicketParams` is restart-stable for an open
  `(sender, recipient, capability, offering)` session. Repeated calls
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
- `PayeeAdmin.ResetSession` gives operators an explicit session-rotation
  surface instead of relying on daemon restarts.
- **Chain integration is available when `--chain-rpc-urls` is set.** In dev
  mode the daemon still uses fake chain providers and a deterministic
  key; in production mode it validates against real chain state and runs
  the redemption pipeline.
- **Chain mode requires a spend limit.** A sender daemon will not start
  without `--max-payment-wei`, the most it may authorize for a single
  payment. Optional `--max-price-per-unit` adds per-work-unit rate
  ceilings, which is how a deployment mixing cheap and expensive
  workloads gets meaningful protection. See
  [operator-runbook §3.5](./docs/operator-runbook.md).

Anything in [`docs/operator-runbook.md`](./docs/operator-runbook.md)
that talks about real funds, real gas, or real redemption is
**forward-looking**. Do not deposit real funds against a v0.2 daemon.

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
| `--db` | `/var/lib/livepeer/payment-daemon/sessions.db` | BoltDB session ledger path (receiver only) |
| `--txintent-db` | `txintents.db` beside `--db` | BoltDB transaction-intent store: every redemption the daemon has signed, resumed on restart (receiver, chain mode) |
| `--payee-admin-token` | empty | bearer token for receiver-only `PayeeAdmin` methods; falls back to `PAYEE_DAEMON_ADMIN_TOKEN` when unset |

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
