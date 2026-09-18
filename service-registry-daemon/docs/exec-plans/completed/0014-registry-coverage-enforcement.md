---
id: 0014
slug: registry-coverage-enforcement
title: Enforce package coverage and exercise failure boundaries
status: completed
owner: codex
opened: 2026-09-14
---

## Design

Work is tracked in lnm-gpd. Replace the no-op gate with statement-weighted package
coverage validation at 75%, using Go cover profiles. Inventory executable packages
under cmd and internal so packages without tests cannot disappear from the gate.
Interface-only packages with no executable statements are exempt. Examples and
repository development tools (lint and tools) are outside the shipped daemon
coverage boundary; their behavior is checked by dedicated tests and lint commands.
The production floor stays 75% for every package, not an aggregate average.

Add meaningful error, lifecycle, decoder and discovery tests. Remove unreachable
fallback code where types already guarantee valid input. Validate malformed and
missing profiles, exact floor comparisons, and missing runtime packages. Wire
coverage-check into ship-check so release validation cannot bypass it.

## Validation result

Completed 2026-09-14. Full ship-check passes (lint, race tests and per-package
coverage). Every executable daemon package exceeds 75%; seeder coverage is 100%.
Generated documentation and current links pass. Both Compose configurations
validate; Prometheus checks all 14 rules and the per-instance idle/failure
regression passes. Historical references and previously completed plans were
preserved. Image publication and Blueclaw environment acceptance are separate
release work, tracked in lnm-yer.
