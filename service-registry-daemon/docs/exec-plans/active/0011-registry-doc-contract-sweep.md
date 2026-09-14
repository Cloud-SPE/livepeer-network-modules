---
id: 0011
slug: registry-doc-contract-sweep
title: Align registry documentation with executable contracts
status: active
owner: codex
opened: 2026-09-14
---

## Goal

Review the registry component's active documentation, deployment examples, CLI help, wire contract and implementation together. Work is tracked in `lnm-735`; broken-link repair is `lnm-qp0`.

## Design

Replace obsolete publisher/signing and node-shaped manifest descriptions with the coordinator/cold-signing/protocol-envelope flow. Document actual discovery, synchronous refresh, route selection, signature policy, health and observability behavior. Link adjacent component ownership instead of duplicating their implementation manuals. Distinguish runtime implementation gaps from protocol requirements.

Preserve historical references and completed plans byte-for-byte. The documentation checker validates current documentation and examples, excluding those immutable historical trees; add tests that current broken links still fail. Generate the manifest example from the protocol fixture, validate it with the boundary decoder, and provide a reproducible generation command rather than hand-editing generated content.

Correct deployment examples and CLI descriptions where they advertise unsupported behavior. Fix the narrow selection unsigned-policy bypass, with regression coverage, rather than weakening the documented policy. Larger pre-existing security/readiness gaps become explicit beads and documented limitations for a separate implementation change.

## Validation

Run documentation generation twice for deterministic output, document/link lint, all Go lints and race tests, the in-process example, YAML parser validation of shipped overlays, and Compose config validation. Compare historical paths with HEAD to ensure they are unchanged. Verify local Docker build if available; do not publish or change image tags.
