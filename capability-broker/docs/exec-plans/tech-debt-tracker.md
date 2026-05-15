# Tech debt tracker (capability-broker)

Component-local debt. Append rows when you encounter debt; remove them when
the debt is paid down in the same PR.

| Item | Opened | Notes |
|---|---|---|
| `GO_VERSION` ARG pinned to `1.23` | 2026-05-06 | Matches local toolchain at scaffold time. Bump to current stable per core belief #16 once Renovate / dependabot is wired (or in any incidental PR that touches the Dockerfile). Update both `Dockerfile` ARG default and `go.mod` `go` directive. |
| Mock `Payment` middleware uses fixed `expected_max_units = 1` | 2026-05-06 | The `Livepeer-Payment` envelope's `expected_max_units` field isn't decoded yet. Real protobuf decoding lands with payment-daemon integration in plan 0005. |
| `vault://` secret resolver returns "not yet wired" error | 2026-05-06 | `env://` works for the OpenAI-style API resale case. Vault integration is plan 0005 alongside payment-daemon. |
