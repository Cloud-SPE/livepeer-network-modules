---
id: 0004
slug: v3-schema-and-archetype-a
title: v3 schema bump (offerings rename) + archetype-A standardization
status: completed
owner: agent
opened: 2026-04-29
depends-on: livepeer-network-suite plan 0003 §A
superseded-by: ../../../docs/exec-plans/completed/0016-v3-0-1-control-plane-reset.md
closed: 2026-04-30
---

## Goal

Land the modules-project half of the suite-wide v3 reset: bump the
manifest schema to `schema_version: "3.0.1"`, rename `Model`/`models[]` →
`Offering`/`offerings[]` (proto + canonical schema + worker.yaml +
resolver client), update host-archetype docs to declare archetype A as
the only supported deployment, and ship two new design-docs covering the
worker `/registry/offerings` contract and the new-workload onramp recipe.

## Non-goals

- No backwards-compat: no dual-mode parser, no alias proto fields. The
  pre-v3 shape is rejected with a clear error.
- No data migrations for the publisher daemon's BoltDB. State wipes
  cleanly on next install.
- No coordinator-side scrape implementation. That lands in the
  orch-coordinator repo.

## Approach

- [x] Bump manifest `schema_version` to the finalized v3.0.1 semver-string contract in `docs/design-docs/manifest-schema.md`
      (top-level shape example + validation order rule).
- [x] Rename `Model` → `Offering` and the repeated `models` field →
      `offerings` in `proto/livepeer/registry/v1/types.proto`.
- [x] Rename `SelectRequest.model` → `SelectRequest.offering` in
      `proto/livepeer/registry/v1/resolver.proto`.
- [x] Regenerate Go stubs under `proto/gen/` via `buf generate` after the schema change.
- [x] Update publisher manifest builder + signer in
      `internal/service/publisher/` to emit `offerings[]` and the new
      `schema_version`.
- [x] Update resolver parser + `Select` handler in
      `internal/service/resolver/` to recognize the new schema and
      reject the pre-v3 shape (no compat code). Update audit/metric
      labels mentioning `model` → `offering`.
- [x] Replace the old `cmd/livepeer-registry-refresh/main.go` proposal shape with JSON `raw-registry-manifest.json` input.
- [x] Update example files and fixtures:
      `examples/static-overlay-only/nodes.yaml`, and
      `docs/generated/manifest-example.md`.
- [x] Mark archetype A as the only supported deployment in
      `docs/design-docs/architecture.md` and any `docs/operations/`
      archetype guides; strike-through worker-publisher references.
- [x] Add new design-doc
      `docs/design-docs/worker-offerings-endpoint.md` defining the
      `/registry/offerings` contract from network-suite plan 0003
      §Decision 5: body shape, optional bearer auth, draft → operator
      confirms → published-in-manifest semantics.
- [x] Add new design-doc
      `docs/design-docs/adding-a-new-workload.md` summarising the
      onramp recipe from plan 0003 §Decision 6.
- [x] Superseded by top-level plan 0016 for the finalized v3.0.1 cut. Release-tag handling moved there.

## Decisions log

### 2026-04-30 — Closed via plan 0016 at the finalized v3.0.1 contract

This module-local plan was opened against an intermediate `v3.0.0` understanding of the suite reset. The landed code followed the finalized top-level v3.0.1 plan instead: semver-string manifests, raw/signed manifest file split, removed geo/warm fields, and updated publisher input flow. The work is complete, but the source of truth moved to top-level plan 0016 before implementation finished.

## Open questions

- **Modules-project version tag** — CONFIRMED `v3.0.0` (operator
  answered 2026-04-29; suite-wide coordinated tag, all repos land at
  v3.0.0).
- **Manifest `schema_version` integer** — CONFIRMED `3` (operator
  answered 2026-04-29; mirrors the suite version cut).
- **Daemon image pinning** — CONFIRMED hardcoded `v3.0.0` in any
  compose snippets shipped under `deploy/`. No tech-debt entry needed:
  every component lands at v3.0.0 in this wave, so there's no version
  uncertainty to track.
- Does `livepeer-registry-refresh` need a CLI flag rename (e.g.
  `--models-file` → `--offerings-file`) alongside the struct rename?

## Artifacts produced
