# proto-contracts

Canonical protobuf contracts for in-monorepo wire surfaces.

This module owns the `.proto` files and generated Go stubs for:

- `livepeer/registry/v1`
- `livepeer/protocol/v1`

The `livepeer/payments/v1` contracts are owned by
`livepeer-network-protocol/proto-go` (the canonical payments surface); this
module no longer carries a duplicate copy.

Every in-monorepo producer and consumer imports generated code from here.
External sibling repos can continue vendoring independently until their own
migration plans move them onto shared imports.

## Consuming from another repo

External Go consumers can import the generated stubs directly once the
submodule tag is published:

```bash
go get github.com/Cloud-SPE/livepeer-network-modules/proto-contracts@v0.1.1
```

`worker-runtime` consumes this module the same way. The published release path
does not rely on a local `replace ../proto-contracts`.

## Commands

- `make proto` — buf lint + regenerate all stubs
- `make test` — run module tests
- `make lint` — vet + custom lints
- `make coverage-check` — enforce per-package coverage floor

## 2026-09-14: settlement-domain route binding

`SelectedRoute.settlement_domain_id` (field 17) and `Capability.settlement_domain_id`
(field 6) relay the immutable ledger ID from the cold-signed manifest. Paid clients
must require it and scope their accounts accordingly. See
[the cross-component contract](../docs/design-docs/settlement-domain-identity.md).
Payments protobuf additions remain owned by `livepeer-network-protocol/proto/`.

## 2026-09-24: cached registry catalog and retry diagnostics

Additive `Resolver.ListOfferings` returns informational offering snapshots with
provider identity, full SelectedRoute metadata, selectability and explicit
unfiltered discovery coverage. `ResolveResult` and `KnownEntry` gain
`DiscoveryStatus`; deferred failures carry `RegistryResolutionDetail` and
`google.rpc.RetryInfo` alongside the existing stable Struct error code.
See the [consumer contract](../service-registry-daemon/docs/product-specs/grpc-surface.md)
and [JSON fixtures](livepeer/registry/v1/testdata/README.md).
The implementation design is [plan 0016](../service-registry-daemon/docs/exec-plans/completed/0016-classified-retries-and-catalog.md).
