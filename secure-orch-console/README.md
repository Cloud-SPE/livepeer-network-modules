# `secure-orch-console/`

The cold-key host's diff-and-sign console. Runs on `secure-orch` — the
firewalled machine that holds the orchestrator's cold key. The
operator drives the sign cycle:

1. Coordinator builds a candidate manifest and exposes it for download.
2. Operator opens the secure-orch console over `ssh -L`-tunneled
   port-forward and uploads the candidate via the web form.
3. Operator reviews a structural diff against the last-signed
   manifest, types the last 4 hex chars of the signer eth address to
   confirm, and signs.
4. Console returns the signed envelope as a download attachment and
   atomically updates `last-signed.json`.
5. Operator uploads `signed.json` to the coordinator's web UI;
   coordinator publishes at `/.well-known/livepeer-registry.json`.

Designed against
[`../docs/exec-plans/completed/0019-secure-orch-trust-spine-design.md`](../docs/exec-plans/completed/0019-secure-orch-trust-spine-design.md).

## Bind address

Loopback-only remains the recommended deployment posture. The console
now accepts any explicit `host:port` in `--listen`, so operators can
choose `127.0.0.1:8080`, `0.0.0.0:8080`, or a specific interface. The
binary rejects only ambiguous all-interface shorthand such as `:8080`.

## Status — v0.1

v0.1 ships:

- [`internal/canonical/`](./internal/canonical/) — JCS-equivalent
  canonical-bytes algorithm, zero-dep, fixture-tested.
- [`internal/signing/`](./internal/signing/) — `Signer` interface +
  V3 JSON keystore (`secp256k1` + EIP-191 personal-sign).
- [`internal/audit/`](./internal/audit/) — append-only JSONL audit
  log with size-based rotation.
- [`internal/diff/`](./internal/diff/) — structural diff against
  last-signed (header + per-tuple keyed on `(capability_id, offering_id)`).
- [`internal/config/`](./internal/config/) — operator config +
  explicit-listen-address validation.
- [`web/`](./web/) — HTTP server with embedded HTML/CSS templates for
  login, diff renderer, and tap-to-sign confirm.
- [`cmd/secure-orch-console/`](./cmd/secure-orch-console/) — main
  binary. Wires V3-keystore signer through.
- [`cmd/secure-orch-keygen/`](./cmd/secure-orch-keygen/) — cold-key
  generation helper.

Cross-cutting verifier (used by resolver / coordinator / gateway)
lives at
[`../livepeer-network-protocol/verify/`](../livepeer-network-protocol/verify/).

Manifest transport is HTTP-only via the web UI: no inbox or outbox
spool dirs, no filesystem watcher, no USB. Hardware-backed signers
(YubiHSM 2, Ledger, PKCS#11) are explicitly out of scope for v0.1
(plan 0019 §13 Q1 + §14).

When `SECURE_ORCH_ADMIN_TOKENS` is set, the console requires an
operator login with admin token + actor identity and records the actor
into audit events. The login is a single active session with a 12-hour
absolute timeout and a 30-minute idle timeout.

When `PROTOCOL_DAEMON_SOCKET` is set, the console also renders a
protocol status panel using the local `protocol-daemon` unix socket.

## Image

`tztcloud/livepeer-secure-orch-console:<tag>`

## Run gestures

```sh
make build      # build dev image locally
make test       # in-container go test ./...
make smoke      # full -race test suite in container
make run        # foreground the binary; default bind 127.0.0.1:8080
```

## Documentation

- [`AGENTS.md`](./AGENTS.md) — agent-facing component map + attribution
  for ported code.
- [`DESIGN.md`](./DESIGN.md) — boundaries + architectural discipline.
- [`docs/operator-runbook.md`](./docs/operator-runbook.md) — operator
  guide; ported from the prior reference impl and adapted to the
  secure-orch surface.
- [`docs/threat-model.md`](./docs/threat-model.md) — abbreviated copy
  of plan 0019 §3 for component-local reference.
