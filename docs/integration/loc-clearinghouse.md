# Clearinghouse integration

A clearinghouse is the wholesale payer and settlement consumer while an end
user may invoke the broker directly. The clearinghouse owns account funding,
route selection, spend authorization, and reconciliation; it does not need to
proxy workload bytes.

## 1. Fund aggregate wholesale float

Maintain a bounded target balance for each stable
`(chain, payer, payee, denomination)` account. Query the selected broker's
TLS-bound account view, calculate:

```text
shortfall = max(0, target_available - observed_available)
```

and mint only that shortfall. Fund through
`POST /v1/payment/account/fund`, or attach the shortfall payment to a valid
authorization. Tickets are account-funding instruments. They are not tied to
one end user, request maximum, capability, or session, and they never authorize
work.

The target float is an operating policy based on expected aggregate burn,
replenishment latency, concurrency, route-exit tolerance, and acceptable
service interruption. It should not grow with the maximum token or media
duration declared by every request.

## 2. Authorize one invocation

After route and quote selection, mint a single-purpose
`SpendAuthorization`. For a job, bind the caller-selected
`Livepeer-Request-Id` and exact request digest. For a session, bind the
globally unique `gateway_session_id`, exact open digest, cumulative maximum,
and optional delegated caller key.

The user receives the broker URL and authorization. If the authorization has a
`caller_public_key`, the user proves possession on each invocation with
`Livepeer-Caller-Proof`. A raw HTTP implementation is allowed; the SDK only
constructs and reports the same protocol messages.

Every successor session authorization names its predecessor. A payment alone,
an old authorization, or a generic bearer credential cannot start unrelated
work.

## 3. Bind settlement to clearinghouse state

Verify the broker's signed `SettlementRecord` using the delegated settlement
key from the signed registry manifest. Verify the received canonical payload;
do not repair or re-serialize it before signature verification.

| Path | Clearinghouse binding |
|---|---|
| paid job | `request_id` and `authorization_id` |
| paid session | `gateway_session_id` and current authorization chain |

`job_id` and `session_id` are broker identifiers useful for lookup.
`work_id` carries the authorization ID in authorization-only settlements; it
is not a shared ticket-session account.

Also require the pinned `accepted_quote_ref`, route fingerprint, capability,
offering, work unit, payer, payee, chain, denomination, and account version to
match the clearinghouse record.

## 4. Reconcile wholesale and retail independently

The broker's `billed_value_wei` and `debited_units` describe wholesale
settlement against the stable payer-payee account. Verify billed value using
the pinned cumulative price curve. Funding, reservation, debit, and release
are separate quantities; neither a large authorization maximum nor a funding
ticket is customer usage.

Retail USD billing is a separate clearinghouse ledger. Apply the product's
retail rate, minimum, discounts, and customer balance to the independently
recorded retail usage. Reconcile that customer record to the wholesale
`request_id` or `gateway_session_id`; never make the orchestrator's account
key customer-specific.

## 5. Reconcile without trusting callback delivery

```text
GET /v1/exchange/{request_id}
GET /v1/settlement/{job_id-or-session-id}
POST /v1/non-admission/{request_id}
POST /v1/payment/account
```

Response trailers, SDK callbacks, and user reports are latency optimizations.
The clearinghouse can query the locked broker directly using identifiers it
issued.

- A valid terminal settlement releases the authorization encumbrance and is
  booked exactly once.
- `accounting_pending` remains encumbered and is polled; absence is not zero
  usage.
- A signed `expired_unused`/non-admission outcome for the same authorization
  permits release of that unused authorization hold.
- A missing or unverifiable outcome remains unresolved under clearinghouse
  policy; it must not be silently rewritten as settled.

Ticket expiry is relevant only to funding-intent reconciliation. It does not
decide whether a workload authorization was admitted or settled.

## 6. Restart and route change

Persist selected quote bindings, authorization bytes and predecessor chain,
request/session IDs, account observations, and the highest accepted settlement
version before handing authority to a user.

After restart, recover by querying the broker and payer daemon. Do not mint a
second authorization for uncertain work until the first authorization's state
is known. Stop funding a route before deselection, drain admitted work, and
consume its bounded residual float where practical; v1 does not promise
cross-payee transfer or automatic cash refund.

The payer sender and registry resolver are trusted sidecars intended for
co-location over Unix sockets, not public network exposure.
