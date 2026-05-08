---
id: 0007
slug: buf-lint-alignment
title: Align service-registry proto lint with the stable v1 API
status: completed
owner: agent
opened: 2026-05-02
closed: 2026-05-02
---

## Goal
Make `service-registry-daemon` pass `make proto` and therefore unblock monorepo CI, without breaking the already-stable v1 gRPC API.

## Non-goals
- No wire-breaking renames of the published `Publisher` / `Resolver` services.
- No field-number changes.
- No behavioral changes to runtime gRPC handlers.

## Approach
- [x] Replace deprecated Buf lint category usage with an explicit ruleset.
- [x] Exempt naming-style rules that would force breaking API renames on the stable v1 surface.
- [x] Fix true proto hygiene issues that do not affect compatibility (for this change, the unused import).
- [x] Regenerate service-registry proto code and rerun monorepo CI gates.

## Decisions log
### 2026-05-02 — Preserve the shipped v1 service and RPC names
The module’s product spec explicitly marks `livepeer.registry.v1.Publisher` and `livepeer.registry.v1.Resolver` as stable. Renaming them just to satisfy Buf’s `SERVICE_SUFFIX` or request/response naming conventions would create an unnecessary compatibility break. The correct fix is to align Buf’s lint profile with the module’s frozen public API.

## Open questions
- None currently.

## Artifacts produced
- `service-registry-daemon/proto/buf.yaml` aligned with the stable v1 API surface
- `service-registry-daemon/proto/livepeer/registry/v1/types.proto` unused import cleanup and enum prefix alignment
- regenerated `service-registry-daemon/proto/gen/go/livepeer/registry/v1/*`
- gRPC adapter/test updates for regenerated enum names
