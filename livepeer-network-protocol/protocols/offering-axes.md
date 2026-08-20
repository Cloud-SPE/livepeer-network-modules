---
spec_name: offering-axes
version: 1.0.3-draft
status: draft
last_updated: 2026-08-20
---

# Offering declared axes

Under the v1 protocols, an offering no longer names an interaction mode from
an enumerated list. It names a **protocol** (`paid-job/v1` or
`paid-session/v1`) and declares the axes that used to be smuggled into mode
names, offering-name suffixes, and undocumented fields. Everything a consumer
previously inferred from a mode string is now a readable declaration in the
manifest capability tuple.

The design rule: **an axis is declared here iff some counterparty gates or
plans on it.** Seller-side implementation choices (which extractor counts
units, storage engines, backend paths) stay in host configuration and are
deliberately absent.

The key words MUST, MUST NOT, SHOULD, and MAY are to be interpreted as in
RFC 2119.

## 1. The `protocol` field

Replaces `interaction_mode`. Tag grammar `name/vN` (same as descriptor
schemas): currently `paid-job/v1` or `paid-session/v1`. Consumers gate on the
values they know and MUST refuse — not guess at — unknown protocols.

The old "engagement" question (one-shot vs long-lived, the only distinction
the clearinghouse ever needed) is answered by this field alone.

## 2. Axes for `paid-job/v1` offerings

Declared in the capability tuple's `job` object (REQUIRED for job offerings):

| Field | Req | Values / default | Who reads it |
|---|---|---|---|
| `transports` | yes | non-empty subset of `unary`, `stream`, `multipart` | Gateways select per-request; brokers refuse undeclared transports pre-payment. |

That is the entire job surface. Usage units are already declared by
`work_unit`; the extractor that counts them is host config; idempotency
windows are operator policy. One offering, many transports — the per-mode
offering duplication the old taxonomy forced is structurally gone.

## 3. Axes for `paid-session/v1` offerings

Declared in the capability tuple's `session` object (REQUIRED for session
offerings):

| Field | Req | Values / default | Who reads it |
|---|---|---|---|
| `descriptor_schema` | yes | `name/vN` tag | Gateways MUST NOT open sessions whose schema they don't implement; brokers reject runner descriptors that don't match it. |
| `attachment` | no | `external` (default) \| `inband-ws` | `external`: runtime coordinates come from the descriptor and data never transits the broker. `inband-ws`: the session's data plane is a broker-relayed WebSocket (the old `ws-realtime` shape). |
| `metering` | yes | `runner-reported` \| `broker-observed` | Where the seller's usage claims originate: runner events (§7 of the session spec) or broker-side observation of traffic it relays (only meaningful with `inband-ws` attachment). |
| `refill` | no | `extensible` (default) \| `bounded` | `bounded` offerings reject top-up after open. The clearinghouse gates refill on this instead of a mode-name list. |
| `heartbeat` | no | `{ interval_seconds, missed_threshold }`; defaults 10 / 3 | Gateways predict liveness enforcement; brokers enforce it. |
| `lease` | no | `{ policy, max_seconds }`; default policy `funding-tracking` | The session spec's normative lease default applies unless overridden here; gateways read it before opening. |
| `tolerance_band_pct` | no | number; advisory | The divergence tolerance the seller commits to operate within (trust-model doc). A buyer's route selection MAY prefer tighter bands. |
| `runway_increment_units` | no | integer; advisory | Seller-suggested top-up sizing. Buyers own their actual increment (it is their exposure bound), but a suggestion aids first-contact sizing. |
| `session_params_schema` | no | object; advisory | The runner's own description of the `session_params` it expects, relayed verbatim (see paid-session §7.1.1). Lets a gateway validate before opening rather than discovering the requirement as a create-time failure after payment was validated. Not operator-authored and never broker-enforced. |

`metering: broker-observed` with `attachment: external` is invalid: a broker
cannot observe traffic that never transits it.

### 3.1 `session.max_rotations`

Caps how many times a session may be rebound onto a rotated payment
identity (paid-session §3.3.1). Optional, default **3**.

The bound exists because a rebind costs the payer a funded envelope: an
unbounded rotate-and-rebind loop spends deposit without delivering work.
A broker also refuses a rebind whose predecessor generation delivered no
units at all, whatever this value says — that catches a loop after one
round rather than after `max_rotations` of them.

Consumers gate on nothing here; it is the broker's own bound, advertised
so an operator can see it.

## 4. Who consumes what

| Consumer | Gates on | Treats as opaque |
|---|---|---|
| Registry / coordinator / resolver | nothing (pass-through) | everything |
| Clearinghouse (LOC-shaped) | `protocol`; `session.refill` | all else |
| Gateway | `protocol`, `job.transports`, `session.descriptor_schema`, `session.attachment` | `extra`, `constraints` |
| Gateway (planning, not gating) | `session.heartbeat`, `session.lease`, `session.tolerance_band_pct`, `session.runway_increment_units` | — |
| Broker (self-validation) | everything it advertises — host config MUST be consistent with the declaration | — |

This table is the point of the exercise: the clearinghouse's hard-coded
`JOB_MODES` / `SESSION_OPEN_MODES` lists, its per-mode refill policy, and its
SDKs' per-mode top-up knowledge all collapse into two declared fields, and a
new capability with a new descriptor schema requires **zero** changes
anywhere except the two parties that actually speak it: the runner that emits
the descriptor and the gateway that consumes it.

## 5. Manifest schema changes

Applied to `manifest/schema.json` alongside this document:

- `interaction_mode` is **removed** (no deprecation period; pre-v1 consumers
  are extinct by decision of 2026-08-18).
- `protocol` is added, required, pattern `^[a-z][a-z0-9-]*/v[0-9]+$`.
- `job` and `session` axis objects are added with conditional requirements:
  a `paid-job/*` protocol requires `job` and forbids `session`; a
  `paid-session/*` protocol requires `session` and forbids `job`.
- `per_units` is added, optional, integer ≥ 1, absent means 1 (see §6).
- `work_unit`, `price_per_unit_wei`, `worker_url`, `extra`, `constraints`
  are unchanged.

The manifest `spec_version` major MUST be bumped when this lands: the removal
of `interaction_mode` is breaking by design.

## 6. Price and its denominator

An offering's price is a **pair**, and reading either half alone is a
billing error:

- `price_per_unit_wei` — a non-negative decimal string, so precision is
  not capped by JSON's safe-integer range.
- `per_units` — the denominator. The price buys this many work units.
  Optional; absent or `0` means `1`.

The denominator exists because `price_per_unit_wei` is an integer count of
wei. Without it, no offering can price below 1 wei per unit, which is the
normal case for token-metered workloads. It is not a display convenience:
every layer that touches money MUST carry it, and a layer that drops it
bills `per_units` times the intended rate.

### 6.1 The cumulative billing rule

For `U` **cumulative** work units delivered since the session opened (for
`paid-job/v1`, the units of the single exchange):

```
bill(U) = ceil(U × price_per_unit_wei / per_units)
```

- **Ceiling**, so a payee is never left short on work already delivered.
- **Cumulative**, and this is the load-bearing half. A debit or a refill
  costs `bill(U_after) − bill(U_before)`. Rounding each increment on its
  own would cost the payer up to one wei per increment and — far worse —
  would make two honest implementations disagree, because the total then
  depends on how the work happened to be chunked.

Both sides of a session MUST compute this identical function. It is
written here, once, so neither re-derives it.

### 6.2 Pinning

`price_per_unit_wei` and `per_units` are pinned at session open for the
life of the session, exactly as face value is. A price change on the
offering applies to sessions opened after it; it never moves an open
session's cumulative curve.

### 6.3 Wire names

The same value travels under three names, two of them historical. They
are the same number and MUST agree:

| Layer | Field |
|---|---|
| Manifest tuple, broker offerings doc | `per_units` |
| Resolver `SelectedRoute` | `units_per_price` |
| Payment envelope `PriceInfo` | `pixels_per_unit` |

`pixels_per_unit` is go-livepeer's name and is not being renamed: the
field number is the compatibility surface. A payment whose denominator
disagrees with the offering's is an envelope mismatch and MUST be
refused.

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.0.3-draft | 2026-08-20 | Add §3.1 `session.max_rotations`, the bound on rebinding a session onto a rotated payment identity (paid-session §3.3.1). |
| 1.0.2-draft | 2026-08-20 | Add §6: price is a `(price_per_unit_wei, per_units)` pair, the cumulative ceiling billing rule that both sides compute identically, pinning at session open, and the three wire names for the denominator. Written normatively because the reference implementation had it in the catalog, the settlement record, and nowhere in the ledger. |
| 1.0.1-draft | 2026-08-19 | Add the advisory `session.session_params_schema` axis (see paid-session §7.1.1), carried by `manifest/schema.json` and relayed opaquely by `orch-coordinator`, so it reaches gateways through the signed manifest as well as the broker's `/registry/offerings`. |
| 1.0.0-draft | 2026-08-18 | Initial axes. Replaces mode-name inference; `interaction_mode` removed from the manifest. |
