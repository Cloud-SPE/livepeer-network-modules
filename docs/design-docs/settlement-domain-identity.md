# Settlement-domain identity

Implemented contract: protocol major 4; spend-authorization signature domain
`livepeer-spend-authorization/v2`. Design provenance: `lnm-pvb` and
[plan 0053](../exec-plans/active/0053-settlement-domain-identity.md).

An independent payment-daemon financial ledger owns one immutable public
`settlement_domain_id`. It is a random, nonzero 256-bit value encoded as `0x`
followed by 64 lowercase hex digits. It is not a key, credential, broker name,
URL hash, or on-chain payee address. The complete wholesale account key is:

```text
(chain_id, payer, payee, settlement_domain_id, denomination)
```

The receiver generates the ID once in the same Bolt database that owns balances,
reservations and account versions. `--settlement-domain-id` optionally imports
an existing identity at bootstrap; if the stored ID differs, startup fails.
Stored chain and payee bindings cannot be changed by editing configuration.
The current denomination is `wei`. Local account rows remain keyed by payer/payee
inside this single-domain database; the database identity supplies the missing
namespace. External reconciliation stores must include the complete tuple.

The broker discovers the ID through PayeeDaemon Health. The real adapter pins it
at connection startup, checks it after reconnects, and includes it in each account
RPC. The broker also pins this observed ID in its durable workload store, refusing to
restart that store against a different payment ledger. Those RPCs reject missing
or mismatching IDs before account mutation. A
socket being rebound to a different ledger therefore cannot redirect an existing
session's accounting to a colliding authorization ID.

Each advertised capability carries the ID into the coordinator's candidate and
cold-signed manifest. Registry SelectedRoute exposes it as field 17. Coordinator
aggregation includes the domain in its uniqueness key: identical offerings under
different domains remain separate routes, even if their payee and price match.
One worker URL cannot describe two domains in the same publication. The cold
console indexes the domains independently; adding/replacing one requires critical
review, including when other content is unchanged.

Payers must compare the selected route, account observation and ticket-parameter
response IDs before signing or computing a shortfall. AccountFundingIntent and
SpendAuthorizationPayload explicitly carry the ID. The sender rejects ticket
parameters from another domain before minting. Signed authorizations bind both
the ID and broker URI; the receiver checks the ID and the broker checks the URI.
Signed job/session settlement records, non-admission records and account responses retain the same ID.
Versions are monotonic within an account in one domain, never across brokers.
An authorization ID may be reused in a different complete account tuple.

Ticket wire encoding, signatures, recipient and on-chain redemption remain
unchanged. The recipient-random session still belongs to the issuing receiver.
The conformance test funds two real receiver ledgers with one payer/payee and
attempts to credit A's ticket at B, including after opening A's work ID at B.
B rejects it because a guessed work ID does not supply A's stored recipient-random
state. The domain is an additional account/protocol binding, not a replacement
for ticket signature, randomness and nonce validation.

## URI rules

The shared `proto-go/identity.BrokerURI` implementation defines route comparison:

| Part | Rule |
|---|---|
| Scheme | HTTP or HTTPS, lowercase; production signed worker URLs require HTTPS. |
| Host | Lowercase ASCII DNS or IP literal. Unicode hostnames must be converted to IDNA A-labels before configuration. Trailing DNS dot, empty labels and invalid labels are rejected. |
| Port | Decimal normalization; remove HTTP 80 and HTTPS 443. Reject empty, zero or out-of-range ports. |
| Trailing slash | Remove one final slash, including the root slash. |
| Base path | Preserve case and segments. Reject dot segments, repeated slashes, backslashes and percent escapes. |
| Credentials/query/fragment | Reject, including empty `?` or `#`. |
| Redirects | Never establish identity. The payer's ticket-parameter HTTP client rejects redirects. |

Canonical URI equality is not account equality. A URL alias needs a new
route-bound authorization even if it reaches the same domain. Failover to another
domain requires a new authorization and an independent balance/funding decision.

## Upgrade, restore and migration

Drain admitted jobs, sessions, pending accounting retries and authorizations using
the previous contract before upgrading. Take consistent backups of both the broker
and payment-daemon databases. Initialize the receiver identity first; startup
refuses a legacy database containing admitted authorizations. An eligible legacy
ledger gets an ID transactionally without resetting its credit or version counters.
Persist that ID with its backup, then upgrade the broker/coordinator, cold-sign the
new candidate, and update registry and payer/LOC clients to protocol major 4.
Old manifests may still be readable for diagnosis; an empty route ID cannot be used
for paid work. Never turn an old unknown namespace into a guessed ID.

Moving a URL while moving the **complete same ledger** preserves its ID and credit.
Publish the new URI under the same ID and issue new authorizations for that URI.
Starting a new independent ledger creates a new ID and zero account balances;
there is no implicit alias or credit transfer. Financial transfer/reconciliation
requires an explicit operator procedure, outside this change.

A complete ledger restore keeps its identity. Fence the old writer before starting
a restored instance; two divergent writable copies must not share an ID. Missing
or corrupt identity metadata in a migrated ledger fails closed and requires a
complete backup recovery. An operator must not copy just the ID into a fresh store
to claim another ledger's balances. Backups can be stale: preserving identity does
not waive the need to reconcile lost transactions before resuming service.

## Conformance

`payment-daemon/internal/service/sender/settlement_domain_e2e_test.go` exercises
real payer/receiver gRPC services, HTTP ticket-parameter proxies, ticket validation
and independent Bolt stores. It covers independent funding, versions, authorization
IDs, cross-domain authorization/account-operation rejection, ticket replay rejection,
use of both balances, and URL relocation with retained credit. Store tests cover
bootstrap import, restart, chain/payee mismatch and lost metadata. Registry wire
tests verify per-route relay through cold, cached and refreshed SelectMany responses.
Coordinator and console tests cover domain-aware aggregation and required review.

For the proposed coordinated rollout, consumer field mapping and review evidence,
see [the release and migration package](settlement-domain-release.md).
