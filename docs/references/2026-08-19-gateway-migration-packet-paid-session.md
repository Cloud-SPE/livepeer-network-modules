# Gateway migration packet — vtuber + daydream → paid-session/v1

Date: 2026-08-19. For the teams behind `livepeer-modules-vtuber` and
`livepeer-modules-daydream-gateway`. Based on a read of both codebases on
2026-08-18.

## Why you two get one packet

You solved the same problem in opposite directions, and both answers were
rational given what the contract offered:

- **daydream** used the broker as a real session authority — `/v1/cap`,
  `Livepeer-*` headers, a control WS — and is pinned to
  `session-control-external-media@v0`, a mode string that **no longer exists
  in any registry**. It works because the broker it talks to still answers,
  not because anything guarantees it will.
- **vtuber** skipped the broker entirely. No mode string, no `Livepeer-*`
  headers, no `/v1/cap` anywhere: the gateway holds session authority, mints
  its own worker bearers, and speaks a private WebSocket protocol straight
  to the runner with the payment envelope base64'd inside a frame.

Vtuber's choice is the more informative one. When a contract doesn't fit, a
team doesn't bend it — they route around it and reimplement it, usually
worse. `synthesizeStaticRoute` fabricating an eth address of `0x0000…`, a
price of `"0"`, and a self-computed quote id is not a hack anyone wanted to
write; it's what "the broker doesn't fit my shape" costs.

The v1 goal is narrow: **make the broker cheap enough to use that bypassing
it stops being rational.** Below is what that buys each of you.

## What you can delete

| What you built | Why | What replaces it |
|---|---|---|
| **Encrypted bearers at rest** (vtuber: an entire ADR + migration `0003_session_bearer_at_rest.sql` decrypting `vtbsw_*` tokens on boot so sessions survive a gateway restart) | No way to re-authenticate to a live session after restart | A **session credential** issued once at open. `(session_id, credential)` is your entire continuity state — it survives *both* sides' restarts and authenticates status, top-up, end, and the control-WS. No vault, no boot resumer. |
| **`attached \| restored \| detached` tri-state + `/reattach`** (daydream) plus session state duplicated three ways (in-process map, JSON file, SQLite) | Same root cause, different workaround | Same fix. The broker's own authority is durable now, so reattaching is presenting a credential, not reconstructing a relationship. |
| **Route rehydration for top-ups** (vtuber `routeFromSession()`: rebuilding a synthetic `SelectedRoute` from ten denormalized DB columns; 503 if any is null) | Top-up needed quote context the gateway had to reassemble | Top-up is `POST /v1/session/{id}/topup` with the credential and a new envelope. The broker holds the binding; you hold nothing but the credential. |
| **Bespoke usage callback** (vtuber: runner POSTs `{seconds, work_units}` to `gatewayUsageUrl` every ~1s, stored for analytics only, with `seconds: max(1, round(elapsed))` drift because a zod schema rejects `0`) | No usage channel existed on the contract | Cumulative usage claims ride the protocol's own event channel (runner → broker), and reach you via status or pushed `session.usage.tick` frames. Cumulative totals mean rounding drift can't accumulate. |
| **Dropped signals** (daydream: `session.usage.tick`, `session.balance.low`, `session.refilled` parsed, debug-logged, and thrown away) | Nothing downstream could act on them | They're now normative and actionable: the `balance` object carries `runway_units`, `runway_seconds_estimate`, and `will_refuse_next_refill` — a one-refill-ahead winddown warning you can drain against instead of discovering refusal mid-session. |
| **Topups into the void** (daydream: mints payment and returns `202 {topup_minted: true, delivered_via_control_ws: false}` when the WS isn't attached — a paid envelope thrown away) | Top-up delivery depended on a channel that might not exist | Top-up is an HTTP verb that either credits the session and extends the lease, or refuses with `refill_refused` *before* taking payment. The WS is optional; nothing depends on it being attached. |
| **Topup that doesn't extend the lease** (both repos: funding and `expires_at` unlinked) | Not modeled | Normative: a successful top-up extends the lease, and never shortens it. Lease expiry has a one-heartbeat grace so an in-flight top-up can't lose a race to the sweeper. |
| **`params.extras` smuggling** (vtuber: egress URL + signed auth minted out of band and stuffed into an opaque blob) | Buyer-supplied credentials had no home | They have a documented home: `session_params`, passed to the runner verbatim, never interpreted, logged, or relayed. The rule is explicit in `descriptors/trickle-egress.md`: descriptors describe the *runner's* runtime; buyer infrastructure credentials travel the other way in params. |
| **Unauthenticated scope URL** (daydream: `media.scope_url` treated as an open capability URL; control WS opened with no `Authorization` header at all) | No credential existed to present | `scope-passthrough/v1` makes API access itself a granted operation: the grant secret is the bearer for every call to `scope_url`. |
| **Recovery-by-audit-log** (daydream: runtime health reconstructed from browser-reported events posted to `/v1/live-events`, replayed from an audit log with CSV export, 15 bespoke `session_live_*` action strings) | No broker-side health or usage signal existed | Session state, usage, lease, and terminal reason are queryable from status; liveness is enforced broker-side by heartbeat with a stable `close_reason`. |
| **Polling everywhere** (vtuber: 60s expiry sweeper, per-second usage POSTs, first-usage-report as the "session is live" signal) | No push channel | Optional control-WS pushes `session.usage.tick`, `session.balance`, `session.ended`. HTTP remains authoritative — attach for latency, not for correctness. |

## Your descriptor schemas exist

Workload identity now lives in **runtime-descriptor schemas**, not protocol
names — which is why there is no `vtuber-session@v0` or
`daydream-scope@v0` and never will be. Two schemas were written from your
current shapes:

- **`descriptors/trickle-egress/v1`** — vtuber. Public: `control_url`
  (runner-hosted control attach point), optional `preview_url` and
  `status_url`. Grant: `control-attach`, which authenticates your attach
  *and every re-attach after a restart*. The egress destination stays
  buyer-supplied in `session_params`.
- **`descriptors/scope-passthrough/v1`** — daydream. Public: `scope_url`,
  optional `status_url`. Grant: `scope-api-access`, the bearer for proxied
  calls.

Both are drafts written from the outside. **They are capability-owned** —
you propose changes, and a schema change costs no broker, clearinghouse, or
registry work. That is the whole point of the factoring: adding or changing
a workload's coordinates touches exactly two parties, the runner that emits
them and the gateway that consumes them.

## What the migration actually looks like

For **daydream**, it's mostly renaming into a contract that now exists:
`POST /v1/session` with `Livepeer-Protocol: paid-session/v1` and a required
`Livepeer-Request-Id`; session-open takes a real body (`session_params`), so
workload config stops being smuggled through `/api/v1/*` after the fact; the
open response carries the descriptor, the credential, the lease, the balance
object, and control URLs. Your payer-side call also needs to move off the
legacy `faceValue + recipient + capability + offering` shape — vtuber
already did this migration; their plan `0003-daemon-contract-alignment`
is the reference.

For **vtuber**, it's larger but subtractive: the broker becomes the session
authority you were emulating. You keep your product surface (api keys,
concurrency caps, the customer control relay, the persona/VRM params); you
delete the parts that exist only because you had to be your own broker —
route synthesis, bearer vaults, quote-column denormalization, the private
control protocol. The runner keeps its usage measurement; it just reports
cumulative totals on the standard event channel instead of a bespoke
callback URL, with retries safe by contract (exactly-once debit is the
broker's problem).

## The no-broker case: decided, and cheaper than it looks

Vtuber's design has one property v1 does not replicate: it works with
**no broker at all**, via `synthesizeStaticRoute` — fabricating a route
with eth address `0x0000…0000`, price `"0"`, and self-computed quote and
fingerprint values so the payer call has the right shape when no registry
daemon is running. That gives you `docker compose up` with gateway plus
runner and nothing else.

`paid-session/v1` is paid by construction: the engine validates payment
before binding a runtime and fails closed otherwise. We are **not** adding
an unpaid path, for three reasons:

- A mode where sessions run without payment authority is exactly what
  fail-closed exists to prevent, and anything that exists for dev
  eventually gets misconfigured into production.
- It would be a second code path through the session engine — the thing
  this whole redesign exists to remove.
- It is not actually needed for your use case (below).

**What to do instead: add one container.** The broker runs standalone with
an in-process mock payment client. No registry daemon, no payment daemon,
no wallet, no chain:

```yaml
# host-config.yaml — local development only
payment_daemon:
  mock: true
  # Optional: makes the mock ledger survive a broker restart, so you can
  # exercise session rebind locally the way production behaves.
  mock_state_path: /var/lib/livepeer/payment-mock.json
```

Your dev stack goes from two processes to three: runner, broker, gateway.
Point the gateway at the broker's URL instead of resolving a route, and
you can drive the full product locally including real session lifecycle,
usage claims, top-ups, and restart recovery.

This is not a theoretical claim: our conformance suite runs exactly this
configuration — mock payment, no registry, no wallet — and executes 32
scenarios against it, including broker restart with session rebind.

The trade is one container against deleting `synthesizeStaticRoute`, a
fabricated zero-address route that exists only to satisfy a payment API.
We think that is a good trade, but if running a broker in your dev loop
turns out to be a genuine problem rather than an inconvenience, tell us
and we will discuss it.

## What we need from you

1. **Review your descriptor schema** against your actual runtime —
   `trickle-egress/v1` and `scope-passthrough/v1` are the drafts; field
   names, extra coordinates, and grant semantics are yours to shape.
2. **Vtuber: confirm the metering unit and cadence.** Today `WEI_PER_WORK_UNIT`
   is defined and documented as "currently not used in code," and usage
   feeds analytics only. In v1 usage claims drive debits, so the unit and
   its price become real.
3. **Daydream: confirm the payer-contract migration path** — you're on the
   pre-quote shape and it has to move regardless of this work.
4. **Vtuber: confirm the one-extra-container dev loop works for you** — see
   the no-broker section above. If it genuinely does not, that is a
   conversation, not a silent workaround.
