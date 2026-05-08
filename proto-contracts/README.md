# proto-contracts

Canonical protobuf contracts for in-monorepo wire surfaces.

This module owns the `.proto` files and generated Go stubs for:

- `livepeer/payments/v1`
- `livepeer/registry/v1`
- `livepeer/protocol/v1`

Every in-monorepo producer and consumer imports generated code from here.
External sibling repos can continue vendoring independently until their own
migration plans move them onto shared imports.

## Consuming from another repo

External Go consumers can import the generated stubs directly once the
submodule tag is published:

```bash
go get github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts@v0.1.1
```

`worker-runtime` consumes this module the same way. The published release path
does not rely on a local `replace ../proto-contracts`.

## Commands

- `make proto` — buf lint + regenerate all stubs
- `make test` — run module tests
- `make lint` — vet + custom lints
- `make coverage-check` — enforce per-package coverage floor
