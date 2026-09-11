---
spec_name: offering-axes
version: 1.1.0-draft
status: draft
last_updated: 2026-09-11
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
`work_unit`; the extractor that counts them is declared by the runner at
attach (`runner-attach.md` §3.2) and frozen into the offer, never
advertised; idempotency windows are operator policy. One offering, many transports — the per-mode
offering duplication the old taxonomy forced is structurally gone.

## 3. Axes for `paid-session/v1` offerings

Declared in the capability tuple's `session` object (REQUIRED for session
offerings):

| Field | Req | Values / default | Who reads it |
|---|---|---|---|
| `descriptor_schema` | yes | `name/vN` tag | Gateways MUST NOT open sessions whose schema they don't implement; brokers reject runner descriptors that don't match it. |
| `attachment` | no | `external` (the only value) | Runtime coordinates come from the descriptor and data never transits the broker. The caller connects to the runner directly, at the address the descriptor publishes; a pool member serving a session workload therefore exposes a public endpoint (the pool's member-edge feature). A broker-relayed data plane (`inband-ws`) was declared here until 2026-09-02 and never implemented; it is gone rather than pending. |
| `metering` | yes | `runner-reported` (the only value) | The seller's usage claims originate as runner events (§7 of the session spec). `broker-observed` was declared until 2026-09-02 for a relayed data plane that was never built; with no traffic transiting the broker there is nothing for it to observe. |
| `refill` | no | `extensible` (default) \| `bounded` | `bounded` offerings reject top-up after open. The clearinghouse gates refill on this instead of a mode-name list. |
| `heartbeat` | no | `{ interval_seconds, missed_threshold }`; defaults 10 / 3 | Gateways predict liveness enforcement; brokers enforce it. |
| `lease` | no | `{ policy, max_seconds }`; default policy `funding-tracking` | The session spec's normative lease default applies unless overridden here; gateways read it before opening. |
| `tolerance_band_pct` | no | number; advisory | The divergence tolerance the seller commits to operate within (trust-model doc). A buyer's route selection MAY prefer tighter bands. |
| `runway_increment_units` | no | integer; advisory | Seller-suggested top-up sizing. Buyers own their actual increment (it is their exposure bound), but a suggestion aids first-contact sizing. |
| `session_params_schema` | no | object; advisory | The runner's own description of the `session_params` it expects, relayed verbatim from the runner's attach document (`runner-attach.md` §3.2). Lets a gateway validate before opening rather than discovering the requirement as a create-time failure after payment was validated. Not operator-authored and never broker-enforced. |

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

For `U` cumulative work units delivered under one job authorization or one
logical session authorization chain:

```
bill(U) = ceil(U × price_per_unit_wei / per_units)
```

- **Ceiling**, so a payee is never left short on work already delivered.
- **Cumulative**, and this is the load-bearing half. An authorization advance
  costs `bill(U_after) − bill(U_before)`. Rounding each increment on its
  own would cost the payer up to one wei per increment and — far worse —
  would make two honest implementations disagree, because the total then
  depends on how the work happened to be chunked.

Both payer and payee MUST compute this identical function. For a paid job,

```text
billed_value_wei = bill(actual_units)
```

For a paid session, every usage event and successor authorization stays on the
same logical cumulative curve. The terminal billed value is `bill(total
debited_units)`; event boundaries and authorization revisions do not introduce
additional ceilings.

The curve does **not** span unrelated jobs merely because they share a payer,
payee, account, funding generation, capability, or offering. Stable accounts
aggregate value, not workload identity. `payment_cumulative_units` is a
historical ticket-session field and MUST be zero in authorization-only
settlements.

**A settlement MUST attest what the account ledger charged** and carry enough
authorization, quote, units, and account-version data for the payer to verify
it.

### 6.2 Pinning

`price_per_unit_wei` and `per_units` are pinned at session open for the
life of the session. A price change on the
offering applies to sessions opened after it; it never moves an open
session's cumulative curve.

The accepted price and denominator are pinned by the job authorization or the
first session authorization. A successor session authorization MUST carry the
same curve. New offering prices apply only to new authorization chains.

Ticket recipient rand is a funding-generation identity only. It may remain
stable across many account replenishments or rotate without changing any job
or session identity. Payers key workload state on authorization IDs and
account state on `(chain, payer, payee, denomination)`, never on recipient rand
or funded value.

Ticket face value and win probability are not identity and are not pinned. A
payer re-quotes the target expected value for each replenishment while the
payee preserves recipient rand. The payee MUST retain an economically
redeemable winning face and vary win probability to represent the target EV;
shrinking the winning face for a tiny shortfall transfers redemption cost to
the payee and is not fair funding. Each signed ticket carries its own values,
so earlier tickets retain their original economics. A payer MUST require the
resulting aggregate EV to equal the funding intent or refuse before signing.

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
| 1.1.0-draft | 2026-09-11 | Moves the cumulative billing curve from shared ticket-session identity to one job authorization or logical session authorization chain. Removes `session.max_rotations`; recipient rand now identifies funding generations only. |
| 1.0.10-draft | 2026-09-09 | §6.2 makes exact-EV sizing economically symmetric: the payee retains a redeemable winning face and varies win probability. Shrinking face value to a small account shortfall avoided payer surplus only by creating payee-side uneconomic winners. |
| 1.0.9-draft | 2026-09-09 | §6.2 corrects stale face-value pinning: face value is mutable per-replenishment sizing, not session identity. Payers re-quote in both directions under the stable recipient rand and refuse any signed-EV mismatch, preventing both nonce-exhausting underfund and surplus account float. |
| 1.0.8-draft | 2026-09-02 | `attachment: inband-ws` and `metering: broker-observed` removed (plan 0045, decision 13 of the 2026-09-02 walkthrough). Both were accepted by brokers and served by none: the broker's session WebSocket is the §8 control socket, not a media relay. Every session data plane is external; a pool member exposes it through the pool's member-edge feature. The enums keep one value each so a future value is an addition, not a redefinition. |
| 1.0.7-draft | 2026-08-26 | Runner-owned axes (`transports`, `descriptor_schema`, `metering`, `work_unit`, `session_params_schema`, the extractor) now originate in the attach document (`runner-attach.md`) and reach the manifest only through the offer freeze; references to paid-session §7.1.1 repointed. No wire change. |
| 1.0.6-draft | 2026-08-21 | §6.1: add `payment_cumulative_units` — the running total on the `work_id`, distinct from the session- or exchange-scoped `debited_units` — and state what it does and does not make verifiable: a paid-job charge is fully recomputable from the record, a paid-session charge on a shared identity is attested rather than recomputable, because interleaved sessions do not occupy contiguous stretches of the curve. |
| 1.0.5-draft | 2026-08-21 | §6.1: the cumulative curve spans EXCHANGES, not only ticks — a paid-job exchange is one increment on its payment session's curve, so the second job on a session costs the difference of two ceilings. A settlement MUST attest what the ledger charged rather than recomputing, and carries the cumulative total so the charge stays verifiable from the record. Found on mainnet: a signed record attested 5 wei for a debit the ledger charged 4. |
| 1.0.4-draft | 2026-08-20 | §6.2: state the identity invariants explicitly — refill sizing never changes session identity, a payer must not key its session cache on funded value, face value is pinned at first issuance and a larger refill mints more tickets rather than larger ones. All three were true of the implementation by accident and written down nowhere. |
| 1.0.3-draft | 2026-08-20 | Add §3.1 `session.max_rotations`, the bound on rebinding a session onto a rotated payment identity (paid-session §3.3.1). |
| 1.0.2-draft | 2026-08-20 | Add §6: price is a `(price_per_unit_wei, per_units)` pair, the cumulative ceiling billing rule that both sides compute identically, pinning at session open, and the three wire names for the denominator. Written normatively because the reference implementation had it in the catalog, the settlement record, and nowhere in the ledger. |
| 1.0.1-draft | 2026-08-19 | Add the advisory `session.session_params_schema` axis (see paid-session §7.1.1), carried by `manifest/schema.json` and relayed opaquely by `orch-coordinator`, so it reaches gateways through the signed manifest as well as the broker's `/registry/offerings`. |
| 1.0.0-draft | 2026-08-18 | Initial axes. Replaces mode-name inference; `interaction_mode` removed from the manifest. |
