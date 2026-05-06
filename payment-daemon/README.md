# `payment-daemon/`

Long-lived sidecar that owns the Livepeer-Network payment session state.
Runs on both sides of a paid request:

- **`--mode=receiver`** — orchestrator-side. Validates incoming
  `Payment` envelopes, tracks per-sender balances, and (post chain
  integration) redeems winning tickets on-chain. The capability-broker
  talks to this daemon over a unix socket via the `PayeeDaemon` gRPC
  service.
- **`--mode=sender`** — gateway-side. Mints `Payment` envelopes for the
  paying app. Gateways and the conformance runner talk to this daemon
  over a unix socket via the `PayerDaemon` gRPC service.

Wire format and gRPC contracts at [`../livepeer-network-protocol/proto/livepeer/payments/v1/`](../livepeer-network-protocol/proto/livepeer/payments/v1/).
Operational reading: [`docs/operator-runbook.md`](./docs/operator-runbook.md).

## Status (v0.2 — wire-compat + sender, chain stubbed)

- Both `sender` and `receiver` modes wire up. One binary, mode chosen
  at boot.
- `Payment` wire format is byte-compatible with go-livepeer's
  `net.Payment` per [`wire-compat.md`](../livepeer-network-protocol/docs/wire-compat.md);
  envelopes from this daemon decode against go-livepeer's `pm/`.
- Receiver sessions persist to BoltDB
  (`/var/lib/livepeer/payment-daemon/sessions.db`).
- **Cryptography is stubbed.** Sender signs with a deterministic
  dev-mode key (no chain RPC, no real keystore). Receiver accepts any
  well-formed `Payment` bytes and credits zero EV.
- **Chain integration deferred.** No Arbitrum, no go-ethereum, no real
  redemption submissions. Provider interfaces are in place; plan 0016
  swaps in real chain implementations behind them.

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
                 │  BoltDB sessions.db          │
                 └──────────────────────────────┘
```

The unix socket is the trust boundary: only processes with filesystem
access to the socket can call the daemon. The shared volume between the
broker container and the daemon container is the docker-level realization
of that boundary.
