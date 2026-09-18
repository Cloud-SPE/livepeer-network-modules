# Qualified regional reports

`GET /api/reports` authenticates the wallet and collects each configured
controller's `GET /member/v1/regional-report`. The controller reads one Bolt
snapshot and exposes only that wallet's allocation and payment history. Every
window and payment retains pool identity; source evidence retains pool, broker,
receiver source, round and inclusion/work digests. No 500-row history cap applies.

Amounts are exact decimal wei strings. Earned amounts from closed windows split
into awaiting approval, approved pending payment, and confirmed paid. Failed
payments remain pending obligations. Operator-paid gas never reduces a member
amount. Unclosed work is not represented as a guaranteed payout. A wallet with
no allocation has an explicit zero in a complete closed regional window; an
unavailable report has no amount. Commission, rounding residual and zero-work
operator allocation remain separate pool-level telemetry; the zero-work count
and total windows, and zero-work revenue and total confirmed revenue, are exact
fraction operands. Other wallets and their allocations are never returned.

`observed_at` marks the report read, `ledger_observed_at` the latest immutable
round snapshot, and `complete_round_spans` lists actual reconciled coverage,
including gaps. A fresh HTTP response does not claim a current-round close.
Window holds and correction holds remain visible without rewriting earnings.

The portal validates identity and category conservation before caching. Each
regional response is fresh, stale (last successful response with its original
fetch time), or unavailable. The bounded, wallet/pool-keyed in-memory cache lasts
at most 24 hours and disappears on restart; losing it yields unavailable data,
never zero. No cached data authorizes a mutation.

Aggregate rows group only exactly matching chain, asset, start round and end
round. They list contributing pool/window IDs and missing pools. A stale,
unavailable or differently aligned region prevents a purported complete total.
There is deliberately no unqualified cross-period grand total. The controller's
public `/member/v1/public-region` projection contains only controller reachability
and configured offering descriptions; it makes no claim about live runner
eligibility and exposes no private member data.
