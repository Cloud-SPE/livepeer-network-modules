# `secure-orch-console/`

The cold-key host's diff-and-sign console. Runs on `secure-orch` — the
firewalled machine that holds the orchestrator's cold key. The
operator drives the sign cycle:

1. Coordinator builds a candidate manifest and exposes it for download.
2. Operator hand-carries the candidate to secure-orch (USB, scp,
   laptop — operator's choice).
3. Operator opens the console, reviews a structural diff against the
   last-signed manifest, taps to sign.
4. Console writes the signed manifest to a local outbox; operator
   ferries it back to the coordinator.
5. Coordinator publishes at `/.well-known/livepeer-registry.json`.

Designed against
[`../docs/exec-plans/active/0019-secure-orch-trust-spine-design.md`](../docs/exec-plans/active/0019-secure-orch-trust-spine-design.md).

## Hard rule

**secure-orch never accepts inbound connections.** The console's
HTTP server binds `127.0.0.1` only. Operators access it via
`ssh -L 8080:127.0.0.1:8080 secure-orch` from a LAN laptop; the
tunnel terminates inside secure-orch's loopback. See plan 0019
§6.1.1 for why this preserves the hard rule.

## Status (commit 2 of 7 — canonicalization + signing primitives)

Today this directory ships:

- [`internal/canonical/`](./internal/canonical/) — JCS-equivalent
  canonical-bytes algorithm, zero-dep, fixture-tested.
- [`internal/signing/`](./internal/signing/) — `Signer` interface +
  V3 JSON keystore (`secp256k1` + EIP-191 personal-sign).

Not yet wired (commits 3–7 per
[plan 0019 §12](../docs/exec-plans/active/0019-secure-orch-trust-spine-design.md)):

- Verifier package under
  [`../livepeer-network-protocol/verify/`](../livepeer-network-protocol/verify/)
  (commit 3).
- Console binary scaffold + audit / inbox / outbox packages
  (commit 4).
- Web UI (Go HTTP server + embedded HTML/CSS/JS for diff + sign)
  (commit 5).
- YubiHSM 2 PKCS#11 signer behind the same `Signer` interface
  (commit 6).
- USB auto-detect + audit-log rotation polish (commit 7).

## Image

`tztcloud/livepeer-secure-orch-console:<tag>`

## Run gestures

```sh
make build      # build dev image locally
make test       # in-container go test ./...
```

`make run` and `make smoke` arrive in commit 4 once the binary lands.

## Documentation

- [`AGENTS.md`](./AGENTS.md) — agent-facing component map + attribution
  for ported code.
- [`DESIGN.md`](./DESIGN.md) — boundaries + architectural discipline.
- [`docs/operator-runbook.md`](./docs/operator-runbook.md) — operator
  guide; ported from the prior reference impl and adapted to the
  secure-orch surface.
- [`docs/threat-model.md`](./docs/threat-model.md) — abbreviated copy
  of plan 0019 §3 for component-local reference.
- [`docs/hsm-setup-yubihsm.md`](./docs/hsm-setup-yubihsm.md) —
  YubiHSM 2 + `yubihsm-shell` install + key gen + audit-log
  configuration. Stub today; landed in commit 6.
