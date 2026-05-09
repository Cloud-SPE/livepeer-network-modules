# AGENTS.md

This is `secure-orch-console/` — the cold-key host's diff-and-sign UX
plus the canonicalization, signing, and verification primitives the
rest of the supply side relies on. Designed against
[`../docs/exec-plans/completed/0019-secure-orch-trust-spine-design.md`](../docs/exec-plans/completed/0019-secure-orch-trust-spine-design.md).

Component-local agent map. The repo-root [`../AGENTS.md`](../AGENTS.md)
is the cross-cutting map; this file scopes to console-specific
guidance.

## Bind posture

Loopback-only remains the recommended deployment posture for the cold
key host, but the console now leaves the exact bind address to the
operator. `--listen` must be an explicit `host:port`; ambiguous
all-interface shorthand such as `:8080` is rejected.

## Operating principles

Inherited from the repo root, plus:

- **The cold key never crosses a host boundary.** v0.1 holds it as a
  V3 JSON keystore on the secure-orch host. Hardware-backed signers
  are out of scope for v0.1 (plan 0019 §13 Q1 + §14).
- **No auto-sign.** The operator-confirm gesture is not skippable.
  Resolver-side replay protection makes a fresh sign cheap; that is
  not a license to skip the diff review.
- **Bytes-identical canonicalization** with the resolver / coordinator
  / gateway verify path. The `internal/canonical/` package is the
  single source of truth on both sides of the sign/verify boundary.
- **Audit log is append-only.** Every console gesture (load candidate,
  view diff, sign, write outbox) emits a JSONL record. Rotation is
  size-based; entries are never edited.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Architectural overview | [`DESIGN.md`](./DESIGN.md) |
| Operator-grade runbook (boot, sign, recover, rotation) | [`docs/operator-runbook.md`](./docs/operator-runbook.md) |
| Threat model | [`docs/threat-model.md`](./docs/threat-model.md) |
| Build / run / test gestures | [`Makefile`](./Makefile) |

## Package layout

```
cmd/
  secure-orch-console/      — main binary (web-server entrypoint)
  secure-orch-keygen/       — cold-key generation helper
internal/
  canonical/                — JCS-equivalent canonical bytes
  signing/                  — Signer iface + V3 keystore
  diff/                     — candidate-vs-last-signed structural diff
  audit/                    — rolling JSONL audit log with size-based rotation
  config/                   — operator config (keystore path, last-signed path, listen, audit log)
web/                        — Go HTTP server + embedded HTML/CSS templates
                              (handles candidate upload, diff render, sign confirm)
testdata/                   — canonical-bytes fixtures
```

Manifest transport is HTTP-only via the web UI; there is no inbox or
outbox spool dir. Operators upload candidates as a multipart form;
the signed envelope is returned as a download attachment and
mirrored atomically to `last-signed.json`.

The verifier lives in
[`../livepeer-network-protocol/verify/`](../livepeer-network-protocol/verify/)
because resolvers, coordinators, and gateways all need it.

## Code-of-conduct

- The canonicalizer is zero-dep stdlib only. No third-party JCS lib
  (plan 0019 §13 Q4 lock).
- Anything that calls `net.Listen` / `http.Server.Addr` /
  `ListenAndServe` MUST require an explicit `host:port`. Never accept
  ambiguous all-interface shorthand such as `:<port>`.
- `Signer` is the abstraction in front of the cold-key holder. v0.1
  wires V3 keystore only; the interface is small and abstract enough
  that a hardware-backed signer can land later without changing call
  sites.
- Static web assets live under `web/` and are embedded via `embed.FS`.
  No frontend build step in the monorepo.

## Attribution — ports from the prior reference impl

Per repo-root [`../AGENTS.md`](../AGENTS.md) lines 62–66, code copied
in from a named source repo records what was copied, from where, and
that the user authorized the copy. Plan 0019 §12 commit 2 + §4.1
authorize the verbatim port of the canonicalizer + signer from
`livepeer-modules-project/service-registry-daemon/` (a reference
impl outside `livepeer-network-suite`):

| Local path | Source path | Notes |
|---|---|---|
| `internal/canonical/canonical.go` | `service-registry-daemon/internal/types/canonical.go` lines 1–127 | Tree-walker is a verbatim port. The wrapper is type-agnostic for the new manifest shape (the prior impl baked a manifest-specific zeroing pass). |
| `internal/signing/signer.go` | `service-registry-daemon/internal/providers/signer/signer.go` | V3-keystore `Signer` impl. Address type is local rather than imported from a `types` package. |
| `../livepeer-network-protocol/verify/verifier.go` | `service-registry-daemon/internal/providers/verifier/verifier.go` | Recover is symmetric with the signer. |
| `docs/operator-runbook.md` | `service-registry-daemon/docs/operations/running-the-daemon.md` | Adapted to the secure-orch (cold-key) operator surface — publisher mode only, no resolver/discovery flags. |
