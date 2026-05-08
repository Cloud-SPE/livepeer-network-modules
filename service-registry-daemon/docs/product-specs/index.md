# Product specs

Specs in this directory describe **the contract this daemon offers to consumers**. They are stable: changing one is a breaking change.

## Index

- [grpc-surface.md](grpc-surface.md) — gRPC RPC list, request/response shapes, error codes, stability rules
- [manifest-contract.md](manifest-contract.md) — what an operator publishes; what a consumer can rely on
- [legacy-compat.md](legacy-compat.md) — guarantees for old `go-livepeer` clients

## Convention

- A product-spec begins with a stability label (`v1-stable`, `experimental`, `deprecated-since-vX`).
- Every observable behavior is documented as either guaranteed-forever or experimental.
- Anything not documented here is **not** part of the contract.
