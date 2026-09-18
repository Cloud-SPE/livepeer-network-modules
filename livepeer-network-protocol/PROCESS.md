# Proposing changes to the protocol

This subfolder governs the wire spec for the rewrite. Changes are PR-driven; this doc
explains when you need a PR vs. when you can land directly.

## When you need a PR (and at least one independent reviewer)

- Adding a new protocol (`protocols/<new>.md`) or changing an existing one.
- Adding a new descriptor schema (`descriptors/<new>.md`) or changing an
  existing one.
- Adding a new extractor (`extractors/<new>.md`).
- Any change to the manifest schema (`manifest/schema.json`).
- Any change to header conventions (`headers/livepeer-headers.md`).
- Any breaking change to an existing protocol, descriptor schema, or extractor
  (major SemVer bump).
- Any change to the conformance runner's externally-observable behavior.

## When you can land directly (no review)

- Typo fixes in any spec doc.
- Clarifying examples that don't change required behavior.
- Adding new conformance fixtures that exercise existing required behavior.

## What a new-protocol or new-descriptor-schema PR must include

**A new descriptor schema is the default answer; a new protocol is the rare,
expensive one** (see [`protocols/offering-axes.md`](./protocols/offering-axes.md)
and `docs/design-docs/interaction-modes.md`). A schema is implemented only by
the runner that emits it and the gateway that consumes it — no broker,
clearinghouse, or registry work.

1. The protocol or descriptor-schema spec (`protocols/<new>.md` or `descriptors/<new>.md`)
   with frontmatter declaring its version (start at `1.0.0-draft`; drop the
   `-draft` suffix only when the spec is judged stable).
2. At least one demonstrable use case in the PR description.
3. Conformance fixture declarations in the spec's §Conformance covering the happy path
   plus at least one failure case.
4. Any required changes to the [conformance runner](./conformance/) to support
   the new framing — a schema needs at minimum a public-by-contract fixture; a
   protocol introducing a new transport (e.g. gRPC bidi) needs the runner to
   learn to drive it.
5. Approval from at least one independent reviewer that the addition is
   meaningfully distinct from what already ships.

## What a new-extractor PR must include

1. The extractor spec under `extractors/<new>.md`.
2. Reference pseudocode or a concrete recipe.
3. Conformance fixtures demonstrating the extractor against representative responses.

## What a manifest-schema PR must include

1. Updated `manifest/schema.json`.
2. Updated examples under `manifest/examples/`.
3. A changelog entry in `manifest/changelog.md` (or in this `PROCESS.md` if no
   dedicated changelog yet).
4. A spec-wide SemVer bump in [`VERSION`](./VERSION) — manifest changes are *always*
   spec-wide.

## Versioning

See [`README.md`](./README.md) §Versioning. Hybrid SemVer means:

- Spec-wide changes bump [`VERSION`](./VERSION).
- Per-protocol and per-schema changes bump the version in that spec's frontmatter.
- Manifest schema changes are always spec-wide bumps (the manifest is cross-cutting).

## Stability promise

A spec at `1.0` or higher is stable. Breaking changes require a major bump and a
deprecation notice in `manifest/changelog.md` (or the relevant `protocols/<name>.md`
changelog block) at least one minor version before the breaking release.

Pre-1.0 specs (`0.x.y`) are not stable; minor bumps may break consumers. Implementers
who pin to a pre-1.0 version do so at their own risk.

A `-draft` suffix (`1.0.0-draft`) means the same thing at any number: the spec
is being implemented against but has not yet been declared stable, so breaking
changes may land without a major bump until the suffix is dropped. Every v1
protocol and descriptor schema currently carries it.

## Governance

The spec subfolder lives in the `livepeer-network-modules` monorepo until it stabilizes
and is judged ready for extraction to a standalone repo. The extraction is itself a
PR-tracked decision; at that point this `PROCESS.md` migrates with the spec and adds
release-tagging procedure.
