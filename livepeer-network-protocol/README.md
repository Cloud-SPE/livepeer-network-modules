# livepeer-network-protocol

The wire-protocol specification that the workload-agnostic supply-side rearchitecture is
built around. **Not a code library.** Implementers conform to the specs here; the
broker-side reference implementation lives at the monorepo root in
[`capability-broker/`](../capability-broker/). The previously-shipped
TypeScript `gateway-adapters/` reference middleware was removed from
this repo on 2026-05-19 along with the four product gateways that
consumed it; future gateway-side implementations are out-of-repo.

## Status

Spec-wide version **2.0.0** — see [`VERSION`](./VERSION). The 2.0.0 major bump
removed `interaction_mode` from the manifest in favour of `protocol` plus
declared axes; pre-2.0 consumers cannot read these manifests. Individual
protocol and descriptor-schema specs are still at `1.0.x-draft` in their own
frontmatter.

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
| [`conformance/`](./conformance/) | Executable conformance suite for the v1 protocols; runs against the reference broker or any implementation by URL |
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

[`conformance/`](./conformance/) is an executable suite for `paid-job/v1`,
`paid-session/v1`, and the runtime-descriptor framework. Every scenario pins a
normative clause from `protocols/*.md` §Conformance, and the suite never
imports the reference broker — it speaks only the wire contract.

```sh
make conformance          # auto mode: against the in-repo reference broker

# URL mode: against any implementation
cd conformance && go run ./cmd/livepeer-conformance --broker-url https://your-broker --pause
```

See [`conformance/README.md`](./conformance/README.md) for the offerings your
broker must serve in URL mode, and for the three assertions the suite
deliberately cannot make black-box.

## Proposing changes

See [`PROCESS.md`](./PROCESS.md).
