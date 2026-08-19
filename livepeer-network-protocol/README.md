# livepeer-network-protocol

The wire-protocol specification that the workload-agnostic supply-side rearchitecture is
built around. **Not a code library.** Implementers conform to the specs here; the
broker-side reference implementation lives at the monorepo root in
[`capability-broker/`](../capability-broker/). The previously-shipped
TypeScript `gateway-adapters/` reference middleware was removed from
this repo on 2026-05-19 along with the four product gateways that
consumed it; future gateway-side implementations are out-of-repo.

## Status

Pre-1.0 (spec-wide). Current version: see [`VERSION`](./VERSION).

Per-protocol versions are tracked in each `protocols/<name>.md` frontmatter, and
per-schema versions in each `descriptors/<name>.md`. Hybrid SemVer is the
authoritative versioning policy — see [core belief #14](../docs/design-docs/core-beliefs.md).

## Layout

| Folder | What it holds |
|---|---|
| [`manifest/`](./manifest/) | Manifest JSON Schema, examples, schema changelog |
| [`protocols/`](./protocols/) | The two core protocols (`paid-job/v1`, `paid-session/v1`), the runtime-descriptor framework, and the offering declared axes |
| [`descriptors/`](./descriptors/) | One spec per runtime-descriptor schema (`sfu-room/v1`, `rtmp-hls/v1`, …) |
| [`extractors/`](./extractors/) | One spec per work-unit extractor (`openai-usage`, `response-jsonpath`, …) |
| [`headers/`](./headers/) | `Livepeer-*` header conventions, payment envelope structure |
| [`proto/`](./proto/) | Canonical `.proto` source for the payment wire format and the daemon gRPC services |
| [`proto-go/`](./proto-go/) | Generated Go bindings for `proto/`; importable as a Go module |
| [`verify/`](./verify/) | Cross-cutting Go module that recovers the Ethereum address from a manifest signature (resolver / coordinator / gateway double-verify) |
| [`docs/`](./docs/) | Cross-cutting design docs ([`wire-compat.md`](./docs/wire-compat.md) — byte-for-byte contract with go-livepeer's `pm/`) |

## Versioning

Hybrid SemVer:

- **Spec-wide SemVer** at [`VERSION`](./VERSION) covers cross-cutting parts: manifest
  schema, header conventions, payment envelope, extractor library envelope.
- **Per-protocol SemVer** in each `protocols/<name>.md` frontmatter; per-schema
  SemVer in each `descriptors/<name>.md`.
- Manifest tuples carry both: `spec_version: "<X.Y>"` at the manifest root +
  `protocol: "<name>/v<N>"` per capability, plus `session.descriptor_schema`
  for paid-session offerings (see `protocols/offering-axes.md`).

## Implementing this spec

You can implement either side (broker or gateway middleware) in any language. There is
no required Livepeer library; the contract is the wire spec here.

The broker-side reference implementation lives at:

- [`../capability-broker/`](../capability-broker/) — Go broker (resolution of plan 0002
  Q4).

The gateway-side TypeScript reference middleware that previously lived at
`../gateway-adapters/` was removed on 2026-05-19 along with the four
product gateways that consumed it.

## Verifying your implementation

The mode-era conformance suite was removed with the v0 modes (2026-08-19).
An executable conformance runner for the v1 protocols is being rebuilt; until
it ships, the normative fixtures lists in `protocols/*.md` §Conformance are
the contract.

## Proposing changes

See [`PROCESS.md`](./PROCESS.md).
