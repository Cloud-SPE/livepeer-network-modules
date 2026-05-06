# livepeer-network-protocol

The wire-protocol specification that the workload-agnostic supply-side rearchitecture is
built around. **Not a code library.** Implementers conform to the specs here; reference
implementations are opt-in and live in their own component subfolders at the monorepo
root (`capability-broker/`, `gateway-adapters/`).

## Status

Pre-1.0 (spec-wide). Current version: see [`VERSION`](./VERSION).

Per-mode versions are tracked in each `modes/<mode>.md` frontmatter. Hybrid SemVer is
the authoritative versioning policy — see
[plan 0002](../docs/exec-plans/active/0002-define-interaction-modes-spec.md) Q2
resolution and [core belief #14](../docs/design-docs/core-beliefs.md).

## Layout

| Folder | What it holds |
|---|---|
| [`manifest/`](./manifest/) | Manifest JSON Schema, examples, schema changelog |
| [`modes/`](./modes/) | One spec per interaction mode (`http-reqresp`, `http-stream`, …) |
| [`extractors/`](./extractors/) | One spec per work-unit extractor (`openai-usage`, `response-jsonpath`, …) |
| [`headers/`](./headers/) | `Livepeer-*` header conventions, payment envelope structure |
| [`proto/`](./proto/) | Canonical `.proto` source for the payment wire format and the daemon gRPC services |
| [`proto-go/`](./proto-go/) | Generated Go bindings for `proto/`; importable as a Go module |
| [`verify/`](./verify/) | Cross-cutting Go module that recovers the Ethereum address from a manifest signature (resolver / coordinator / gateway double-verify) |
| [`docs/`](./docs/) | Cross-cutting design docs ([`wire-compat.md`](./docs/wire-compat.md) — byte-for-byte contract with go-livepeer's `pm/`) |
| [`conformance/`](./conformance/) | Test fixtures + Go runner + Dockerfile + Makefile + compose.yaml |

## Versioning

Hybrid SemVer:

- **Spec-wide SemVer** at [`VERSION`](./VERSION) covers cross-cutting parts: manifest
  schema, header conventions, payment envelope, extractor library envelope.
- **Per-mode SemVer** in each `modes/<mode>.md` frontmatter covers that specific mode.
- Manifest tuples carry both: `spec_version: "<X.Y>"` at the manifest root +
  `interaction_mode: "<name>@v<N>"` per capability.

## Implementing this spec

You can implement either side (broker or gateway middleware) in any language. There is
no required Livepeer library; the contract is the wire spec here.

The reference implementations live in:

- [`../capability-broker/`](../capability-broker/) — Go broker (resolution of plan 0002
  Q4). *Not yet present.*
- [`../gateway-adapters/`](../gateway-adapters/) — TypeScript gateway middleware
  (resolution of plan 0002 Q4). *Not yet present.*

## Verifying your implementation

Pull `tztcloud/livepeer-conformance:<tag>` (image tag matches this spec's
[`VERSION`](./VERSION)) and run it against your broker or gateway. See
[`conformance/`](./conformance/).

## Proposing changes

See [`PROCESS.md`](./PROCESS.md).
