# Proposing changes to the protocol

This subfolder governs the wire spec for the rewrite. Changes are PR-driven; this doc
explains when you need a PR vs. when you can land directly.

## When you need a PR (and at least one independent reviewer)

- Adding a new protocol (`protocols/<new>.md`) or changing an existing one.
- Adding a new extractor (`extractors/<new>.md`).
- Any change to the manifest schema (`manifest/schema.json`).
- Any change to header conventions (`headers/livepeer-headers.md`).
- Any breaking change to an existing mode or extractor (major SemVer bump).
- Any change to the conformance runner's externally-observable behavior.

## When you can land directly (no review)

- Typo fixes in any spec doc.
- Clarifying examples that don't change required behavior.
- Adding new conformance fixtures that exercise existing required behavior.

## What a new-mode PR must include

1. The protocol or descriptor-schema spec (`protocols/<new>.md` or `descriptors/<new>.md`)
   with frontmatter declaring its version (start
   at `v0.1.0`; bump to `v1.3.0` only when the mode is judged stable).
2. At least one demonstrable use case in the PR description.
3. Conformance fixture declarations in the spec's §Conformance covering the happy path
   plus at least one failure case.
4. Any required changes to the conformance runner (being rebuilt for v1) to support
   the mode's framing — if the mode introduces a new transport (e.g., gRPC bidi) the
   runner must learn to drive it.
5. Approval from at least one independent reviewer that the mode is meaningfully
   distinct from the existing protocols — a new descriptor schema, not a new protocol, is the default answer (see protocols/offering-axes.md).

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

## Governance

The spec subfolder lives in the `livepeer-network-modules` monorepo until it stabilizes
and is judged ready for extraction to a standalone repo. The extraction is itself a
PR-tracked decision; at that point this `PROCESS.md` migrates with the spec and adds
release-tagging procedure.
