# Product specifications

These documents describe the current consumer-facing implementation and its
limits. The proto package name is not a substitute for checking the coordinated
modules release, especially across the removed publisher/old-manifest migration.

- [gRPC surface](grpc-surface.md): methods, selected-route fields, policy and diagnostics.
- [Manifest contract](manifest-contract.md): signed format, verification and outstanding protocol checks.
- [Legacy compatibility](legacy-compat.md): endpoint-only fallback and CSV behavior.

Observable contract changes require code, tests and documentation in the same
change. Protocol requirements may be stricter than current enforcement; those
gaps are called out explicitly and tracked in beads.
