# Dev chain-seed readiness

Bead: `lnm-11k` (downstream report: LOC `loc-1zq.9.1`).

## Problem

`--dev --chain-seed` preloads the in-memory chain provider, but dev mode has
neither chain discovery nor a round clock. The runtime seeder is therefore not
constructed and `ListKnown` remains empty until a consumer happens to call
`ResolveByAddress`.

## Invariants

- An explicitly configured seed is a startup contract, not a best-effort hint.
- Resolution uses the ordinary signed-manifest path and its existing policy.
- The daemon does not announce readiness until every seed has resolved.
- Plain dev mode and overlay-only best-effort warming remain unchanged.

## Implementation

Parse the seed through one shared strict loader, preload the in-memory chain,
then synchronously resolve each seeded address after the resolver service is
constructed and before listeners report readiness. Any resolution failure
fails startup. Tests use a locally signed manifest to prove a fresh start and
a second start both populate `ListKnown`; malformed/unreachable seeds remain
negative cases.

## Verification

Verified with the full race suite, golangci-lint, the unsigned-manifest lint,
and the coverage check. The component-wide doc gardener still reports its
pre-existing broken-link inventory; none of those links is introduced or
changed by this plan.
