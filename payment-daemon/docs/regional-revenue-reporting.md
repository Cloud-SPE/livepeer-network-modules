# Regional receiver revenue evidence

Regional revenue is confirmed ticket face value, attributed to the Livepeer
`currentRound()` read at the successful redemption receipt's inclusion block.
It is not attributed to the round observed when the daemon notices confirmation.
The receipt block number/hash, transaction hash, round, observed head and check
time are stored with the redeemed ticket.

The settlement loop looks for a prior durable redemption intent before applying
expiry or used-ticket guards. After a restart, an already-confirmed transaction
can therefore finish accounting without being discarded as an expired/used ticket
or sent again. An unresolved broadcast intent or unavailable historical RPC holds
that item. Used tickets with no corresponding local intent require audited recovery.

`PayeeDaemon.GetRoundRevenue` remains local on the receiver Unix socket. Its
additive response fields identify the immutable receiver settlement domain,
chain, and payee and qualify the result with `complete`, an incomplete reason,
canonical observed-head and finalized-prefix evidence, the covered round,
observation time, and an inclusion digest. The broker exposes only this read
operation at `/reporting/v1/revenue/{round}` using a dedicated scoped
`revenue-reader` credential. It exposes no receiver mutation socket over HTTPS.

A report reads all pending/redeemed rows in one database transaction, without a
500-item limit. It revalidates inclusion evidence and requires a fresh chain head
(no more than two minutes old), sufficient configured confirmation depth, and a
confirmed chain prefix already in a later round. A source with an outstanding
relevant confirmation, settlement catch-up, changed canonical inclusion, or
unqualified legacy history reports incomplete. A responsive stale RPC does not
count as complete. The report finishes by rechecking the observed canonical
prefix. Detailed evidence remains in the receiver ledger; the digest commits to
the ordered ticket/transaction/block/round/value records and the receiver identity.

A complete zero is valid. Missing or incomplete data is unknown and must hold
regional closure. Dev receivers and older providers cannot produce chain-qualified
completeness. Regional collectors must inspect the completeness fields rather
than merely summing the legacy amount field.

Legacy confirmed rows without durable inclusion evidence and old used-ticket
sentinels are deliberately held. Preserve the full receiver and transaction-intent
stores and restore a verified paired backup, or perform an audited evidence
backfill before treating that history as complete. Do not delete a missing source,
reset its domain ID, or reinterpret a held report as zero. Changes to previously
recorded canonical evidence require operator investigation; approved regional
payouts are not rewritten by the receiver reporting operation.

Local regression tests cover 601 redemptions across reopen/retry, explicit zero,
stale coverage, reorg rejection, missing confirmations, and recovery of a confirmed
redemption after the observation clock has moved past ticket expiry. Production
RPC availability, archive retention and throughput remain rollout checks.

Regional work recovery also relies on immutable accepted advance evidence in the
receiver ledger. Successful wholesale advances now retain their per-sequence
state. An exact historical replay returns that accepted cumulative billed value,
even after expiry or final settlement, with zero new funding or billing. Changed
units, reservation, payer or payee are rejected. The returned authorization state
is the historical accepted result; current account balances remain current.
Older databases without the historical sequence evidence are not backfilled by
guessing. Keep the receiver database with broker accounting backups so normal
restart recovery can replay accepted operations safely.

`FreezeRevenueSource` is a local privileged operation that irreversibly fences
receiver intake after every admitted authorization settles. It synchronizes with
in-flight intake calls and persists before responding. It does not stop the
redemption loop. `GetRevenueSourceStatus` reports the persistent fence, remaining
redemptions, final inclusion round and complete confirmed coverage. A frozen
receiver remains available for read-only historical reporting. Restore the fence
with the receiver ledger; never recreate an empty ledger under the old source ID.

`CloseUnexecutedAuthorization` is a private broker restart-recovery operation.
The broker may call it only when its durable admission record has no runner
binding. The receiver atomically tombstones absent authorization IDs against
late in-flight admission, or releases an existing zero-use, zero-billed
reservation. Recorded usage or billing blocks this operation. This is not a
remote refund, transfer or operator write API.
