# Immutable settlement-domain identity

Bead: `lnm-pvb`. Accepted 2026-09-14.

Each independent payment-daemon ledger owns a random 256-bit public
`settlement_domain_id` (64 lowercase hex digits prefixed by `0x`). The wholesale
account identity is `(chain, payer, payee, settlement_domain_id, denomination)`.
The ID is persisted transactionally with accounting state, retained on restart
and complete ledger restore, and must never be reused for divergent ledgers.
Bootstrap import is supported; a configured/stored mismatch fails startup.
An existing ledger acquires an ID without moving or resetting its balances.
Operators must drain outstanding pre-upgrade authorizations before upgrading.

The broker obtains this identity from its payment daemon. Each capability tuple
in the cold-signed manifest binds the ID to its worker URL. Coordinator
aggregation includes the domain in its identity, preserving independent accounts
with identical offerings. A changed domain requires explicit cold-sign review.
The registry exposes the signed ID on SelectedRoute and includes it in route
fingerprints. Payers bind signed spend authorizations to the ID; receivers reject
missing or foreign IDs before admission/funding. Account views and signed
settlements carry the same ID. Protocol major 4 and spend-authorization domain v2
make this fail-closed change distinguishable from the legacy contract.

URLs remain transport/authorization bindings. Moving a hostname with the complete
same ledger preserves money, but requires a newly signed route and authorization.
A new ledger starts a new account; no implicit alias transfers credit. Restore
must fence the old writer. Lost metadata requires recovery from a complete backup,
not regeneration against existing financial state.

Conformance covers independent ledgers with equal payer/payee, distinct IDs,
independent balances and versions, cross-domain authorization and ticket rejection,
independent funding, restart, explicit bootstrap, mismatch rejection and migration.
Production image tags and deployment are separate from this protocol change.

Implementation is complete; review and coordinated publication
are tracked by `lnm-rqz`. Validation: 58/58 protocol conformance scenarios; real
two-ledger payer/receiver isolation and migration tests; component race tests;
payment/coordinator Docker test stages; broker Docker builder; registry lint,
document generation, race and per-package coverage gates. No image was published
or deployment changed.

The [release and migration package](../../design-docs/settlement-domain-release.md)
records reviewer entry points, consumer contract changes, release-policy questions,
rollout order, recovery boundaries and deployment acceptance. It does not record
independent approval or publication; those remain on `lnm-rqz`.
