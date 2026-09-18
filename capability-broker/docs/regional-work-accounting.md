# Regional work accounting

A broker configured with `pool_id` requires `accounting_store_path`, scoped
service authentication and a same-pool scoped `receipt_sink`. The work database
is bound permanently to the pool ID, receiver settlement-domain ID and broker
service resource. A changed receiver identity stops startup. Runtime reload
cannot change this identity, the database path or the receipt client; rotating
the contents of existing credential files remains supported.

Before dispatch, paid jobs and paid sessions persist the selected enrollment,
member wallet, backend, capability and offering against the receiver's spend
authorization. The original signed authorization preserves the accepted price;
a later offer-price change cannot rewrite it. Successor session authorizations
inherit the same member binding. Certification and unbilled work add no weight.

Before every receiver advance or settlement call, the broker persists its exact
sequence, payer, units and funding bytes. A lost response is recovered by
replaying that operation to the same receiver. The returned cumulative billed
amount is persisted before reporting billing success. Each positive increase
creates one final, source-qualified receipt. A settlement that merely repeats
an already billed total creates no additional contribution. Native units are
telemetry; monetary deltas determine contribution weights.

Receipt finalization uses a fresh confirmed round observation from the local
receiver's revenue-coverage reader. Completed billing awaiting that observation
remains durable and unqualified. Its contribution enters the round when the
broker can finalize it; it never backdates into a closed work-report prefix.
This bookkeeping rule is separate from revenue timing: redeemed ETH belongs to
the successful transaction's inclusion-block round. Unknown billing results
hold affected work reports until recovered. No clock or source outage is
interpreted as zero work.

The broker retains all finalized receipts and delivery acknowledgements. The
outbox retries HTTPS delivery after controller outages and normal restarts, in
batches of 500 without limiting the complete history. Delivery is idempotent;
regional controller receipts are immutable. The controller credential must carry
`source_id` equal to this receiver's settlement-domain ID and the `broker`
receipt-ingestion role, in addition to the exact pool and controller resource.

`GET /reporting/v1/work/{round}` requires a scoped `revenue-reader` credential.
It exposes the source identity, observed closed-through round, freshness,
receipt count and canonical digest over every finalized contribution. The
regional reconciler compares that proof with every paginated controller receipt.
A delayed delivery therefore prevents closure even if the broker's report is
complete. An explicit complete empty digest is the only zero-work evidence.

Keep `workledger.db`, the broker session/credential stores and the receiver's
account ledger on persistent storage. The work database contains signed
financial authorizations and funding envelopes; restrict it and its backups to
the broker operator. Stop the broker before copying its database for manual
backup, and restore the receiver and broker accounting evidence coherently.
Never substitute an empty database to clear a held report. An unreplayable
receiver operation requires an audited investigation; it remains held rather
than being silently dropped. Automated failover and recovery UI are out of scope.

Local tests cover source identity binding, partial billing, lost responses,
controller outages, restart, successor authorization binding, duplicate requests,
stale clocks and more than 500 receipts. These checks do not establish live
rollout or real-chain acceptance.

## Source drain and retirement

Use scoped `controller` or `pool-admin` credentials to call
`POST /admin/v1/source/drain` with an audit `reason`. The broker persists the
stop-new-authorization fence, stops advertising offerings and preserves existing
billing/recovery and reporting paths. No timeout clears this fence. Record the
source's participation stop in the controller registry as well.

Once existing work has finished and all billed receipts are acknowledged, call
`POST /admin/v1/source/freeze` with a reason. This refuses unfinished broker
operations or receipt delivery and asks the local receiver to freeze intake.
The receiver refuses while any authorization remains admitted. Its freeze is
permanent and durable; normal restart cannot resume tickets, funding or new
admissions. Background redemption of already queued winners continues.

A scoped `revenue-reader` can inspect `GET /reporting/v1/source`. It reports the
intake fence, unsettled authorizations, pending redemptions, unfinalized billing,
undelivered receipts, last work/redemption rounds and confirmed coverage. Missing
history, source mismatches and pending obligations cannot produce complete
retirement proof. This is a machine API, not a recovery UI.

Before accepting traffic after restart, the broker recovers durably recorded
admissions that never obtained a runner binding. A local receiver operation
atomically fences those authorization IDs against late admission and releases
only zero-use, zero-billed reservations. A pre-crash RPC that finishes later
cannot recreate the reservation. Any recorded usage or billed value refuses
this classification. Bound jobs and sessions remain under their normal recovery
paths; absence of a recent heartbeat or expiry is never proof of no execution.
The broker retains unresolved admission evidence and refuses startup if that
proof cannot be established.

A session authorization revision inherits the predecessor's cumulative billed
value and units. The broker captures that immutable baseline from the receiver's
superseded predecessor before returning admission success or allowing billing on
the successor. Only value above that baseline becomes new contribution. Delayed
predecessor receipt finalization, admission-response loss and restart do not
change the baseline. Historical receipts finalized without this evidence are
held for audited correction rather than silently rewritten.

Session authorization revisions persist a sealed intent containing the exact
signed authorization, funding batch, reservation, and promised lease before
receiver admission. The authority change and idempotent response commit together.
After a lost response or restart, the broker replays that intent before further
usage or settlement; it never settles the superseded predecessor as though the
revision had failed. An unresolved revision holds accounting and source retirement.
Pending usage must resolve before a new revision can begin. During source drain,
only an identical, already bound and receiver-confirmed admitted authorization can
replay; new admissions remain blocked. Recovery does not infer failure from a
missing response or discard a pending financial request on a timeout.
