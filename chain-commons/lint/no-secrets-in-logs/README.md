# no-secrets-in-logs

Static analysis pass that flags `slog`/`logger.Logger` calls where Field values
appear to be keystore content, payment tickets, signed manifest bytes, webhook
secrets, or seed phrases.

The full implementation lands in plan 0001 §J. This README pins the policy;
the tool enforces.

## Banned Field key names (case-insensitive substring match)

Any of these in a `logger.Field` key triggers an error:

- `password`, `passwd`, `pwd`
- `secret`, `seed`, `mnemonic`
- `private_key`, `priv_key`, `privkey`
- `keystore_content`, `keystore_json`
- `auth_token`, `bearer_token`, `api_key`
- `signature` (when value is bytes — sometimes legitimate, sometimes not;
  warn rather than error)

## Banned Field value types

- `[]byte` of length 32 (likely a private key)
- `*ecdsa.PrivateKey`
- Anything pulled directly from `keystore.Keystore.Sign()` returns

## Allow-list

Tests under `_test.go` and the `testing/` package may emit synthetic-key
log lines for fixture purposes. Allow-listed via path filter.

## Why this exists

A leaked private key in a log line is a critical security incident. The
provider-decorator pattern means chain-commons code (where keystore content
flows through) is the last layer that *could* accidentally log a key — once
it's emitted to the logger, the daemon-side decorator and the operator's
log aggregation can't unsee it.

This lint is the belt-and-braces.
