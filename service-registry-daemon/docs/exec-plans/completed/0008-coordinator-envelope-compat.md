---
title: Accept orch-coordinator signed-manifest envelope in resolver mode
status: completed
last-reviewed: 2026-05-09
---

# Goal

Unblock resolver consumers when the published on-chain `serviceURI`
points at an `orch-coordinator` instance still serving the signed
envelope form:

```json
{
  "manifest": { ... },
  "signature": { ... }
}
```

instead of the daemon's flat v3 manifest shape.

# Scope

- Teach `service-registry-daemon` to parse and verify the coordinator
  envelope as a compatibility input.
- Project the envelope into `ResolvedNode` values so gateway
  consumers continue to route on worker URL, capability, offering,
  extra, and constraints.
- Keep the existing flat-manifest path unchanged.

# Non-goals

- Changing the coordinator's published wire format.
- Removing the flat v3 manifest path.
- Changing gateway selection behavior beyond restoring candidate
  visibility.

# Steps

- [x] Add a strict decoder + canonicalizer for the coordinator
      envelope in `internal/types/`.
- [x] Update resolver manifest fetch/verify flow to try the
      compatibility decoder before returning parse failure.
- [x] Add resolver tests covering valid envelope resolution and
      signature mismatch behavior.
- [x] Run the daemon test suite.
