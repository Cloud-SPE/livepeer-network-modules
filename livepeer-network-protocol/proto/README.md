# `proto/` — wire-format definitions

Canonical Protobuf definitions for the Livepeer-Network protocol.

These files are the source of truth for the bytes on the wire. The generated
Go bindings are committed alongside them in
[`../proto-go/`](../proto-go/), importable as its own Go module (TypeScript
encoders historically lived in an OpenAI-protocol gateway that is out-of-repo
as of 2026-05-19); the `.proto` files in this folder are what those bindings
track.

## Layout

```
proto/
└── livepeer/
    ├── payments/
    │   └── v1/
    │       ├── types.proto          — Livepeer-Payment envelope + shared types
    │       ├── payer_daemon.proto   — Sender-side gRPC service
    │       ├── payee_daemon.proto   — Receiver-side gRPC service
    │       └── payee_admin.proto    — Receiver admin gRPC service
    └── sessionrunner/
        └── v1/
            ├── control.proto        — session-runner control plane
            └── media.proto          — session-runner media plane
```

## Versioning

The `v1` directory pins the wire major-version. Backwards-incompatible
changes get a new sibling directory (`v2/`) and the broker accepts both
during transition windows.

Field-additions inside `v1/` are non-breaking and require only a CHANGELOG
note. Field-removals or renumbering require a new package version.

## Status

`v1` is a pre-1.0 working copy until the monorepo cuts v1.3.0. Until then,
breaking changes can land inside `v1/` as long as every co-released
component is updated in the same commit.

## Regenerating bindings

The Go bindings are generated and committed; consumers don't need protoc
at build time. To regenerate after editing a `.proto` file:

```sh
make -C ../../payment-daemon proto
```

The generated files are formatted with `goimports` and committed.
