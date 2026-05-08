# Plan 0021 — VTuber session top-up via control-WS refill

**Status:** completed  
**Opened:** 2026-05-08  
**Owner:** harness  
**Related:** `vtuber-gateway/`, `capability-broker/`, `payment-daemon/`, `livepeer-network-protocol/`, plan 0013-vtuber, plan 0015 (interim debit)

## 1. Why this plan exists

`vtuber-gateway` now routes session-open through the broker's
`session-control-plus-media@v0` mode and uses resolver-driven worker selection.
That fixed the stale static-broker shape, but it also made one remaining gap
more obvious:

- `POST /v1/vtuber/sessions/:id/topup` exists
- the gateway can mint a fresh `Livepeer-Payment`
- but the broker mode does not yet expose a refill/top-up operation the gateway
  can use to apply that new payment to an already-open session

Other gateway flows already have a coherent payment/session lifecycle. VTuber now
needs the same thing at the mode + broker layer, not a gateway-local workaround.

## 2. Current broken behavior

Today the system is split:

1. `vtuber-gateway` emits one payment at session-open
2. broker session stays open and drives `session.usage.tick` / balance events
3. `vtuber-gateway` top-up route mints another payment
4. but there is no broker-side API that accepts that refill for the active session

The current gateway-side top-up route is therefore not a real end-to-end refill.

## 3. Goal

Make VTuber top-up a first-class network operation:

1. gateway mints a new payment for the same orch/offering/session
2. gateway sends a protocol-defined refill message to the active session
3. broker forwards that refill message on the existing control path
4. backend emits a refill confirmation event
5. gateway surfaces success to the caller

This should look and feel like the rest of the Livepeer payment/session flows:
strict validation, explicit state transitions, and protocol-defined events.

## 4. Scope

### In scope

- Extend `session-control-plus-media@v0` with a refill/top-up operation.
- Wire `vtuber-gateway` to send that operation over the existing control WebSocket.
- Wire `vtuber-gateway` top-up to that broker operation.
- Add tests for refill success and the outbound control-envelope shape.
- Update protocol docs and operator docs.

### Not in scope

- Redesigning the entire VTuber balance model.
- Changing initial session-open payment semantics.
- Reworking Stripe/customer-wallet UX.
- Generalizing this to all session modes in the same patch unless the same primitive
  is immediately reusable.

## 5. Proposed wire contract

The least disruptive shape is a control-plane message on the existing active
session.

### Gateway → broker

Control WebSocket envelope:

```json
{
  "type": "session.topup",
  "body": {
    "payment_header": "<base64 Livepeer-Payment>"
  }
}
```

Why this shape:

- stays inside the existing `session-control-plus-media@v0` control plane
- keeps refill scoped to an already-open `session_id`
- avoids inventing a second session-specific REST endpoint unless later needed

### Backend → gateway

Success:

```json
{
  "type": "session.balance.refilled",
  "body": {
    "accepted": true
  }
}
```

Failure:

```json
{
  "type": "session.error",
  "body": {
    "code": "payment_invalid"
  }
}
```

The exact body shape can remain minimal in v1. The important invariant is that
the gateway and customer control surface have a standard refill message.

## 6. Broker-side behavior

For `session.topup`, the broker should:

1. accept the envelope on the existing control WebSocket
2. preserve the `payment_header` payload
3. forward it through the session-control path like other capability-scoped
   control commands
4. leave session-specific refill semantics to the backend implementation

The broker must reject refill attempts when:

- session is already ended or closing
- control path is unavailable

## 7. Gateway-side behavior

`vtuber-gateway` top-up route should:

1. authenticate the customer session bearer
2. mint a fresh payment with the existing session's orch/offering data
3. open the broker control path for that session
4. send `session.topup`
5. return success once the control envelope has been delivered

This matches the existing VTuber control-plane model more closely than trying to
invent a second broker-side payment ledger just for mid-session refill.

## 8. Payment-daemon implications

This plan assumes the existing sender-side daemon remains the payment envelope owner.
The gateway still asks `payment-daemon` to create the refill payment. The new work is
at the gateway + mode-spec layer, not inside sender-side minting.

## 9. Test matrix

Minimum tests:

- worker client sends `session.topup` with `payment_header`
- gateway route still rejects inactive sessions
- gateway route returns 200 after payment mint + control send succeed
- gateway route returns 502 when control send fails

## 10. Implementation order

### Phase 1 — protocol doc

- Update `livepeer-network-protocol/modes/session-control-plus-media.md`
- define `session.topup`
- define expected broker reply semantics

### Phase 2 — gateway implementation

- replace the VTuber top-up placeholder with a real `session.topup` control send
- add unit coverage for the outbound envelope
- keep the broker on its existing transparent control relay path

### Phase 3 — docs

- update `vtuber-gateway` docs
- document the shared `session.topup` control contract

## 11. Exit criteria

- VTuber top-up is a real session-control operation
- `vtuber-gateway` no longer carries a placeholder top-up path
- protocol doc is updated
- tests pass across gateway

## 12. Risks

- The backend still owns the actual refill semantics. If it needs explicit
  acknowledgement, that should arrive as a workload/broker event on the same
  control WebSocket instead of a second gateway-specific API.
