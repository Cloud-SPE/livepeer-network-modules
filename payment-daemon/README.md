# `payment-daemon/`

Long-lived sidecar that owns the Livepeer-Network payment session state on
the orchestrator host. The capability-broker talks to this daemon over a
unix socket using the gRPC service defined at
[`../livepeer-network-protocol/proto/livepeer/payments/v1/payee_daemon.proto`](../livepeer-network-protocol/proto/livepeer/payments/v1/payee_daemon.proto).

## Status (v0.1)

- Receiver mode only.
- Sessions persist to BoltDB (`/var/lib/livepeer/payment-daemon/sessions.db`).
- Ticket validation is a no-op: any non-empty `ticket` bytes are accepted.
- No chain integration. No warm-key handling.
- Sender mode (envelope minting) is co-located with each gateway in v0.1
  (see `../openai-gateway/src/livepeer/` and the conformance runner). A
  proper sender-side gRPC surface is a follow-up.

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
