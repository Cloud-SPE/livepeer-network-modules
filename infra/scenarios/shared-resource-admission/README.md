# Shared resource admission verification

This scenario produces the handoff evidence for beads epic `lnm-8bs`. It
verifies the generic Modules mechanism with the transcode templates as its
first consumer:

- controller placement derives one admission domain per physical GPU UUID;
- generated Compose gives every runner on that GPU the same base path and
  the same writable lock-file inodes;
- the member agent creates those inodes once, rejects unsafe replacements,
  and preserves them across reconciliation;
- broker capacity refusal is public `503 capacity_exhausted` with zero-use
  accounting;
- certification treats temporary admission contention as inconclusive; and
- the protocol conformance scenarios preserve route binding and mutually
  exclusive admitted/non-admitted evidence.

Run from this directory:

```sh
./verify.sh
```

The script is Docker-first and writes a redact-safe bundle beneath `run/`.
It records the Modules Git revision and dirty state, GPU inventory when
available, the exact commands and results, and a copy plus digest of the
golden generated Compose fixture.

To add corroborating consumer tests from a local transcode checkout:

```sh
TRANSCODE_REPO=/absolute/path/to/livepeer-modules-transcode ./verify.sh
```

Consumer results are explicitly identified by that checkout's HEAD and dirty
state. A dirty checkout is useful corroboration, but is never described as an
immutable transcode revision and is not part of the Modules implementation.

This scenario does not deploy or modify LOC, a retail gateway, the transcode
repository, or a production orchestrator.
