# AGENTS.md — proto-contracts

This module owns the canonical protobuf contracts and generated Go stubs for
the monorepo's wire surfaces.

## Start here

- [README.md](README.md)
- [docs/design-docs/index.md](docs/design-docs/index.md)
- [docs/design-docs/contract-ownership.md](docs/design-docs/contract-ownership.md)

## Invariants

1. Canonical wire-contract `.proto` files live here.
2. Generated Go stubs are committed and regenerated only from this module.
3. Wire-shape changes require an exec-plan and the relevant changelog entry.
