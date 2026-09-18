---
id: 0013
slug: registry-provider-health
title: Provider diagnostics and configured chain identity
status: completed
owner: codex
opened: 2026-09-14
---

## Design

Work is tracked in lnm-cuh. Validate every configured chain RPC endpoint against
--chain-id before constructing production chain discovery. Fail startup on
unreachable or mismatched endpoints instead of allowing unchecked failover.
Overlay-only and publisher modes bypass this check because they do not use chain
discovery. This checks configuration at startup, not a hostile endpoint changing
its chain after startup.

Track completed serviceURI reads and HTTP manifest fetches through provider
wrappers, independent of optional Prometheus metrics. Diagnostic booleans reflect
the most recently completed operation; no attempt is false, and an unused
provider is true (not required). Preserve the last real successful chain-read
timestamp through failures; a successful not-found response counts as a healthy
chain read. These are observations, not active probes or signature-health claims.

## Validation

Test healthy/error/recovery transitions, not-found chain responses, no attempts,
disabled providers, timestamp preservation, wrong chain and unreachable endpoint
startup checks. Run full race tests and update operator contracts and CLI help.

## Validation result

Completed 2026-09-14. Full ship-check passes (lint, race tests and per-package
coverage). Every executable daemon package exceeds 75%; seeder coverage is 100%.
Generated documentation and current links pass. Both Compose configurations
validate; Prometheus checks all 14 rules and the per-instance idle/failure
regression passes. Historical references and previously completed plans were
preserved. Image publication and Blueclaw environment acceptance are separate
release work, tracked in lnm-yer.
