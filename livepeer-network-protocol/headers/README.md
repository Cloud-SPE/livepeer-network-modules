# headers/

`Livepeer-*` HTTP header conventions and the payment envelope structure.

The headers spec is the **first artifact** plan 0002 produces — every mode spec depends
on it.

**Status:** [`livepeer-headers.md`](./livepeer-headers.md) — **accepted** (plan 0002 Q5
closed 2026-05-06).

The spec defines:

- 5 required request headers: `Livepeer-Capability`, `Livepeer-Offering`,
  `Livepeer-Payment`, `Livepeer-Spec-Version`, `Livepeer-Mode`.
- 1 optional request header: `Livepeer-Request-Id`.
- 4 response headers: `Livepeer-Backoff`, `Livepeer-Work-Units`,
  `Livepeer-Health-Status`, `Livepeer-Error`.
- 9 machine-readable error codes.
- Broker forwarding behavior — strip `Livepeer-*` before reaching the backend;
  inject backend-specific auth from `host-config.yaml`.

Header changes are cross-cutting and force a spec-wide SemVer bump.
