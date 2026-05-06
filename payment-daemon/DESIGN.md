# DESIGN — payment-daemon

## Why a separate process

In production this daemon will hold the orchestrator's warm signing key
and own the chain-integrated redemption pipeline (probabilistic-
micropayment ticket validation, Arbitrum interaction, cold-key escalation).
Embedding any of that inside the broker would couple the broker's release
cadence and process lifecycle to chain operations and key handling — both
of which need a separate trust boundary.

v0.1 contains none of that, but the architectural separation lands here so
the rest of the system (broker, conformance, gateway) is built against the
out-of-process boundary from day one.

## Boundaries

- **Inbound:** gRPC over a unix socket. The broker is the only caller in
  v0.1. The socket's filesystem permissions are the trust boundary; in
  Docker, the shared volume between broker + daemon containers is what
  realizes that.
- **Outbound:** none in v0.1. Future: chain RPC (Arbitrum), maybe a
  metrics scrape endpoint.
- **State:** BoltDB file at a configured path. Single-writer (this
  process). The daemon owns its file lock and refuses to start if the
  file is locked by another process.

## RPCs

See [`../livepeer-network-protocol/proto/livepeer/payments/v1/payee_daemon.proto`](../livepeer-network-protocol/proto/livepeer/payments/v1/payee_daemon.proto).

The session lifecycle: `OpenSession → Debit* → Reconcile → CloseSession`.
`Health` is out-of-band and called once at broker boot.

## BoltDB layout

One bucket: `sessions`. Keys are session IDs (16-byte hex). Values are
JSON-encoded session records:

```go
type Session struct {
    ID            string    `json:"id"`
    CapabilityID  string    `json:"capability_id"`
    OfferingID    string    `json:"offering_id"`
    Ticket        []byte    `json:"ticket"`        // opaque
    EstimatedMax  uint64    `json:"estimated_max"` // from envelope
    OpenedAt      time.Time `json:"opened_at"`
    Debits        []uint64  `json:"debits"`
    ActualUnits   *uint64   `json:"actual_units"`  // nil until Reconcile
    Closed        bool      `json:"closed"`
    ClosedAt      time.Time `json:"closed_at"`
}
```

JSON over gob/binary because human-readable when debugging and the volume
is low.

## Failure modes

| Caller mistake | Response |
|---|---|
| OpenSession with empty ticket | `InvalidArgument: ticket is empty` |
| OpenSession with capability_id mismatch | `InvalidArgument: capability_id mismatch` |
| OpenSession with offering_id mismatch | `InvalidArgument: offering_id mismatch` |
| OpenSession with expected_max_units == 0 | `InvalidArgument: expected_max_units must be > 0` |
| Debit / Reconcile / Close on unknown session | `NotFound: session not found` |
| Debit / Reconcile after Close | `FailedPrecondition: session is closed` |
| BoltDB write fails | `Internal: <bolt error>` |

The broker maps these back to Livepeer error codes
(`payment_invalid` / `payment_envelope_mismatch` / `internal_error`).

## What's deliberately NOT here

- **Sender-side gRPC.** v0.1 ships only the receiver. Gateways encode
  envelopes locally using the generated bindings (Go) or a small
  hand-rolled encoder (TS).
- **Chain integration.** `Ticket` is opaque bytes, not parsed.
- **Warm-key handling.** No key material is read or held.
- **Interim-debit cadence.** The gRPC surface allows multiple `Debit`
  calls per session, but v0.1 callers issue exactly one.

Each of these is its own follow-up plan; the wire format and gRPC surface
in this plan are forward-compatible with all of them.
