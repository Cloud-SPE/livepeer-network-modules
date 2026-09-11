# headers/

`Livepeer-*` HTTP header conventions and the payment envelope structure.

Both protocol specs ([`paid-job/v1`](../protocols/paid-job.md),
[`paid-session/v1`](../protocols/paid-session.md)) depend on this document.

**Status:** [`livepeer-headers.md`](./livepeer-headers.md) — **draft**,
rewritten for the v1 protocols (2026-08-19).

The spec defines:

- 5 required request headers: `Livepeer-Capability`, `Livepeer-Offering`,
  `Livepeer-Payment`, `Livepeer-Protocol`, `Livepeer-Request-Id`.
- 7 response headers: `Livepeer-Backoff`, `Livepeer-Work-Units`,
  `Livepeer-Work-Unit`, `Livepeer-Job-Id`, `Livepeer-Settlement`,
  `Livepeer-Health-Status`, `Livepeer-Error`.
- The machine-readable `Livepeer-Error` code table and its HTTP-status mapping.
- Broker forwarding behavior — strip `Livepeer-*` before reaching the backend;
  inject backend-specific auth from `host-config.yaml`.

Header changes are cross-cutting and force a spec-wide SemVer bump.
