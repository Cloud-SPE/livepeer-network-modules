# Settlement-domain release and consumer migration

Review package prepared 2026-09-15 for `lnm-rqz`. Implementation commit:
`80800f8` (`feat(payments)!: scope wholesale accounts to settlement domains`).
Reviewed source baseline for this package: `6e67d0d`.

**This is a proposed coordinated rollout, not a release announcement.** The
implementation is committed locally; independent review, image publication and
production acceptance are not recorded as complete. Beads `lnm-rqz` owns that
remaining work. No deployment credentials, image tags or production changes
are implied by this document.

## Problem and resulting behavior

Two independent brokers can share a Livepeer orchestrator/payee address without
sharing a financial database. Previously, a consumer indexing accounts only by
chain/payer/payee/denomination could merge their balances and version counters.
Each receiver ledger now generates and durably owns a public immutable
`settlement_domain_id`. Funding broker A affects A's account; broker B retains
its own balance and version sequence. Neither an authorization nor a ticket
from A can be used to credit B's independent receiver.

Read the [identity contract](settlement-domain-identity.md) for URI rules,
backup/restore semantics and the precise distinction between URL and financial
identity. A URL hash and coordinator-local broker name are not the account key.

## Review entry points

| Boundary | Source to review | Required property |
|---|---|---|
| Wire and signature contract | [Wholesale account spec](../../livepeer-network-protocol/protocols/wholesale-account.md), [payment protos](../../livepeer-network-protocol/proto/livepeer/payments/v1/types.proto) | Domain included in account identity and v2 signed authorization; missing/foreign domains rejected. |
| Ledger lifecycle | [Store implementation and tests](../../payment-daemon/internal/store/settlement_domain_test.go) | Generate once, retain balances/versions, reject conflicting identity or undrained legacy authorizations. |
| Cross-broker isolation | [Real receiver/payer conformance test](../../payment-daemon/internal/service/sender/settlement_domain_e2e_test.go) | Independent funding and versions, foreign authorization/account RPC/ticket rejection, retained credit on URL relocation. |
| Discovery and cold signing | [Manifest schema](../../livepeer-network-protocol/manifest/schema.json), [changelog](../../livepeer-network-protocol/manifest/changelog.md) | Each signed tuple binds its domain; separate domains remain separate routes. Domain replacement requires cold review. |
| Registry projection | [Resolver proto](../../proto-contracts/livepeer/registry/v1/resolver.proto) | Every selected route retains its signed domain and settlement keys. |
| Operational identity | [Broker runbook](../../capability-broker/docs/operator-runbook.md), [receiver runbook](../../payment-daemon/docs/operator-runbook.md) | Broker durable store cannot silently bind to another ledger; complete backups and writer fencing preserve identity. |

The independent reviewer should specifically examine whether receiver-random
validation isolates tickets even when the other receiver is given the same work
ID, whether payer idempotency/cache keys include the full domain, and whether
any restore or reconnect path can regenerate or silently change identity.
Passing implementation tests is evidence, not independent review approval.

## Release policy and version labels

Protocol `VERSION` is **4.0.0**; the signature domain is
`livepeer-spend-authorization/v2`. These do not select a Docker image tag, nor
do they rename the existing protobuf package `livepeer.payments.v1`.

[PROCESS.md](../../livepeer-network-protocol/PROCESS.md) requires a PR and at
least one independent reviewer for manifest/protocol breaking changes. Its
stability promise also requires a deprecation notice at least one minor version
before a stable breaking release. The current manifest changelog records 3.0.0
and 4.0.0, but no intervening minor deprecation notice for this change. Draft
status on individual protocol documents does not by itself settle the stable
spec-wide manifest question.

The proposed policy-compliant route is to publish the prior-major deprecation
notice before releasing major 4. If maintainers choose a different transition,
they must explicitly resolve and record that policy decision in the protocol
review. This package does not waive the rule or relabel already published
artifacts. Major-4 publication remains pending that resolution.

The release owner must identify the approved source commit, each image tag and
resulting immutable digest, supported architectures, and LOC/BlueClaw client
revisions in the release record. Build the coordinated set from that approved
source. Do not infer image compatibility from a mutable tag such as `v2.0.0`.
The previously published registry-only discovery fix does not constitute a
major-4 release.

## Consumer contract: LOC and BlueClaw

Regenerate clients from both the payment protocol and registry protobuf sources.
Adding protobuf fields is wire-decodable by older clients, but dropping these
fields is not semantically compatible with this paid-work contract.

| Surface | New field number | Consumer action |
|---|---|---|
| `SelectedRoute.settlement_domain_id` | 17 | Retain with the chosen worker URL, payee, price and settlement keys. |
| Registry `Capability.settlement_domain_id` | 6 | Preserve when constructing routes. |
| `SpendAuthorizationPayload.settlement_domain_id` | 22 | Sign and verify under `livepeer-spend-authorization/v2`. |
| `CreateSpendAuthorizationRequest.settlement_domain_id` | 18 | Pass the selected signed domain to the payer daemon. |
| `AccountFundingIntent.settlement_domain_id` | 3 | Bind account funding to the selected receiver. |
| `WholesaleAccountView.settlement_domain_id` | 11 | Reject mismatched observations before computing a shortfall. |
| Settlement record domain | 34 | Verify and reconcile in the correct domain. |
| Non-admission record domain | 11 | Verify and retain the account namespace. |

The receiver's account RPCs also require the domain: `FundWholesaleAccount`
(field 2), `AdvanceAuthorization` (7), `SettleAuthorization` (5),
`GetWholesaleAccount` (2), and `GetSpendAuthorization` (3). Admission carries it
inside the signed authorization. See the [payee proto](../../livepeer-network-protocol/proto/livepeer/payments/v1/payee_daemon.proto).

LOC persistence, reconciliation and account-observation caches must use
`(chain_id, payer, payee, settlement_domain_id, denomination)`. Never assign old
ambiguous rows to a domain by guessing from payee or URL. Reconcile each old row
against its actual receiver ledger and preserve provenance before migrating it.
If historical rows merged multiple brokers, they require explicit reconciliation;
adding an ID column alone cannot recover the split.

BlueClaw must compare the domain on the signed selected route, account response,
and fetched ticket parameters before funding or authorizing. Missing or unequal
IDs must fail closed. Use independently computed balances/funding decisions
when failover selects another domain. A URL change within the same domain still
requires a new authorization bound to the new canonical broker URI.

`settlement_domain_id` does not replace `settlement_keys`. The latter still
identify the delegated keys used to verify signed settlement records; the former
identifies the ledger/account namespace. Both must survive route selection.
On-chain payee identity, ticket encoding/signatures and redemption are unchanged.

## Rollout procedure

1. Prepare the approved images and upgraded clients before admitting major-4
   traffic. Record the current images/digests and configured chain/payee, ledger
   paths, broker workload-store paths and signing material references. Keep
   private keys out of the review/release record.
2. Stop new admissions and client funding to the affected brokers. Drain jobs,
   sessions, admitted authorizations and accounting retries under the old
   contract. Resolve outstanding accounting before stopping services. Do not
   delete authorizations to bypass the receiver's migration guard.
3. With writers stopped, take consistent complete backups of receiver ledgers,
   payer state, broker workload stores and required sealing keys. Record baseline
   balances and account versions for the accounts used in acceptance testing.
   Preserve coordinator/cold-sign publication state as well.
4. Start each upgraded receiver against its existing ledger. Let it generate its
   own ID; the optional `--settlement-domain-id` flag is for deliberate bootstrap
   import, not an arbitrary broker setting. Record each receiver's Health ID and
   verify chain/payee and baseline credit/version preservation. Distinct ledgers
   must have distinct IDs. Back up the migrated state before resuming writes.
5. Upgrade the broker and coordinator. Verify each broker observes its intended
   receiver's ID and pins it in the correct durable workload store. Stop on a
   stored/configured mismatch. Generate a major-4 candidate and inspect the
   worker URL/domain associations before moving it to the cold signing host.
6. Use the normal [cold-sign workflow](../../secure-orch-console/docs/operator-runbook.md)
   and [coordinator publication workflow](../../orch-coordinator/docs/operator-runbook.md).
   Publish with a fresh sequence and valid lifetime. Preserve each broker's
   independently advertised domain. The cold orchestrator key stays on its host.
7. Upgrade registry and payer services and the LOC/BlueClaw clients as one
   coordinated paid path. Force a fresh route observation and confirm the
   actual SelectMany response includes the signed domain and settlement keys.
   Minimal static overlays still discover the coordinator manifest; they do
   not become a source of static pricing or ledger identity.
8. Run the acceptance procedure below against the deployed digest set, then
   resume admissions gradually. Record results and client revisions in the
   release record before calling the deployment accepted.

## Deployment acceptance

Use two independent broker/receiver stores with the same chain, payer and payee.
Record domain A and B, then observe both accounts. Fund A and verify only A's
balance/version changes. Independently fund and use B; its version sequence need
not equal A's. Exercise paid work and verify settlement signatures, domains and
resulting account views through the actual LOC/BlueClaw client.

Attempt A's authorization at B and verify rejection without account mutation.
Attempt A's ticket at B and verify no credit, including when the work ID is known.
Confirm that selecting B after A requires an independent authorization and
funding decision. Capture RPC outcomes and account snapshots without private
keys or bearer credentials.

Rehearse a URL migration and complete backup restore in an isolated environment:
fence the old writer, retain the complete ledger/domain, publish the new URI and
use a new authorization. Credit must remain associated with the same domain.
A fresh independent ledger must get a new ID and must not inherit old credit.
Do not run two writable restored copies with the same domain.

## Recovery boundary

Before new-format writes, recovery may restore the entire consistent pre-upgrade
service/store set while clients remain stopped. After funding or admission under
major 4, restoring an old backup or downgrading a binary can lose acknowledged
money/accounting. Stop admissions, preserve current stores and reconcile before
choosing a recovery procedure; prefer a forward fix. Never delete domain metadata
or copy only an ID to make startup succeed.

Manifest publication has its own anti-rollback sequence. Recovery must not rely
on re-uploading an older signed manifest or deleting registry high-water marks.
Any recovery publication follows the normal cold-sign process with an increasing
sequence and compatible consumers.

## Validation evidence and reproduction

The implementation bead records 58/58 protocol conformance scenarios, component
race suites, payment/coordinator Docker tests, broker image build and registry
ship checks. Those are implementation-time results, not production acceptance.
On 2026-09-15 the package preparation reran protocol schema/example/signature and
version checks plus the real two-ledger and store migration tests at `6e67d0d`.

For a development checkout with the test toolchain available:

```sh
make -C livepeer-network-protocol check
go -C payment-daemon test ./internal/service/sender -run TestIndependentSettlementDomainsConformance -count=1
go -C payment-daemon test ./internal/store -run TestSettlementDomain -count=1
```

Component Docker build/test procedures remain in their operator runbooks and
Makefiles. Review/release acceptance must name the tested commit and actual
published digests; local test results alone do not verify a remote deployment.
