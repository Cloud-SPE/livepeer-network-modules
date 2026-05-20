# `payment-daemon/`

Long-lived sidecar that owns the Livepeer-Network payment session state.
Runs on both sides of a paid request:

- **`--mode=receiver`** — orchestrator-side. Validates incoming
  `Payment` envelopes, tracks per-sender balances, and (post chain
  integration) redeems winning tickets on-chain. The capability-broker
  talks to this daemon over a unix socket via the `PayeeDaemon` gRPC
  service. Operator-only maintenance calls use the co-mounted
  `PayeeAdmin` gRPC service on the same socket.
- **`--mode=sender`** — gateway-side. Mints `Payment` envelopes for the
  paying app. Gateways and the conformance runner talk to this daemon
  over a unix socket via the `PayerDaemon` gRPC service. Callers may
  later report payee-side rejection outcomes back to the daemon so it
  can invalidate stale cached sessions.

Wire format and gRPC contracts at [`../livepeer-network-protocol/proto/livepeer/payments/v1/`](../livepeer-network-protocol/proto/livepeer/payments/v1/).
Operational reading: [`docs/operator-runbook.md`](./docs/operator-runbook.md).

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
- **Chain integration is available when `--chain-rpc` is set.** In dev
  mode the daemon still uses fake chain providers and a deterministic
  key; in production mode it validates against real chain state and runs
  the redemption pipeline.

Anything in [`docs/operator-runbook.md`](./docs/operator-runbook.md)
that talks about real funds, real gas, or real redemption is
**forward-looking**. Do not deposit real funds against a v0.2 daemon.

## Image

`tztcloud/livepeer-payment-daemon:<tag>`

## Run gestures

```sh
make build      # build dev image locally
make run        # foreground; sock at ./run/payment-daemon.sock
make test       # in-container go test ./...
make publish TAG=0.1.0   # multi-arch push (requires real TAG)
```

## Configuration

Flags:

| Flag | Default | Purpose |
|---|---|---|
| `--socket` | `/var/run/livepeer/payment-daemon.sock` | unix socket the gRPC server listens on |
| `--db` | `/var/lib/livepeer/payment-daemon/sessions.db` | BoltDB session ledger path |
| `--payee-admin-token` | empty | bearer token for receiver-only `PayeeAdmin` methods; falls back to `PAYEE_DAEMON_ADMIN_TOKEN` when unset |

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
