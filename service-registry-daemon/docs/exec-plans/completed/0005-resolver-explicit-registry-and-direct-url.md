---
id: 0005
slug: resolver-explicit-registry-and-direct-url
title: Make resolver use one explicit registry contract and fetch the exact on-chain manifest URL
status: completed
owner: agent
opened: 2026-05-01
closed: 2026-05-01
depends-on: 0004
---

## Goal

Align `service-registry-daemon` resolver mode with the current gateway deployment contract: one resolver deployment talks to exactly one configured registry contract, and the resolver fetches the full manifest URL returned on-chain verbatim.

Completion means the gateway archetype passes an explicit resolver registry address through Docker Compose, resolver code no longer derives `/.well-known/livepeer-registry.json` from the chain value, and resolver-facing docs/tests match the new behavior.

## Why this plan exists

The current resolver still assumes a legacy pointer shape: it reads a single registry contract and appends `/.well-known/livepeer-registry.json` to the on-chain string. The current publisher/operator stack now publishes fully qualified manifest URLs, including AI-specific filenames, and the gateway must resolve against exactly one configured registry contract rather than probing multiple targets.

That mismatch causes valid full-path publications to fail at resolution time. This is a documented behavior change inside `service-registry-daemon`, so it needs an exec-plan before code lands.

## Non-goals

- No publisher-mode changes in `service-registry-daemon`.
- No changes to `protocol-daemon` publication behavior.
- No multi-target or fallback resolver support.
- No schema/signature changes to manifest verification.

## Approach

### §A. Plan and contract updates

- [x] Add this exec-plan and keep it updated as implementation decisions land.
- [x] Update resolver-facing docs to state that the resolver fetches the exact on-chain manifest URL and that gateway deployments configure one explicit registry contract address.

### §B. Resolver implementation

- [x] Change resolver manifest fetch logic to use the on-chain URL verbatim instead of deriving a well-known subpath.
- [x] Remove now-unused URL-joining helper code if nothing else depends on it.
- [x] Keep resolver mode/config single-target: one `--service-registry-address` per deployment.

### §C. Gateway deployment wiring

- [x] Update `deploy/gateway/compose.yaml` so the resolver receives `--service-registry-address=${SERVICE_REGISTRY_ADDRESS}` explicitly.
- [x] Update `deploy/gateway/.env.example` to document that operators choose either the standard ServiceRegistry or AIServiceRegistry address for that variable.
- [x] Correct misleading flag/docs language that currently implies Controller resolution for resolver `GetServiceURI()` reads.

### §D. Tests and verification

- [x] Update resolver and gRPC wire tests to preload full manifest URLs and host fixture bodies at those exact URLs.
- [x] Run focused Go tests covering resolver behavior, gRPC wire behavior, URL helpers, and CLI/config parsing touched by the change.
- [x] Verify the gateway compose archetype still renders cleanly with the new env variable.

## Decisions log

### 2026-05-01 — Resolver stays single-target and deployment-selected

The gateway side should not attempt multiple registry contracts or workload-specific probing. One resolver deployment talks to one configured registry contract. Operators choose whether that contract is the standard ServiceRegistry or the AIServiceRegistry in deployment config, not in app logic.

### 2026-05-01 — Publisher remains unchanged

The service-registry publisher is already signing-only and does not own on-chain writes. The required fix is entirely on the resolver side plus gateway deployment wiring, so publisher behavior stays untouched.

### 2026-05-01 — Compatibility code paths stay in place, but full-path URL fetch is now the primary resolver contract

This patch intentionally does not remove CSV or legacy resolver code paths. The immediate production bug is that resolver was mangling fully qualified manifest URLs by appending a well-known suffix and hiding the chosen registry contract behind implicit defaults. Fixing that bug only requires direct URL fetch plus explicit single-registry configuration, so broader mode removals are left out of scope.

## Open questions

- Whether the resolver should continue accepting CSV/legacy mode code paths as dormant compatibility behavior once direct full-path fetch is in place. Initial implementation will avoid broad mode removals unless the touched tests/docs force it.

## Acceptance gates

- Resolver fetches a full manifest URL returned by `getServiceURI()` without appending a suffix.
- Gateway archetype explicitly passes one registry contract address to resolver mode.
- Resolver docs no longer claim that `GetServiceURI()` reads are Controller-resolved or that the resolver derives the manifest path.
- Focused tests pass.

## Artifacts produced

- Resolver fetch path updated to use exact on-chain manifest URLs in `internal/service/resolver/resolver.go`.
- Gateway deployment archetype now passes `SERVICE_REGISTRY_ADDRESS` explicitly through `deploy/gateway/{compose.yaml,.env.example}`.
- Resolver-facing docs updated across `README.md`, `DESIGN.md`, `docs/design-docs/`, and `docs/operations/running-the-daemon.md`.
- Resolver, gRPC wire, utility, CLI, and example fixtures updated for full manifest URLs including AI-specific paths.
- Verification completed with `make ship-check` and `make coverage-check` in `service-registry-daemon/`, plus top-level `make doc-lint`.

## Follow-ups

- If operators later need stricter startup guarantees, consider making resolver `--service-registry-address` mandatory instead of defaulting it in code.
