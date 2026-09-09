# Bind paid-session settlements to the accepted quote

Bead: `lnm-yzn`

## Problem

The broker validates the gateway's accepted quote at session admission, but
does not retain that quote in durable session state. Consequently its signed
`SettlementRecord` omits `accepted_quote_ref`; an independent clearinghouse
cannot prove that the terminal debit used the route and price it selected and
must reject the record.

## Invariants

- The quote is fixed by the accepted payment or spend authorization, never by
  runner input.
- The exact quote reference survives broker restart and payment-identity
  rotation.
- Both legacy ticket funding and wholesale account authorization produce the
  same signed settlement binding.
- Old durable records without quote fields remain readable, but their
  settlements remain unverifiable rather than inventing a quote.

## Implementation

1. Extract the already-validated quote reference at the HTTP admission edge.
2. Pass it into the session engine and copy its primitive fields into the
   durable session record.
3. Reconstruct `accepted_quote_ref` in every interim and terminal settlement.
4. Add engine/store and HTTP coverage, then run broker, protocol, and LOC's
   production-shaped wholesale rollout conformance gate.

## Completion

Move this plan to `completed/` only after the cross-repository rollout gate
passes against clean immutable LOC and Modules revisions.
