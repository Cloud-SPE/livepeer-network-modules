# payment-daemon lints

Custom static-analysis tools for the payment daemon. Lives inside the
daemon's Go module so the lint can target the same files it ships with.

## `no-secrets-in-logs`

Plan 0017 §5.5 + §11.6 mandate that the warm-key password and the
decrypted `*ecdsa.PrivateKey` never reach a log line. This analyzer
walks every non-test `.go` file in the tree and rejects `slog`-shaped
calls whose attribute keys match a deny list (`password`,
`private_key`, `keystore`, …).

### Usage

The lint is exercised three ways:

1. **`go test ./...` from `payment-daemon/`** — the
   `TestRunPaymentDaemonTreeIsClean` test invokes `Run("../..", …)`
   from the lint package and fails the test run if any finding is
   reported. This is the path `make test` takes.
2. **`make lint` from `payment-daemon/`** — Docker invocation that
   runs `go run ./lint/no-secrets-in-logs --root .` directly. Useful
   for ad-hoc CLI feedback; redundant with `make test`.
3. **`go run ./lint/no-secrets-in-logs --root .`** — for developers
   who'd rather drive the lint themselves.

### Suppressing a finding

Add `//nolint:nosecrets` on the same line or the line above the call
and the finding is dropped. Use sparingly and document why in a
comment; the rule of thumb is **don't suppress** — refactor the call
to elide the secret instead.

### Known limits

The analyzer does not perform type resolution. A method named `Info`
on a non-`slog` logger will be heuristically treated as a logging
call. A non-literal attribute key (`fmt.Sprintf("%s_password", …)`)
is not flagged. These are deliberate trade-offs to keep the lint
zero-dependency and fast.

### Source

Ported from
`livepeer-modules-project/payment-daemon/lint/no-secrets-in-logs/`
at tag `v4.1.3` (SHA `caddeb342edb88faeea6a52e83a24c55704f0ef5`),
with the remediation pointer redirected from the prior repo's plan
0013 (structured-logging) to plan 0017 §5.5.
